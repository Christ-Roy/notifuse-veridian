// Package schema porte le DDL de la table des écarts du réconciliateur. Paquet feuille (aucune
// dépendance) : la migration V64 et le module reconcile l'importent sans cycle.
package schema

// Statements : DDL de la table des écarts (migration V64, additive et idempotente).
// Le contrat 3.2 du design pose id, check, severity, subject_ref, expected, observed,
// explanation, proposed_action, first_seen_at, last_seen_at, resolved_at, resolved_by ;
// s'y ajoutent title (libellé court), evidence (la preuve) et resolution (ce qui a été
// fait). Un seul écart ouvert par (check, subject_ref) : index unique partiel.
var Statements = []string{
	`CREATE TABLE IF NOT EXISTS veridian_reconcile_finding (
		id VARCHAR(36) PRIMARY KEY,
		"check" VARCHAR(40) NOT NULL,
		severity VARCHAR(10) NOT NULL,
		subject_ref VARCHAR(255) NOT NULL,
		title VARCHAR(300) NOT NULL DEFAULT '',
		expected TEXT NOT NULL DEFAULT '',
		observed TEXT NOT NULL DEFAULT '',
		explanation TEXT NOT NULL DEFAULT '',
		evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
		proposed_action JSONB,
		first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		resolved_at TIMESTAMPTZ,
		resolved_by VARCHAR(120),
		resolution JSONB
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_veridian_reconcile_finding_open
		ON veridian_reconcile_finding("check", subject_ref) WHERE resolved_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_veridian_reconcile_finding_seen
		ON veridian_reconcile_finding(last_seen_at DESC)`,
}
