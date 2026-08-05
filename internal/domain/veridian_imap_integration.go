package domain

// Veridian fork — Intégration IMAP self-service (Lot 1 sprint cold outbound,
// 2026-06-15).
//
// BRIQUE FONDATRICE. Permet à Notifuse de POLLER lui-même une boîte IMAP dont
// les credentials sont saisis 100 % via la config / l'UI (par un non-dev, zéro
// script externe). Deux lots downstream consomment ce poller comme handlers :
//
//   - bounce-loop  : détecte les NDR (Non-Delivery Reports) du relai Postfix
//     cold outbound qui arrivent par mail dans la boîte de retour, et alimente
//     la suppression-list / le rate-limit.
//   - stop-on-reply : détecte une réponse humaine d'un prospect et stoppe la
//     séquence cold le concernant.
//
// Les deux s'enregistrent via VeridianIMAPConsumer.OnNewMessage sans se gêner :
// le poller dispatche CHAQUE message neuf à TOUS les consumers enregistrés
// (best-effort, isolé : le panic/erreur d'un consumer ne bloque pas l'autre).
//
// Stockage des creds : sur l'Integration du workspace (type IntegrationTypeIMAP),
// persisté en JSON blob dans la colonne `integrations` et chiffré au repos via
// le MÊME pattern que SMTP (EncryptString/DecryptFromHexString avec la
// passphrase = config.Security.SecretKey). Aucune migration pour les creds —
// exactement comme les rates/caps par infra (R2). Cf. table "Diffs INLINE" du
// CLAUDE.md : workspace.go gagne 3 `case IntegrationTypeIMAP` (Validate /
// BeforeSave / AfterLoad).
//
// Tracking d'idempotence (UID déjà vus) : table SYSTÈME veridian_imap_uid_seen
// (migration V50), keyée par (workspace_id, integration_id, folder,
// uid_validity, uid). Durable → survit aux redémarrages worker (un token-bucket
// en mémoire ne le garantirait pas). Le uid_validity est CRUCIAL en IMAP : si
// le serveur le change, tous les anciens UID sont invalidés (RFC 3501 §2.3.1.1).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

//go:generate mockgen -destination mocks/mock_veridian_imap_uid_seen_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianIMAPUIDSeenRepository
//go:generate mockgen -destination mocks/mock_veridian_imap_consumer.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianIMAPConsumer

// IntegrationTypeIMAP est le type d'intégration pour une boîte IMAP pollée par
// Notifuse (réception : bounces, réponses). Distinct de IntegrationTypeEmail
// qui est l'ÉMISSION (EmailProvider).
const IntegrationTypeIMAP IntegrationType = "imap"

// DefaultIMAPFolder est le dossier scruté par défaut.
const DefaultIMAPFolder = "INBOX"

// DefaultIMAPPollingInterval est l'intervalle de polling par défaut quand la
// config ne le précise pas (ou le précise hors bornes).
const DefaultIMAPPollingInterval = 2 * time.Minute

// minIMAPPollingInterval borne le bas pour éviter qu'une config absurde
// (ex : 1s) ne martèle un serveur IMAP tiers et ne se fasse throttle/bannir.
const minIMAPPollingInterval = 30 * time.Second

// IMAPSettings contient la configuration d'une boîte IMAP à poller.
//
// Champs sensibles (Password) chiffrés au repos : seul EncryptedPassword est
// persisté ; Password (clair) ne vit qu'en mémoire (runtime), comme le pattern
// SMTPSettings.Password / EncryptedPassword.
type IMAPSettings struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	EncryptedPassword string `json:"encrypted_password,omitempty"`
	UseTLS            bool   `json:"use_tls"`
	Folder            string `json:"folder,omitempty"`

	// PollingIntervalSeconds : période de scrutation pour CETTE boîte. 0 =>
	// DefaultIMAPPollingInterval. Borné en bas par minIMAPPollingInterval.
	PollingIntervalSeconds int `json:"polling_interval_seconds,omitempty"`

	// Password : mot de passe en clair, NON persisté (runtime uniquement).
	Password string `json:"password,omitempty"`
}

