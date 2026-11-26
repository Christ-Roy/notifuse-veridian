// Package activite pousse l'activité Mail de chaque message vers le Hub, par lots signés (CONTRAT-HUB §5.25, fiche 98) : le Hub en
// fait le dossier « Mail » des fiches du CRM. Une ligne par message, jamais le corps.
package activite

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Bornes du contrat : au-delà, le Hub refuse le lot entier (413).
const (
	MaxEvents = 200
	MaxBytes  = 1_000_000
	// maxSubject : le contrat tronque l'objet à 200 caractères ; les autres champs libres ont la même borne.
	maxSubject = 200
	maxAddress = 320
)

// Natures (§5.25).
const (
	NatureCampaign      = "campaign"
	NatureSequence      = "sequence"
	NatureTransactional = "transactional"
	NatureReplyHuman    = "reply_human"
	NatureReplyAuto     = "reply_auto"
	NatureBounce        = "bounce"
)

// Event : un événement du lot. Aucun champ ne porte un corps, une URL ou ses paramètres : la liste des clés est figée par test.
type Event struct {
	Event          string `json:"event"`
	OccurredAt     string `json:"occurred_at"`
	RFCMessageID   string `json:"rfc_message_id"`
	ThreadID       string `json:"thread_id"`
	RecipientEmail string `json:"recipient_email"`
	FromEmail      string `json:"from_email"`
	FromName       string `json:"from_name"`
	Subject        string `json:"subject"`
	Nature         string `json:"nature"`
	ListID         string `json:"list_id,omitempty"`
	AutomationID   string `json:"automation_id,omitempty"`
	BroadcastID    string `json:"broadcast_id,omitempty"`
	BounceType     string `json:"bounce_type,omitempty"`
	Host           string `json:"host,omitempty"`
}

// Lot : le corps POST. Pas de hub_tenant_id : un désaccord rendrait 409 (le Hub retrouve l'organisation par l'abonnement).
type Lot struct {
	WorkspaceID string  `json:"workspace_id"`
	LotID       string  `json:"lot_id"`
	Events      []Event `json:"evenements"`
}

// Pending : une ligne de la file prête à partir, avec son événement.
type Pending struct {
	ID    int64
	Event Event
}

// Batch : un lot et les lignes de la file qu'il porte.
type Batch struct {
	Lot  Lot
	IDs  []int64
	Body []byte
}

// Split coupe les lignes en lots d'au plus maxEvents événements ET maxBytes octets de corps, dans l'ordre. Un événement seul plus
// gros que la borne (impossible avec des champs bornés) part dans un lot à lui seul plutôt que de bloquer la file.
func Split(ws string, rows []Pending, maxEvents, maxBytes int) []Batch {
	if maxEvents <= 0 || maxEvents > MaxEvents {
		maxEvents = MaxEvents
	}
	if maxBytes <= 0 || maxBytes > MaxBytes {
		maxBytes = MaxBytes
	}
	var out []Batch
	var cur Batch
	size := 0
	flush := func() {
		if len(cur.IDs) == 0 {
			return
		}
		cur.Lot.WorkspaceID = ws
		cur.Lot.LotID = uuid.NewString()
		cur.Body, _ = json.Marshal(cur.Lot)
		out = append(out, cur)
		cur, size = Batch{}, 0
	}
	// Enveloppe : {"workspace_id":"…","lot_id":"<36>","evenements":[]} et une virgule par événement.
	envelope := len(`{"workspace_id":"","lot_id":"","evenements":[]}`) + len(ws) + 36
	for _, r := range rows {
		b, err := json.Marshal(r.Event)
		if err != nil {
			continue
		}
		n := len(b) + 1
		if len(cur.IDs) > 0 && (len(cur.IDs) >= maxEvents || envelope+size+n > maxBytes) {
			flush()
		}
		cur.Lot.Events = append(cur.Lot.Events, r.Event)
		cur.IDs = append(cur.IDs, r.ID)
		size += n
	}
	flush()
	return out
}

// HostOf : l'HÔTE d'un lien, jamais son chemin, ses paramètres (ils peuvent porter des jetons), son port ni ses identifiants.
// Vide pour un lien sans hôte (mailto:, tel:) ou illisible.
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "" || len(h) > 253 || strings.ContainsAny(h, "/?#@ ") {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.String()
	}
	return h
}

// historyNature : la nature d'un envoi. Un rejet ou une désinscription garde la nature du message qu'il touche.
func historyNature(broadcastID, automationID string) string {
	switch {
	case broadcastID != "":
		return NatureCampaign
	case automationID != "":
		return NatureSequence
	default:
		return NatureTransactional
	}
}

// mailNature : la nature d'un reçu ; "" = ce reçu ne part pas (le déclencheur ne retient déjà que ces genres).
func mailNature(kind string) (nature, bounceType string) {
	switch kind {
	case "reply_human":
		return NatureReplyHuman, ""
	case "reply_auto":
		return NatureReplyAuto, ""
	case "bounce_hard":
		return NatureBounce, "hard"
	case "bounce_soft":
		return NatureBounce, "soft"
	}
	return "", ""
}

// bounceTypeOf : hard ou soft depuis message_history.bounce_type (HardBounce, SoftBounce, Permanent, Transient...).
func bounceTypeOf(v string) string {
	s := strings.ToLower(v)
	switch {
	case strings.Contains(s, "hard"), strings.Contains(s, "perm"):
		return "hard"
	case strings.Contains(s, "soft"), strings.Contains(s, "trans"):
		return "soft"
	}
	return ""
}

// clip : au plus n caractères, sans couper un caractère UTF-8, espaces de bord retirés.
func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}

// bareMsgID : un Message-ID RFC sans chevrons ni espaces.
func bareMsgID(s string) string {
	return clip(strings.Trim(strings.TrimSpace(s), "<>"), 998)
}

func cleanEmail(s string) string { return clip(strings.ToLower(s), maxAddress) }
