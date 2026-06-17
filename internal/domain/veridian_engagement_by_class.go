package domain

import (
	"context"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_engagement_by_class_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianEngagementByClassRepository
//go:generate mockgen -destination mocks/mock_veridian_engagement_by_class_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianEngagementByClassService

// === Veridian patch ===
// Engagement (sent/delivered/bounced/opened/clicked) PAR CLASSE de provider
// destinataire, sur une fenêtre de dates — KPI dashboard cold outbound (ticket
// todo/2026-06-16-kpi-engagement-par-classe-provider.md).
//
// POURQUOI un endpoint dédié et pas une dimension analytics : la classe n'est
// PAS stockée sur message_history (décision Lot 4 — classe dérivée à la lecture,
// jamais matérialisée). Le moteur analytics générique ne peut donc pas GROUP BY
// classe via une dimension SQL. On suit la posture du breakdown contacts R1 :
// le repo agrège par DOMAINE en SQL (cheap, indexé sur created_at), et le
// service mappe chaque domaine → classe via la table de domaines de
// veridian_provider_class.go (réutilisée, JAMAIS dupliquée en CASE SQL).
//
// ⚠️ DÉGRADATION GRACIEUSE MX (assumée, comme le daily-cap classe & le breakdown
// R1) : la classification se fait par SUFFIXE de domaine, PAS par MX réel (zéro
// lookup DNS sur potentiellement des dizaines de milliers de domaines distincts
// d'un dashboard). Un domaine custom hébergé Google/M365/OVH tombe donc en
// `corporate` ici (les classes MX ovh/ionos/… restent à 0). C'est acceptable
// pour un dashboard d'observabilité ; l'enforcement réputation (throttle) utilise
// bien le MX au moment de l'envoi. Cf. CLAUDE.md "dégradation gracieuse MX".

// VeridianClassEngagement porte les compteurs d'engagement d'UNE classe sur la
// fenêtre. Les noms reflètent les mesures du schéma analytics message_history.
type VeridianClassEngagement struct {
	Sent      int `json:"sent"`
	Delivered int `json:"delivered"`
	Bounced   int `json:"bounced"`
	Opened    int `json:"opened"`
	Clicked   int `json:"clicked"`
}

// VeridianEngagementByClass agrège l'engagement par classe de provider
// destinataire. TOUTES les classes canoniques sont présentes (compteurs à 0 si
// vide) pour une sortie STABLE — l'UI rend une ligne par classe sans connaître
// la liste côté front.
type VeridianEngagementByClass struct {
	ByClass map[string]VeridianClassEngagement `json:"by_class"`
	Total   VeridianClassEngagement            `json:"total"`
}

// VeridianDomainEngagementRow est la projection SQL par domaine destinataire :
// le domaine (lower(split_part(contact_email,'@',2))) + les compteurs FILTER.
type VeridianDomainEngagementRow struct {
	Domain    string
	Sent      int
	Delivered int
	Bounced   int
	Opened    int
	Clicked   int
}

// VeridianEngagementByClassRequest porte les paramètres. WorkspaceID requis ;
// Since/Until bornent la fenêtre sur message_history.created_at ([Since, Until[,
// zero time = pas de borne, aligné sur le handler reply stats).
type VeridianEngagementByClassRequest struct {
	WorkspaceID string `json:"workspace_id" valid:"required,alphanum,stringlength(1|20)"`
	Since       time.Time
	Until       time.Time
}

// VeridianEngagementByClassRepository projette l'engagement par domaine.
// Implémentation Postgres : veridian_engagement_by_class_postgres.go.
type VeridianEngagementByClassRepository interface {
	// GetEngagementByDomain agrège sent/delivered/bounced/opened/clicked par
	// domaine destinataire sur la fenêtre [since, until[ (zero time = pas de
	// borne) de message_history du workspace.
	GetEngagementByDomain(ctx context.Context, workspaceID string, since, until time.Time) ([]VeridianDomainEngagementRow, error)
}

// VeridianEngagementByClassService expose le KPI à la couche HTTP (auth user +
// permission contacts:read avant tout accès données, même posture que le
// breakdown contacts R1 et le reply stats).
type VeridianEngagementByClassService interface {
	GetEngagementByClass(ctx context.Context, req *VeridianEngagementByClassRequest) (*VeridianEngagementByClass, error)
}

// VeridianAggregateEngagementByClass classifie chaque ligne de domaine et agrège
// par classe. Toutes les classes canoniques (VeridianAllProviderClasses) sont
// initialisées à 0 pour une sortie stable. La classification réutilise
// ClassifyProviderClass (suffixe pur, override custom_string_5 NON disponible
// ici — on n'a que le domaine ; le tag de classe est une donnée CONTACT, pas
// MESSAGE). Cohérent avec le breakdown, qui lui a accès au tag.
func VeridianAggregateEngagementByClass(rows []VeridianDomainEngagementRow) *VeridianEngagementByClass {
	classes := VeridianAllProviderClasses()
	byClass := make(map[string]VeridianClassEngagement, len(classes))
	for _, class := range classes {
		byClass[class] = VeridianClassEngagement{}
	}

	var total VeridianClassEngagement
	for _, row := range rows {
		class := veridianClassifyDomainEngagement(row.Domain)
		agg := byClass[class]
		agg.Sent += row.Sent
		agg.Delivered += row.Delivered
		agg.Bounced += row.Bounced
		agg.Opened += row.Opened
		agg.Clicked += row.Clicked
		byClass[class] = agg

		total.Sent += row.Sent
		total.Delivered += row.Delivered
		total.Bounced += row.Bounced
		total.Opened += row.Opened
		total.Clicked += row.Clicked
	}

	return &VeridianEngagementByClass{ByClass: byClass, Total: total}
}

// veridianClassifyDomainEngagement classe un DOMAINE NU (déjà extrait par le SQL)
// via la même table de suffixes que ClassifyProviderClass. On reconstruit une
// pseudo-adresse pour réutiliser le helper canonique sans dupliquer la table.
func veridianClassifyDomainEngagement(domain string) string {
	if domain == "" {
		return ProviderClassCorporate
	}
	if class, ok := classifyBySuffix(domain); ok {
		return class
	}
	return ProviderClassCorporate
}
