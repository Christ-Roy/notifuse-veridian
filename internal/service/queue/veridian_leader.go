package queue

import "github.com/Notifuse/notifuse/internal/veridian/leader"

// VeridianFollowLeader : le worker d'envoi ne prend d'entrées que sur l'exemplaire qui mène (internal/veridian/leader, VER-270).
// Ses portes de débit, l'étalement et les disjoncteurs vivent en mémoire : deux workers enverraient deux fois plus vite.
func (w *EmailQueueWorker) VeridianFollowLeader(g leader.Gate) {
	w.workspaceRepo = leader.Follow(w.workspaceRepo, g)
}

// VeridianFollowLeader : une seule relève IMAP des boîtes des clients (deux exemplaires doubleraient les connexions, Gmail limite).
func (s *VeridianIMAPPollerService) VeridianFollowLeader(g leader.Gate) {
	s.workspaceRepo = leader.Follow(s.workspaceRepo, g)
}
