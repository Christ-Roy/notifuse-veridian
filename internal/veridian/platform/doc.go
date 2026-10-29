// Package platform est le contrat entre le socle Veridian (internal/app/veridian_wiring.go) et les
// capacités Veridian (internal/veridian/<module>), lot L0 du 29/10/2026.
//
// Une capacité est un paquet qui, dans init(), appelle platform.Register(Capability{...}). Le fichier
// internal/app/veridian_mod_<module>.go ne contient que l'import blank de ce paquet : un fichier par
// capacité, donc aucun conflit de fusion entre lots et aucune édition de app.go.
// Le paquet ne dépend jamais de internal/app (il reçoit un Host), les capacités non plus.
package platform
