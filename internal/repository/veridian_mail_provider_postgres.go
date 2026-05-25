package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ErrWorkspaceNotFoundForMailProvider est retourne par SetMailProviderChoice
// quand le UPDATE n'a touche aucune row (workspace_id inconnu). Sentinel
// dedie pour que le handler puisse mapper en 404 sans depend du repo upstream
// (qui a sa propre erreur ErrWorkspaceNotFound dont la stabilite n'est pas
// garantie par sync upstream).
var ErrWorkspaceNotFoundForMailProvider = errors.New("workspace not found for mail provider lookup")

// veridianMailProviderRepository implemente domain.VeridianMailProviderRepository.
//
// Voir migration V48 + domain/veridian_mail_provider.go pour le contexte
// (ticket todo/2026-05-25-mail-send-as-user-via-hub-gateway.md §3.4).
//
// Pattern aligne sur veridianFrozenMemberRepository : systemDB direct, queries
// stateless, validation argument minimal cote repo (la validation enum est
// faite cote service/handler — le repo fait confiance et compte sur le CHECK
// constraint DB comme dernier rempart).
//
// Pas de patch upstream : ce repo fait des SQL bruts contre la colonne
// `workspaces.mail_provider_choice` ajoutee par V48. Le repo upstream
// `workspace_postgres.go` reste intact (il ne lit/ecrit pas cette colonne).
type veridianMailProviderRepository struct {
	systemDB *sql.DB
}

// NewVeridianMailProviderRepository cree un repo Postgres pour la colonne
// `workspaces.mail_provider_choice`.
func NewVeridianMailProviderRepository(systemDB *sql.DB) domain.VeridianMailProviderRepository {
	return &veridianMailProviderRepository{systemDB: systemDB}
}

// GetMailProviderChoice retourne la preference courante pour un workspace.
//
// Semantique :
//   - workspace existe avec valeur explicite → retourne la valeur (smtp_generic|hub_gmail).
//   - workspace existe avec valeur DB DEFAULT (post-migration V48) → retourne 'smtp_generic'.
//   - workspace n'existe pas → retourne (MailProviderSMTPGeneric, nil).
//     Safe fallback : le defaut DB est le comportement actuel — pas de panic.
//     Le caller doit faire un GetByID workspace AVANT s'il a besoin de
//     distinguer "absent" vs "existant avec default".
//
// Pourquoi pas d'erreur sur workspace absent : le hot path (envoi mail
// transactionnel) ne doit pas planter si le workspace_id passe en parametre
// est vide ou bizarre — il doit retomber sur le SMTP generique sans
// interrompre l'envoi. C'est le service / handler qui fait l'enforcement.
func (r *veridianMailProviderRepository) GetMailProviderChoice(ctx context.Context, workspaceID string) (domain.MailProviderChoice, error) {
	if workspaceID == "" {
		return domain.MailProviderSMTPGeneric, nil
	}
	const q = `SELECT mail_provider_choice FROM workspaces WHERE id = $1`
	var choice string
	err := r.systemDB.QueryRowContext(ctx, q, workspaceID).Scan(&choice)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.MailProviderSMTPGeneric, nil
		}
		return "", fmt.Errorf("get mail_provider_choice: %w", err)
	}
	return domain.MailProviderChoice(choice), nil
}

// SetMailProviderChoice met a jour la preference pour un workspace.
//
// Semantique :
//   - UPDATE 1 row → succes (idempotent : 2 calls avec meme valeur =
//     meme UPDATE et meme RowsAffected=1, l'event est emis a chaque call
//     ecrit par le service car le repo ne sait pas si c'est un re-set).
//   - UPDATE 0 rows → ErrWorkspaceNotFoundForMailProvider (workspace_id
//     inconnu, le handler mappe en 404).
//
// Pas de validation enum cote repo : c'est cote service/handler. Le CHECK
// constraint DB est le filet de securite si une valeur passe au travers.
//
// Pas de UPDATE conditionnel sur la valeur courante (le caller decide s'il
// veut emettre un event ou pas — voir veridian_mail_provider_service.go).
func (r *veridianMailProviderRepository) SetMailProviderChoice(ctx context.Context, workspaceID string, choice domain.MailProviderChoice) error {
	if workspaceID == "" {
		return errors.New("workspace_id required")
	}
	const q = `UPDATE workspaces SET mail_provider_choice = $1 WHERE id = $2`
	res, err := r.systemDB.ExecContext(ctx, q, string(choice), workspaceID)
	if err != nil {
		return fmt.Errorf("set mail_provider_choice: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrWorkspaceNotFoundForMailProvider
	}
	return nil
}
