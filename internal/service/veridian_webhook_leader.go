package service

import "github.com/Notifuse/notifuse/internal/veridian/leader"

// VeridianFollowLeader : les livraisons de webhooks sont prises sans verrou par l'amont (GetPendingForWorkspace, pas de
// FOR UPDATE) : deux exemplaires livreraient chacune deux fois. Seul l'exemplaire qui mène livre (VER-270).
func (w *WebhookDeliveryWorker) VeridianFollowLeader(g leader.Gate) {
	w.workspaceRepo = leader.Follow(w.workspaceRepo, g)
}
