package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// VeridianOneClickUnsubscribe rend fonctionnelle la désinscription en un clic de l'en-tête List-Unsubscribe (RFC 8058).
//
// Mesuré en prod le 26/11 (parcours Mail de bout en bout) : le POST d'un client mail sur l'URL de l'en-tête rendait 400.
// Le handler upstream (/unsubscribe-oneclick) ne lit qu'un corps JSON ; un client mail envoie, lui, un formulaire
// « List-Unsubscribe=One-Click » et laisse les paramètres dans l'URL. Gmail et Yahoo exigent ce lien des expéditeurs en
// nombre : il ne désinscrivait personne.
//
// Ce middleware convertit un tel POST en la requête JSON attendue (wid, email, email_hmac, lids, mid lus dans l'URL) avant le
// handler upstream, qui garde toute la vérification (HMAC de l'adresse compris). L'heuristique « robot » du handler ne
// s'applique pas ici : un analyseur de liens suit un GET, il n'envoie pas ce corps explicite ; l'authenticité est tenue par
// le HMAC. Une requête JSON (notification center) passe telle quelle.
func VeridianOneClickUnsubscribe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/unsubscribe-oneclick" || r.Method != http.MethodPost || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		form, _ := url.ParseQuery(string(raw))
		if form.Get("List-Unsubscribe") != "One-Click" {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			next.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		var lids []string
		for _, v := range q["lids"] {
			for _, id := range strings.Split(v, ",") {
				if id = strings.TrimSpace(id); id != "" {
					lids = append(lids, id)
				}
			}
		}
		body, _ := json.Marshal(map[string]interface{}{
			"wid": q.Get("wid"), "email": q.Get("email"), "email_hmac": q.Get("email_hmac"), "lids": lids, "mid": q.Get("mid"),
		})
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("User-Agent", "veridian-rfc8058-one-click")
		next.ServeHTTP(w, r)
	})
}
