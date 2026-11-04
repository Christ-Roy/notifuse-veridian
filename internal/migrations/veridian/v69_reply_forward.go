package veridian

// V69 (lot L1d, fiche 78, 04/11/2026) : journal des copies de reponses de prospects redirigees par
// profil (veridian_reply_forward_to). Une ligne par reponse traitee : la cle primaire rend la copie
// idempotente (le poller IMAP peut relire un message), la table sert de compteur de controle et de
// plafond par profil et par jour. Aucune adresse ni aucun contenu n y est stocke.
// Additive et idempotente : CREATE TABLE IF NOT EXISTS d une table NEUVE (aucun verrou sur une table
// existante, pas d index secondaire). Le tag precedent ignore la table.
func init() {
	Register(Spec{
		Number: 69, Lot: "L1d", Name: "reply_forward",
		Workspace: []string{
			`CREATE TABLE IF NOT EXISTS veridian_reply_forward (
		reply_key   VARCHAR(64) PRIMARY KEY,
		profile_id  VARCHAR(36) NOT NULL,
		recipients  INTEGER NOT NULL DEFAULT 0,
		status      VARCHAR(10) NOT NULL DEFAULT 'pending',
		error_class VARCHAR(40),
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		sent_at     TIMESTAMPTZ
	)`,
		},
	})
}
