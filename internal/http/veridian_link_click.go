package http

import (
	"context"
	"time"
)

// veridianOnLinkClick : hook du détail des clics par lien (reprise amont 35.0). handleClickRedirection l'appelle
// après les filtres bot et 7 s, avec l'URL cliquée (VisitLink ne la reçoit pas). Par défaut : rien. Branché par la
// capacité veridian « liensclics ». L'implémentation ne doit jamais échouer ni ralentir la redirection : elle
// journalise, borne son délai, et ne renvoie rien.
var veridianOnLinkClick = func(ctx context.Context, workspaceID, messageID, url string, at time.Time) {}

// SetVeridianOnLinkClick branche le hook de clic par lien (capacité veridian « liensclics », reprise amont 35.0).
// Un hook nil est ignoré : la redirection continue sans enregistrement.
func SetVeridianOnLinkClick(f func(ctx context.Context, workspaceID, messageID, url string, at time.Time)) {
	if f != nil {
		veridianOnLinkClick = f
	}
}
