// Package apinotfound répond 404 à une route /api/* inconnue. L'amont répondait 200 avec un corps vide (RootHandler.Handle) : un appelant
// qui vise une route absente de cette version, ou la mauvaise méthode, lisait un faux « rien » (fiche 46 : 17 faux positifs de
// réconciliation au Hub). Le fichier amont n'est pas touché : « /api/ » est plus précis que « / » et moins que toute route réelle.
package apinotfound

import (
	"encoding/json"
	"net/http"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Code : code stable du refus.
const Code = "route_not_found"

func init() {
	platform.Register(platform.Capability{
		Name: "apinotfound",
		Wire: func(h platform.Host) error {
			if h.Mux() != nil {
				Mount(h.Mux())
			}
			return nil
		},
	})
}

// Mount pose le 404. La racine et /api/health gardent leur 200 : des sondes les lisent.
func Mount(mux *http.ServeMux) {
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "api running"})
		case "/api/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown API route or method: " + r.Method + " " + r.URL.Path, "code": Code})
		}
	})
}
