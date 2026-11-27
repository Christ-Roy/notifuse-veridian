// Package liensclics : détail des clics par lien d'un envoi (reprise amont 35.0, « per-link click breakdown »).
//
// Une carte par message : URL de destination -> nombre de clics, premier et dernier clic. Le rapport d'un envoi
// additionne les cartes de ses messages : clics totaux par URL et cliqueurs uniques (messages distincts ayant
// cliqué l'URL). Les paramètres UTM restent dans l'URL : ce sont souvent eux qui distinguent deux liens.
package liensclics

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Clic : le cumul d'un lien pour un message.
type Clic struct {
	Count   int       `json:"count"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

// Carte : URL de destination -> cumul de clics d'un message.
type Carte map[string]Clic

// Enregistrer ajoute un clic sur l'URL à la carte (créée si nil) et la rend.
// Une URL vide ne compte pas : rien à attribuer.
func Enregistrer(carte Carte, url string, at time.Time) Carte {
	url = strings.TrimSpace(url)
	if url == "" {
		return carte
	}
	if carte == nil {
		carte = Carte{}
	}
	c, ok := carte[url]
	if !ok || c.FirstAt.IsZero() || at.Before(c.FirstAt) {
		c.FirstAt = at
	}
	if at.After(c.LastAt) {
		c.LastAt = at
	}
	c.Count++
	carte[url] = c
	return carte
}

// Decoder lit la colonne JSONB d'un message. Une valeur absente (NULL, vide) donne une carte vide.
func Decoder(brut []byte) (Carte, error) {
	if len(brut) == 0 || string(brut) == "null" {
		return Carte{}, nil
	}
	var carte Carte
	if err := json.Unmarshal(brut, &carte); err != nil {
		return nil, err
	}
	if carte == nil {
		carte = Carte{}
	}
	return carte, nil
}

// Encoder écrit la carte telle qu'elle se range dans la colonne JSONB.
func Encoder(carte Carte) ([]byte, error) {
	return json.Marshal(carte)
}

// Ligne : une ligne du rapport par lien.
type Ligne struct {
	URL       string `json:"url"`
	Clics     int    `json:"clics"`
	Cliqueurs int    `json:"cliqueurs"`
}

// Rapporter additionne les cartes d'un envoi : clics totaux et cliqueurs uniques par URL.
// Ordre : le plus de clics d'abord, puis l'URL, pour un rendu stable.
func Rapporter(cartes []Carte) []Ligne {
	cumul := map[string]*Ligne{}
	for _, carte := range cartes {
		for url, clic := range carte {
			if clic.Count <= 0 {
				continue
			}
			l, ok := cumul[url]
			if !ok {
				l = &Ligne{URL: url}
				cumul[url] = l
			}
			l.Clics += clic.Count
			l.Cliqueurs++
		}
	}
	lignes := make([]Ligne, 0, len(cumul))
	for _, l := range cumul {
		lignes = append(lignes, *l)
	}
	sort.Slice(lignes, func(i, j int) bool {
		if lignes[i].Clics != lignes[j].Clics {
			return lignes[i].Clics > lignes[j].Clics
		}
		return lignes[i].URL < lignes[j].URL
	})
	return lignes
}