// MarshalJSON masque le mot de passe IMAP EN CLAIR (champ runtime `Password`)
// à toute sérialisation JSON sortante.
//
// Pourquoi : AfterLoad (workspace.go, case IntegrationTypeIMAP) déchiffre
// EncryptedPassword -> Password pour alimenter le poller runtime. Cet objet
// déchiffré est ensuite renvoyé TEL QUEL par les handlers qui sérialisent un
// Workspace (ex. `GET /api/workspaces.get`) — sans ce masquage, le password
// IMAP en clair fuit dans la réponse API à tout MEMBRE du workspace (fuite de
// secret, ticket 2026-06-15-fuite-password-imap-workspaces-get.md). Le poller
// lit le champ Go directement (jamais via un unmarshal de la réponse API),
// donc masquer en sortie ne casse rien côté runtime.
//
// Bonus : le même `json.Marshal` sert AUSSI la persistance DB (Integration.Value
// -> json.Marshal). BeforeSave (IMAP) chiffre le password mais ne vide PAS le
// clair => sans ce masquage, le clair partait aussi DANS LE BLOB `integrations`
// (secret au repos). Ce MarshalJSON ferme les deux fuites d'un coup.
//
// On garde EncryptedPassword ici car le même MarshalJSON sert à la persistance
// DB. La couche HTTP workspace clone ensuite l'objet et retire aussi ce
// ciphertext avant toute réponse API.
//
// `type Alias IMAPSettings` casse la récursion (l'Alias n'hérite pas de la
// méthode MarshalJSON). UnmarshalJSON n'est PAS affecté (décodage entrant
// create/update intégration IMAP inchangé : le password entrant est lu, chiffré,
// persisté normalement).
func (s IMAPSettings) MarshalJSON() ([]byte, error) {
	type Alias IMAPSettings
	clone := Alias(s)
	clone.Password = "" // ne jamais sérialiser le mot de passe en clair
	return json.Marshal(clone)
}

// GetFolder retourne le dossier à scruter, avec fallback sur INBOX.
func (s *IMAPSettings) GetFolder() string {
	if s == nil || strings.TrimSpace(s.Folder) == "" {
		return DefaultIMAPFolder
	}
	return s.Folder
}

// GetPollingInterval retourne l'intervalle de polling effectif (borné).
func (s *IMAPSettings) GetPollingInterval() time.Duration {
	if s == nil || s.PollingIntervalSeconds <= 0 {
		return DefaultIMAPPollingInterval
	}
	d := time.Duration(s.PollingIntervalSeconds) * time.Second
	if d < minIMAPPollingInterval {
		return minIMAPPollingInterval
	}
	return d
}

// Address retourne "host:port" pour le dial.
func (s *IMAPSettings) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// EncryptPassword chiffre Password vers EncryptedPassword (même pattern SMTP).
func (s *IMAPSettings) EncryptPassword(passphrase string) error {
	encrypted, err := crypto.EncryptString(s.Password, passphrase)
	if err != nil {
		return fmt.Errorf("failed to encrypt IMAP password: %w", err)
	}
	s.EncryptedPassword = encrypted
	return nil
}

// DecryptPassword déchiffre EncryptedPassword vers Password (même pattern SMTP).
func (s *IMAPSettings) DecryptPassword(passphrase string) error {
	password, err := crypto.DecryptFromHexString(s.EncryptedPassword, passphrase)
	if err != nil {
		return fmt.Errorf("failed to decrypt IMAP password: %w", err)
	}
	s.Password = password
	return nil
}

// Validate vérifie la config IMAP et chiffre le password si présent.
//
// Suit la convention de SMTPSettings.Validate : la validation chiffre les
// secrets en place (Password -> EncryptedPassword) pour que le caller persiste
// directement la struct prête à sauver. Idempotent : si Password est vide mais
// EncryptedPassword est déjà posé (re-save d'une intégration existante sans
// retoucher le mot de passe), on ne re-chiffre pas.
func (s *IMAPSettings) Validate(passphrase string) error {
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("host is required for IMAP configuration")
	}
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("invalid port number for IMAP configuration: %d", s.Port)
	}
	if strings.TrimSpace(s.Username) == "" {
		return fmt.Errorf("username is required for IMAP configuration")
	}
	// Le mot de passe est obligatoire à la création (pas d'IMAP anonyme dans
	// notre cas d'usage). On accepte un Password vide UNIQUEMENT si un
	// EncryptedPassword existe déjà (édition sans changer le mot de passe).
	if s.Password == "" && s.EncryptedPassword == "" {
		return fmt.Errorf("password is required for IMAP configuration")
	}

	if s.Password != "" {
		if err := s.EncryptPassword(passphrase); err != nil {
			return fmt.Errorf("failed to encrypt IMAP password: %w", err)
		}
	}
	return nil
}

