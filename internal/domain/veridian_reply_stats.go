package domain

import (
	"context"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_reply_stats_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianReplyStatsService

// Veridian fork — KPI taux de RÉPONSE (reply rate) du cold outbound
// (ticket todo/2026-06-16-kpi-reply-rate-dashboard.md).
//
// En cold outreach, le taux de réponse est LE KPI #1 : c'est la conversion
// réelle d'une campagne, bien plus fiable qu'open/clic (pollués par le MPP
// Apple et les gateways de sécurité qui pré-ouvrent/cliquent). Le signal
// "a répondu" est déjà détecté et stocké durablement par le stop-on-reply
// (Lot 3, table veridian_contact_reply) — mais il n'était exposé NULLE PART
// en KPI. Ce module ferme le trou.
//
// CHOIX D'ARCHITECTURE — endpoint dédié, PAS de greffe dans l'analytics :
//   - La donnée reply vit dans la table SÉPARÉE veridian_contact_reply (1 ligne
//     par contact ayant répondu ≥1 fois, colonne replied_at). Elle n'est PAS
//     dans message_history (le schéma que le moteur analytics générique
//     interroge) : pas de colonne count_replied, pas de jointure dans le moteur.
//   - Greffer reply dans le schéma analytics demanderait une colonne + une
//     jointure dans le moteur générique = invasif, pour une seule métrique qui
//     vit ailleurs. On préfère un endpoint stats dédié, calqué PIXEL sur le
//     breakdown contacts R1 (même handler/service/repo, même auth JWT console)
//     — voie propre et minimale.
//
// Le backend renvoie le COMPTE brut de réponses sur la fenêtre. Le ratio
// replied/sent est calculé côté front avec le count_sent qu'EmailMetricsChart
// charge DÉJÀ (évite une 2e source de vérité pour "sent"). Le reply est par
// CONTACT (1 ligne = 1 contact qui a répondu ≥1 fois), donc replied/sent est
// une approximation de pilotage acceptable — documenté dans le tooltip.

// VeridianReplyStats porte le compte de réponses sur une fenêtre temporelle.
// Seul le compte brut est renvoyé ; le ratio est calculé côté front avec le
// count_sent déjà chargé par le dashboard.
type VeridianReplyStats struct {
	// Replied : nombre de contacts ayant répondu (replied_at dans la fenêtre).
	Replied int `json:"replied"`
}

// VeridianReplyStatsRequest porte les paramètres de la requête de reply stats.
// WorkspaceID est requis. Since/Until délimitent la fenêtre (résolues en UTC
// par le handler depuis start/end ISO). Until.IsZero() = pas de borne haute,
// Since.IsZero() = pas de borne basse → tout l'historique.
type VeridianReplyStatsRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	Since       time.Time `json:"-"`
	Until       time.Time `json:"-"`
}

// VeridianReplyStatsService expose le compte de réponses à la couche HTTP.
// Implémentation : internal/service/veridian_reply_stats_service.go (auth user
// + permission contacts:read avant tout accès données — même gardien que le
// breakdown R1, car le reply est une donnée contact).
type VeridianReplyStatsService interface {
	GetReplyStats(ctx context.Context, req *VeridianReplyStatsRequest) (*VeridianReplyStats, error)
}
