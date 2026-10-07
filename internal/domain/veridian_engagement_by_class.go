package domain

import (
	"context"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_engagement_by_class_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianEngagementByClassRepository
//go:generate mockgen -destination mocks/mock_veridian_engagement_by_class_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianEngagementByClassService

// === Veridian patch ===
// Engagement PAR CLASSE de provider destinataire, sur une fenetre de dates, KPI
// dashboard cold outbound.
//
// La classe est lue sur la colonne PERSISTEE message_history.veridian_provider_class
// (posee a l'envoi par le worker, resolution MX comprise : ovh, ionos,
// security_gateway, other_hoster, corporate_selfhost...). L'ancienne version
// deduisait la classe du SUFFIXE du domaine et rangeait donc presque tout en
// "corporate" : on ne s'en sert plus.
//
// Chaque compteur est borne sur SA date : envois par sent_at, rejets par
// bounced_at, reponses humaines par replied_at (jamais sur created_at, qui est la
// date de mise en file : un message cree le 30/09 et envoye le 06/10 compte le 06/10).
// Pas d'ouvertures/clics/livraisons : les mails cold sont en texte brut et le
// relais ne renvoie aucun accuse de livraison.

// VeridianUnclassified regroupe les messages sans classe persistee (anterieurs a
// la classification, ou classe inconnue).
const VeridianUnclassified = "unclassified"

// VeridianClassEngagement porte les compteurs d'UNE classe sur la fenetre.
type VeridianClassEngagement struct {
	Sent         int `json:"sent"`
	Bounced      int `json:"bounced"`
	RepliedHuman int `json:"replied_human"`
}

// VeridianEngagementByClass agrege l'engagement par classe. Toutes les classes
// canoniques + "unclassified" sont presentes (compteurs a 0 si vide) : sortie STABLE.
type VeridianEngagementByClass struct {
	ByClass map[string]VeridianClassEngagement `json:"by_class"`
	Total   VeridianClassEngagement            `json:"total"`
}

// VeridianClassEngagementRow est la projection SQL par classe persistee
// (chaine vide = colonne NULL).
type VeridianClassEngagementRow struct {
	Class        string
	Sent         int
	Bounced      int
	RepliedHuman int
}

// VeridianEngagementByClassRequest porte les paramètres. WorkspaceID requis ;
// Since/Until bornent la fenêtre sur message_history.created_at ([Since, Until[,
// zero time = pas de borne, aligné sur le handler reply stats).
type VeridianEngagementByClassRequest struct {
	WorkspaceID string `json:"workspace_id" valid:"required,alphanum,stringlength(1|20)"`
	Since       time.Time
	Until       time.Time
}

// VeridianEngagementByClassRepository projette l'engagement par classe persistee.
// Implementation Postgres : veridian_engagement_by_class_postgres.go.
type VeridianEngagementByClassRepository interface {
	// GetEngagementByClass compte envois / rejets / reponses humaines par
	// veridian_provider_class sur [since, until[ (zero time = pas de borne).
	GetEngagementByClass(ctx context.Context, workspaceID string, since, until time.Time) ([]VeridianClassEngagementRow, error)
}

// VeridianEngagementByClassService expose le KPI à la couche HTTP (auth user +
// permission contacts:read avant tout accès données, même posture que le
// breakdown contacts R1 et le reply stats).
type VeridianEngagementByClassService interface {
	GetEngagementByClass(ctx context.Context, req *VeridianEngagementByClassRequest) (*VeridianEngagementByClass, error)
}

// VeridianAggregateEngagementByClass range chaque ligne dans sa classe
// canonique (sinon "unclassified") et calcule le total.
func VeridianAggregateEngagementByClass(rows []VeridianClassEngagementRow) *VeridianEngagementByClass {
	classes := VeridianAllProviderClasses()
	byClass := make(map[string]VeridianClassEngagement, len(classes)+1)
	for _, class := range classes {
		byClass[class] = VeridianClassEngagement{}
	}
	byClass[VeridianUnclassified] = VeridianClassEngagement{}

	var total VeridianClassEngagement
	for _, row := range rows {
		key := row.Class
		if _, ok := byClass[key]; !ok {
			key = VeridianUnclassified
		}
		agg := byClass[key]
		agg.Sent += row.Sent
		agg.Bounced += row.Bounced
		agg.RepliedHuman += row.RepliedHuman
		byClass[key] = agg

		total.Sent += row.Sent
		total.Bounced += row.Bounced
		total.RepliedHuman += row.RepliedHuman
	}

	return &VeridianEngagementByClass{ByClass: byClass, Total: total}
}
