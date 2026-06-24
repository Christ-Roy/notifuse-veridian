# Breakdown contacts — SELECT sans LIMIT charge tout le workspace en RAM (OOM)

> **Sévérité** : 🟡 P1 (OOM possible sur gros workspace cold — charge interne, pas exploitable)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-23 (trouvé en HUNT axe 8 scalabilité)

## Problème
`internal/repository/veridian_contact_breakdown_postgres.go` → `GetProviderClassRows` fait
`SELECT email, custom_string_5 FROM contacts` SANS LIMIT et append TOUTES les lignes dans un
slice Go. Endpoint `POST/GET /api/veridian/contacts.providerBreakdown`. Sur workspace cold
massif (coldtunnel = millions de leads) → OOM container possible à chaque appel admin.

## Fix proposé (préserve "zéro CASE SQL")
Agréger en SQL : `GROUP BY lower(split_part(email,'@',2)), custom_string_5` + count(*) →
millions de rows deviennent milliers de domaines distincts. Le service classifie par domaine
en Go (classification reste en Go) en multipliant par le count agrégé. Adapter
`veridian_contact_breakdown_service.go` pour classifier par (domaine, custom_string_5)
pré-agrégé. Résultat final identique. Test non-régression colocalisé.

## Fichiers
- `internal/repository/veridian_contact_breakdown_postgres.go`
- `internal/service/veridian_contact_breakdown_service.go`
- tests colocalisés

## Contexte
HUNT axe 8 a livré 2 fixes OOM (cache MX 676c8514, IMAP/NDR efa6bfd9). Ce 3e (breakdown)
demande d'adapter le service → ticket pour le faire au calme avec son test.
