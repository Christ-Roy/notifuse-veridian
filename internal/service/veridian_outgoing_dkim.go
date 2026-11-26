package service

import (
	"bytes"
	"context"
	"sync/atomic"

	"github.com/Notifuse/notifuse/internal/domain"
)

// === Veridian (VER-166, fiche 97) === signature DKIM du domaine d'envoi VÉRIFIÉ de l'espace.
//
// SMTPService.SendEmail appelle veridianSignOutgoing juste après la composition MIME, avant tout transport (SMTP brut ou API Gmail) :
// le message part signé d=<domaine du From> quel que soit le profil. Le relais peut ajouter sa propre signature : deux signatures sont
// normales, DMARC retient celle qui est alignée. Le signataire est posé par la capacité domauth au démarrage ; absent, ou s'il ne signe
// pas (domaine non vérifié, clé illisible), le message part tel quel, comme avant.

// VeridianOutgoingSigner rend (message signé, true), ou (nil, false) pour laisser le message intact.
type VeridianOutgoingSigner func(ctx context.Context, workspaceID, fromAddress string, raw []byte) ([]byte, bool)

var veridianOutgoingSigner atomic.Pointer[VeridianOutgoingSigner]

// SetVeridianOutgoingSigner pose (ou retire, nil) le signataire des mails sortants.
func SetVeridianOutgoingSigner(f VeridianOutgoingSigner) {
	if f == nil {
		veridianOutgoingSigner.Store(nil)
		return
	}
	veridianOutgoingSigner.Store(&f)
}

// veridianSignOutgoing remplace le contenu de buf par le message signé quand le signataire signe.
func veridianSignOutgoing(ctx context.Context, request domain.SendEmailProviderRequest, buf *bytes.Buffer) {
	p := veridianOutgoingSigner.Load()
	if p == nil || buf == nil {
		return
	}
	signed, ok := (*p)(ctx, request.WorkspaceID, request.FromAddress, buf.Bytes())
	if !ok || len(signed) == 0 {
		return
	}
	buf.Reset()
	buf.Write(signed)
}
