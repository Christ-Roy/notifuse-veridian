// Package buildinfo expose les métadonnées d'image (tag GHCR, git SHA, date
// de build) injectées au moment du `go build` via `-ldflags -X`. Permet à
// `/api/version` de répondre exactement ce qui tourne, pour que la CI puisse
// valider qu'un redeploy a effectivement remplacé le container — défense
// contre le faux positif "deploy success" qui ne change rien (cf 2026-05-18).
//
// Defaults `dev` quand le binaire est compilé sans -ldflags (run local via
// `go run` ou `make dev`).
package buildinfo

// Tag est le tag GHCR de l'image (ex. "v32.0-veridian.eb7a88e2").
// Injecté via `-X github.com/Notifuse/notifuse/internal/buildinfo.Tag=...`
// par le Dockerfile en consommant l'ARG BUILD_TAG.
var Tag = "dev"

// GitSHA est le SHA git du commit buildé (ex. "eb7a88e2..."). Injecté
// au build via `-X github.com/Notifuse/notifuse/internal/buildinfo.GitSHA=...`
// par le Dockerfile en consommant l'ARG BUILD_SHA.
var GitSHA = "dev"

// BuildDate est la date ISO 8601 du build. Injectée au build via
// `-X github.com/Notifuse/notifuse/internal/buildinfo.BuildDate=...`
// par le Dockerfile en consommant l'ARG BUILD_DATE.
var BuildDate = "dev"
