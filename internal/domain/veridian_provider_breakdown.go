package domain

import "context"

//go:generate mockgen -destination mocks/mock_veridian_contact_breakdown_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianContactProviderBreakdownRepository
//go:generate mockgen -destination mocks/mock_veridian_contact_breakdown_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianContactProviderBreakdownService

// Veridian — breakdown du nombre de contacts par classe de provider
// destinataire (cold outbound, R1 ticket
// todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md).
//
// But métier : permettre de dimensionner le throttle par classe (cf.
// veridian_provider_class.go) en connaissance de cause. Ex : "5000 contacts
// Google à 1 mail/min = 3,5 jours" — l'UI Cold outreach affiche le compte à
// côté de chaque carte de classe.
//
// La classification réutilise STRICTEMENT la sémantique de
// veridian_provider_class.go :
//   - override manuel via le tag contact custom_string_5 (option B) prime ;
//   - sinon fallback sur le suffixe de domaine de l'email (table statique) ;
//   - tout domaine inconnu / email invalide / classe non-canonique → corporate.
//
// On classifie en Go (et non en CASE SQL) pour ne PAS dupliquer la table de
// domaines : une seule source de vérité (veridianProviderDomainTable). Le coût
// est un SELECT de 2 colonnes texte (email + custom_string_5) puis un scan
// O(n) ; sur le volume cold outreach (quelques dizaines de milliers de contacts
// par workspace) c'est négligeable.

// VeridianProviderBreakdown agrège le nombre de contacts par classe de
// provider destinataire. Les 5 classes canoniques sont toujours présentes
// (valeur 0 si aucun contact), pour que l'UI rende des cartes stables sans
// avoir à connaître la liste des classes côté front.
type VeridianProviderBreakdown struct {
	Breakdown map[string]int `json:"breakdown"`
	Total     int            `json:"total"`
}

// VeridianContactProviderRow est la projection minimale d'un contact
// nécessaire à la classification : l'email (suffixe de domaine) et le tag
// custom_string_5 (override de classe). Évite de charger le contact complet.
type VeridianContactProviderRow struct {
	Email         string
	CustomString5 *NullableString
}

// VeridianProviderBreakdownRequest porte les paramètres de la requête de
// breakdown. WorkspaceID est requis ; ListID est optionnel (restreint le
// comptage aux contacts membres de cette liste, hors entrées soft-deleted).
type VeridianProviderBreakdownRequest struct {
	WorkspaceID string `json:"workspace_id" valid:"required,alphanum,stringlength(1|20)"`
	ListID      string `json:"list_id,omitempty" valid:"optional"`
}

// VeridianContactProviderBreakdownRepository projette les colonnes
// nécessaires à la classification. Implémentation Postgres :
// internal/repository/veridian_contact_breakdown_postgres.go.
type VeridianContactProviderBreakdownRepository interface {
	// GetProviderClassRows retourne (email, custom_string_5) pour chaque
	// contact du workspace, restreint à la liste listID si non vide.
	GetProviderClassRows(ctx context.Context, workspaceID, listID string) ([]VeridianContactProviderRow, error)
}

// VeridianContactProviderBreakdownService expose le breakdown à la couche
// HTTP. Implémentation : internal/service/veridian_contact_breakdown_service.go
// (auth user + permission contacts:read avant tout accès données).
type VeridianContactProviderBreakdownService interface {
	GetProviderBreakdown(ctx context.Context, req *VeridianProviderBreakdownRequest) (*VeridianProviderBreakdown, error)
}

// VeridianClassifyContactProviderClass dérive la classe d'un contact selon la
// sémantique canonique : override custom_string_5 d'abord, fallback suffixe
// email ensuite. Centralise la priorité pour qu'agrégat et envoi restent
// strictement alignés.
func VeridianClassifyContactProviderClass(row VeridianContactProviderRow) string {
	if override := veridianProviderClassFromTag(row.CustomString5); override != "" {
		return override
	}
	return ClassifyProviderClass(row.Email)
}

// veridianProviderClassFromTag lit le tag custom_string_5 (override de classe)
// avec la même normalisation que VeridianContactProviderClass : trim, lower,
// validation canonique. Retourne "" si absent/null/non-canonique.
func veridianProviderClassFromTag(tag *NullableString) string {
	c := &Contact{CustomString5: tag}
	return VeridianContactProviderClass(c)
}

// VeridianAggregateProviderBreakdown classifie chaque row et agrège le compte
// par classe. TOUTES les classes canoniques (historiques + MX, cf.
// VeridianAllProviderClasses) sont initialisées à 0 pour une sortie STABLE :
// l'UI rend une carte par classe sans connaître la liste côté front, et les
// classes MX (ovh/ionos/…) apparaissent désormais dans le breakdown.
//
// Note : ce breakdown classifie par SUFFIXE (+ override tag custom_string_5),
// PAS par MX réel — il ne fait pas de lookup DNS (zéro I/O sur potentiellement
// des dizaines de milliers de contacts). Les classes MX n'apparaîtront donc
// peuplées QUE pour les contacts dont le tag custom_string_5 porte déjà la
// classe résolue en amont (pré-remplissage à l'import, ticket Prospection). Sans
// tag, un domaine custom hébergé Google reste compté `corporate` ici — c'est
// cohérent avec le coût/perf d'un breakdown de masse, et l'enforcement réputation
// (throttle) utilise bien le MX au moment de l'envoi.
func VeridianAggregateProviderBreakdown(rows []VeridianContactProviderRow) *VeridianProviderBreakdown {
	classes := VeridianAllProviderClasses()
	breakdown := make(map[string]int, len(classes))
	for _, class := range classes {
		breakdown[class] = 0
	}
	for _, row := range rows {
		class := VeridianClassifyContactProviderClass(row)
		breakdown[class]++
	}
	return &VeridianProviderBreakdown{
		Breakdown: breakdown,
		Total:     len(rows),
	}
}
