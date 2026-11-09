// Package mcp sert le MCP de Notifuse (Streamable HTTP, JSON) derriere la porte unique du Hub
// (veridian-hub/docs/CONTRAT-MCP.md). Le Hub signe l'identite en JWT ES256 ; l'app la verifie, retrouve
// l'utilisateur par hub_user_id (jamais de creation), deduit l'espace de l'utilisateur (jamais d'un
// argument de l'IA) et rejoue la requete sur son propre routeur avec une session courte : les droits
// sont ceux de l'upstream, pas une copie. Fiche 83.
package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims : ce que le Hub porte dans son jeton (CONTRAT-MCP, cote app).
type Claims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Scope         string `json:"scope"`
	ClientID      string `json:"client_id"`
	jwt.RegisteredClaims
}

// ErrUnauthorized : jeton absent, mal signe, expire, mauvais emetteur ou mauvaise audience.
var ErrUnauthorized = errors.New("mcp: unauthorized")

const (
	jwksTTL        = time.Hour       // cache maximal des cles (contrat)
	jwksMinRefetch = 30 * time.Second // un kid inconnu ne declenche pas un rechargement a chaque requete
	maxTokenLife   = 10 * time.Minute // le Hub emet 5 min : un jeton plus long est refuse
	maxJWKSBytes   = 64 << 10
)

// Verifier verifie les JWT du Hub contre son JWKS public.
type Verifier struct {
	JWKSURL  string
	Issuer   string
	Audience string
	HTTP     *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	keys    map[string]*ecdsa.PublicKey
	fetched time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify rend les claims d'un jeton valide (ES256, iss, aud, exp, email verifie, sub present).
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	c := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("kid absent")
		}
		return v.key(ctx, kid)
	},
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer(v.Issuer),
		jwt.WithAudience(v.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.now),
		jwt.WithLeeway(10*time.Second),
	)
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if c.Subject == "" || !c.EmailVerified {
		return nil, fmt.Errorf("%w: sub ou email_verified manquant", ErrUnauthorized)
	}
	if c.ExpiresAt == nil || c.ExpiresAt.Time.After(v.now().Add(maxTokenLife)) {
		return nil, fmt.Errorf("%w: duree de vie excessive", ErrUnauthorized)
	}
	return c, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	age := v.now().Sub(v.fetched)
	if k, ok := v.keys[kid]; ok && age < jwksTTL {
		return k, nil
	}
	if v.keys != nil && age < jwksMinRefetch {
		return nil, errors.New("kid inconnu")
	}
	keys, err := v.fetch(ctx)
	if err != nil {
		return nil, err
	}
	v.keys, v.fetched = keys, v.now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("kid inconnu")
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	hc := v.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: statut %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return nil, err
	}
	return parseJWKS(raw)
}

func parseJWKS(raw []byte) (map[string]*ecdsa.PublicKey, error) {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("jwks illisible: %w", err)
	}
	out := map[string]*ecdsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" {
			continue
		}
		x, errX := base64.RawURLEncoding.DecodeString(k.X)
		y, errY := base64.RawURLEncoding.DecodeString(k.Y)
		if errX != nil || errY != nil {
			continue
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if _, err := pub.ECDH(); err != nil { // point hors courbe
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("jwks: aucune cle ES256 exploitable")
	}
	return out, nil
}