// VeridianIMAPMessage est le DTO neutre transmis aux consumers (lots 2/3). Il
// découple les handlers de la lib IMAP concrète (go-imap/v2) : un consumer ne
// dépend QUE de domain, jamais de imapclient. Champs minimaux nécessaires au
// bounce-detection (From/Subject/Body/headers) et au reply-detection
// (From/InReplyTo/References/MessageID).
type VeridianIMAPMessage struct {
	// UID + UIDValidity + Folder identifient le message de manière stable côté
	// serveur (pour audit / dédup amont éventuelle).
	UID         uint32
	UIDValidity uint32
	Folder      string

	// WorkspaceID + IntegrationID : à quel tenant / boîte appartient ce message
	// (un consumer multi-tenant route dessus).
	WorkspaceID   string
	IntegrationID string

	// Enveloppe parsée.
	MessageID  string
	InReplyTo  string   // header In-Reply-To (1ère valeur)
	References []string // header References (chaîne de threading)
	From       string   // adresse expéditeur (mailbox@host)
	To         []string
	Subject    string
	Date       time.Time

	// RawBody : corps brut du message (RFC822 / section "" ou TEXT selon ce que
	// le poller a fetché). Peut être tronqué/absent en cas d'erreur de fetch —
	// les consumers DOIVENT tolérer un body vide (best-effort).
	RawBody []byte
}

// VeridianIMAPConsumer est le CONTRAT que les lots 2 (bounce-loop) et 3
// (stop-on-reply) implémentent. Le poller appelle OnNewMessage pour chaque
// message JAMAIS VU (UID neuf) de chaque boîte pollée.
//
// Garanties offertes par le poller :
//   - OnNewMessage est appelé AU PLUS UNE FOIS par (workspace, integration,
//     folder, uid_validity, uid) sur la durée de vie de la table uid_seen
//     (idempotence durable, survit aux redémarrages).
//   - L'appel est synchrone dans la goroutine du poller. Un consumer NE DOIT
//     PAS bloquer longtemps (faire un travail rapide ou enfiler en async).
//   - Une erreur OU un panic d'un consumer est capturé, loggué, et N'EMPÊCHE
//     PAS les autres consumers de recevoir le message ni le poller de continuer.
//   - Name() sert au logging / à la désambiguïsation.
//
// IMPORTANT pour lots 2/3 : tous les consumers enregistrés sont obligatoires.
// Si l'un retourne une erreur ou panique, les autres sont quand même appelés,
// mais l'UID reste non acquitté et sera rejoué. Les consumers doivent donc être
// idempotents. Ce choix évite de perdre durablement un bounce ou une réponse sur
// une panne DB transitoire.
type VeridianIMAPConsumer interface {
	// Name identifie le consumer (ex: "bounce-loop", "stop-on-reply").
	Name() string
	// OnNewMessage traite un message neuf. Une erreur est loggée et empêche
	// l'acquittement de l'UID afin que le message soit rejoué.
	OnNewMessage(msg *VeridianIMAPMessage) error
}

// VeridianIMAPUIDSeenRepository persiste les UID déjà traités (idempotence
// durable). Table système veridian_imap_uid_seen (migration V50).
//
// La clé logique est (workspace_id, integration_id, folder, uid_validity, uid).
// Inclure uid_validity dans la clé est obligatoire : si le serveur IMAP change
// le UIDVALIDITY d'un dossier, les anciens UID ne désignent plus les mêmes
// messages — on doit alors les re-considérer comme neufs (RFC 3501).
type VeridianIMAPUIDSeenRepository interface {
	// FilterUnseen retourne, parmi `uids`, ceux qui n'ont PAS encore été vus
	// pour la clé (workspaceID, integrationID, folder, uidValidity). Best-effort
	// côté caller : en cas d'erreur DB, le poller doit choisir de SKIP (ne rien
	// traiter) plutôt que de risquer un double-traitement.
	FilterUnseen(
		ctx context.Context,
		workspaceID, integrationID, folder string,
		uidValidity uint32,
		uids []uint32,
	) ([]uint32, error)

	// MarkSeen marque un lot d'UID comme traités (idempotent : ON CONFLICT DO
	// NOTHING). Appelé APRÈS dispatch aux consumers.
	MarkSeen(
		ctx context.Context,
		workspaceID, integrationID, folder string,
		uidValidity uint32,
		uids []uint32,
	) error
}
