package app

import (
	"database/sql"

	"github.com/Notifuse/notifuse/internal/veridian/leader"
)

// veridianFollowLeader (VER-270, deux exemplaires en prod) : l'envoi, la relève IMAP, la livraison des webhooks et le
// renouvellement du certificat du relais ne tournent que sur l'exemplaire qui mène (internal/veridian/leader). Le HTTP, le
// relais SMTP, l'IMAP et le MX des boîtes restent servis par tous. Appelé avant le démarrage des workers : tant que
// l'élection n'a pas tranché, aucun exemplaire ne prend d'entrée (au plus un leader, jamais deux).
func (a *App) veridianFollowLeader() leader.Gate {
	var db *sql.DB
	if !a.veridianMockDB {
		db = a.db
	}
	g := leader.New(db, a.logger)
	if a.emailQueueWorker != nil {
		a.emailQueueWorker.VeridianFollowLeader(g)
	}
	if a.webhookDeliveryWorker != nil {
		a.webhookDeliveryWorker.VeridianFollowLeader(g)
	}
	if a.veridianIMAPPoller != nil {
		a.veridianIMAPPoller.VeridianFollowLeader(g)
	}
	if a.veridianRelayCertManager != nil {
		a.veridianRelayCertManager.FollowLeader(g.Leads)
	}
	if e, ok := g.(*leader.Elector); ok {
		go e.Run(a.GetShutdownContext())
	}
	return g
}
