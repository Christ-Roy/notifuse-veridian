package domain

// Veridian fork — Signal durable "a répondu" (Lot 3 sprint cold outbound,
// 2026-06-15) : "stop-on-reply".
//
// SOURCE DE VÉRITÉ du fait qu'un contact a répondu à un de nos envois. Posé par le
// reply-consumer (qui consomme l'IMAP du Lot 1), consommé par :
//   - le gate d'exit de séquence du Lot 9 (ColdReplyChecker.HasReplied) à chaque tick
//     de l'automation executor → sort le contact de la cadence dès qu'il a répondu,
//     y compris au beau milieu d'un délai de relance ;
//   - l'exit ACTIF immédiat des automations en cours (au moment de la détection).
//
// POURQUOI une table dédiée (et pas un custom field / un statut contact_lists) :
//   - custom_string_5 est DÉJÀ occupé (tag classe provider du tunnel cold) — collision.
//   - contact_lists.status ('active'/'unsubscribed'/'bounced'/'complained') porte la
//     sémantique d'ABONNEMENT à une liste, pas l'engagement d'un prospect ; y ajouter
//     'replied' polluerait le sens upstream et casserait IsTerminalContactListStatus.
//   - Un signal binaire durable, requêtable en O(1) par (workspace, email), idempotent
//     (ON CONFLICT DO NOTHING), survivant aux redémarrages worker, mérite sa table —
//     exactement comme veridian_imap_uid_seen (Lot 1) sert l'idempotence du poller, et
//     comme message_history sert le plafond journalier (V49) plutôt qu'un compteur RAM.
//
// Table WORKSPACE (pas système) : la donnée est métier-contact, elle vit dans la DB du
// workspace au même titre que contacts / contact_lists / message_history. Migration V51
// la crée via UpdateWorkspace (+ init.go pour les nouveaux workspaces).
//
// Idempotence métier (CRUCIALE) : le poller IMAP garantit "at-most-once dispatch" mais
// PEUT re-dispatcher un UID si MarkSeen échoue. Donc MarkReplied DOIT être idempotent :
// re-poser le même signal ne fait rien (ON CONFLICT DO NOTHING), et l'exit de séquence
// associé est lui aussi idempotent (un contact déjà 'exited' ne re-déclenche rien).

import (
	"context"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_contact_reply_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianContactReplyRepository

// VeridianContactReply matérialise le signal "ce contact a répondu" (1 ligne par
// contact ayant répondu au moins une fois). On garde le PREMIER signal (le plus ancien),
// ON CONFLICT DO NOTHING : la première réponse est celle qui sort le prospect de la
// cadence ; les réponses suivantes ne changent rien.
type VeridianContactReply struct {
	// ContactEmail : clé (normalisée lowercase). PK de la table (1 contact = 1 signal).
	ContactEmail string `json:"contact_email"`
	// RepliedAt : quand on a détecté la première réponse (Date du mail entrant, ou
	// now() si la Date est absente/aberrante — voir le consumer).
	RepliedAt time.Time `json:"replied_at"`
	// MatchType : par quel signal la réponse a été détectée (audit).
	MatchType VeridianReplyMatchType `json:"match_type"`
	// MatchedMessageID : l'id message_history de l'envoi cité (vide en fallback).
	MatchedMessageID string `json:"matched_message_id,omitempty"`
}

// VeridianContactReplyRepository persiste et interroge le signal 'replied' (table
// WORKSPACE veridian_contact_reply, migration V51). Toutes les méthodes sont
// workspace-scoped (résolution de connexion via WorkspaceRepository.GetConnection).
type VeridianContactReplyRepository interface {
	// MarkReplied pose (idempotemment) le signal 'replied' pour un contact. ON
	// CONFLICT (contact_email) DO NOTHING : le premier signal gagne, re-poser ne
	// fait rien. Best-effort côté caller : une erreur DB ne doit jamais faire échouer
	// le traitement IMAP (le message sera de toute façon marqué vu).
	MarkReplied(ctx context.Context, workspaceID string, reply *VeridianContactReply) error

	// HasReplied retourne true si le contact `email` a déjà été marqué 'replied'.
	// Consommé par le ColdReplyChecker du Lot 9 (gate d'exit) ET par le consumer pour
	// court-circuiter un re-dispatch (fast-path idempotent). `email` est normalisé
	// lowercase par le caller.
	HasReplied(ctx context.Context, workspaceID, email string) (bool, error)
}
