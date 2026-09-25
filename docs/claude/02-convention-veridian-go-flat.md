# Convention `veridian_*.go` flat

Tout code custom dans `internal/{http,service,repository,domain}/` est préfixé
`veridian_*.go` au **même niveau** que les fichiers upstream — pas de
sous-dossier `internal/http/veridian/`.

Raisons : idiomatique Go (packages plats par responsabilité), pas d'import
cycle, grep-friendly (`ls internal/http/veridian_*`), sync upstream triviale
(aucun fichier upstream ne porte ce préfixe).

Règle stricte : **ne jamais patcher un fichier upstream** pour les besoins
Veridian. Si un handler upstream doit être étendu, créer
`veridian_<nom>_handler.go` qui wraps/remplace, et router dessus depuis le mux
Veridian.

Exemples existants : `internal/http/veridian_handler.go`,
`veridian_autologin_handler.go`, `veridian_magic_handler.go`,
`internal/domain/veridian.go`, `veridian_token.go`.

