// Package reservoir alimente et active les réservoirs de prospects (fiche 85) : import groupé dédoublonné par adresse,
// SIREN et domaine, exclusion des désinscrits, rebonds et plaintes, état des listes, activation par lots dans une
// automation. Rien ici ne touche aux plafonds d'envoi : activer 1000 contacts ne fait pas partir un mail de plus par jour.
package reservoir

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/Notifuse/notifuse/internal/domain"
)

const (
	// MaxBatch : contacts par appel (import comme activation).
	MaxBatch   = 1000
	maxDetails = 50
	maxLists   = 20
)

// ErrInvalid : demande refusée, le message est pour l'appelant (400).
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// Raisons d'écart, rendues telles quelles à l'appelant.
const (
	ReasonBadEmail     = "email_invalide"
	ReasonBadContact   = "contact_invalide"
	ReasonBatchEmail   = "doublon_email_dans_le_lot"
	ReasonBatchSiren   = "doublon_siren_dans_le_lot"
	ReasonBatchDomain  = "doublon_domaine_dans_le_lot"
	ReasonSiren        = "siren_deja_present"
	ReasonDomain       = "domaine_deja_present"
	ReasonUnsubscribed = "desinscrit"
	ReasonBounced      = "rebond"
	ReasonComplained   = "plainte"
	ReasonWriteFailed  = "ecriture_refusee"
)

// Que faire d'une adresse déjà dans l'espace.
const (
	ExistingAttach = "attach" // défaut : fiche intacte, ajoutée aux listes demandées
	ExistingUpdate = "update" // fiche complétée (les champs fournis remplacent), ajoutée aux listes
	ExistingSkip   = "skip"   // rien
)

// Dedupe : clés de dédoublonnage en plus de l'adresse (toujours active).
type Dedupe struct{ Siren, Domain bool }

// ParseDedupe : absent = les trois. "email" est accepté et ne change rien.
func ParseDedupe(keys []string) (Dedupe, error) {
	if keys == nil {
		return Dedupe{Siren: true, Domain: true}, nil
	}
	var d Dedupe
	for _, k := range keys {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "email":
		case "siren":
			d.Siren = true
		case "domain", "domaine":
			d.Domain = true
		default:
			return d, invalid("dedupe accepte email, siren, domain (reçu %q)", k)
		}
	}
	return d, nil
}

// Row : un contact reçu, normalisé.
type Row struct {
	Index   int
	Email   string
	Siren   string
	Domains []string // domaines d'entreprise (jamais une messagerie partagée)
	Fields  map[string]json.RawMessage
	Extra   map[string]json.RawMessage // versés dans custom_json_1
}

// Detail : un contact écarté.
type Detail struct {
	Index       int    `json:"index"`
	Email       string `json:"email,omitempty"`
	Reason      string `json:"reason"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

// contactFields : champs d'un contact Notifuse. Tout autre nom est une donnée d'enrichissement, gardée dans custom_json_1.
var contactFields = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range []string{"external_id", "timezone", "language", "first_name", "last_name", "full_name", "phone",
		"address_line_1", "address_line_2", "country", "postcode", "state", "job_title"} {
		m[f] = true
	}
	for _, p := range []string{"custom_string_", "custom_number_", "custom_datetime_", "custom_json_"} {
		for i := 1; i <= 5; i++ {
			m[fmt.Sprintf("%s%d", p, i)] = true
		}
	}
	return m
}()

// ignoredFields : jamais repris d'un appelant (dates de fiche, jointures).
var ignoredFields = map[string]bool{"email": true, "created_at": true, "updated_at": true, "contact_lists": true, "contact_segments": true, "email_hmac": true, "workspace_id": true}

var (
	sirenKeys  = []string{"siren", "SIREN", "siret", "SIRET"}
	domainKeys = []string{"domain", "domaine", "website", "site", "site_web", "url"}
	digits     = regexp.MustCompile(`\D`)
)

func rawString(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(r, &n) == nil {
		return n.String()
	}
	return ""
}

// NormalizeSiren : 9 chiffres, depuis un SIREN ou un SIRET, espaces admis. Vide si ce n'en est pas un.
func NormalizeSiren(s string) string {
	d := digits.ReplaceAllString(s, "")
	if (len(d) != 9 && len(d) != 14) || strings.Trim(d, "0") == "" {
		return ""
	}
	return d[:9]
}

// NormalizeDomain : domaine enregistrable (exemple.fr) d'un domaine ou d'une URL, ou sous-domaine du client sur une plateforme. Vide si illisible.
func NormalizeDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || strings.ContainsAny(s, "@ ") {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return ""
	}
	return platformKey(host, d)
}

// sharedExtra : messageries grand public hors des classes d'envoi connues. Deux entreprises y cohabitent.
var sharedExtra = map[string]bool{"icloud.com": true, "me.com": true, "mac.com": true, "proton.me": true, "protonmail.com": true, "pm.me": true,
	"gmx.com": true, "gmx.net": true, "gmx.de": true, "mail.com": true, "yandex.com": true, "yandex.ru": true, "mail.ru": true, "zoho.com": true,
	"tutanota.com": true, "ik.me": true, "netcourrier.com": true, "mailo.com": true, "libertysurf.fr": true, "tele2.fr": true, "voila.fr": true,
	"caramail.com": true, "nordnet.fr": true}

// SharedDomain : messagerie ou plateforme partagée, jamais une clé de dédoublonnage.
func SharedDomain(d string) bool {
	return sharedExtra[d] || hostPlatforms[d] || pathPlatforms[d] || domain.ClassifyProviderClass("x@"+d) != domain.ProviderClassCorporate
}

func addDomain(list []string, d string) []string {
	if d == "" || SharedDomain(d) {
		return list
	}
	for _, x := range list {
		if x == d {
			return list
		}
	}
	return append(list, d)
}

// ParseRow lit un contact. reason non vide : écarté.
func ParseRow(i int, raw json.RawMessage) (row Row, reason string) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return Row{Index: i}, ReasonBadContact
	}
	row = Row{Index: i, Fields: map[string]json.RawMessage{}, Extra: map[string]json.RawMessage{}}
	email, err := domain.VeridianCanonicalContactEmail(rawString(m["email"]))
	if err != nil {
		row.Email = strings.TrimSpace(rawString(m["email"]))
		if len(row.Email) > 120 {
			row.Email = row.Email[:120]
		}
		return row, ReasonBadEmail
	}
	row.Email = domain.NormalizeEmail(email)
	row.Domains = addDomain(nil, NormalizeDomain(row.Email[strings.IndexByte(row.Email, '@')+1:]))

	// Les données d'enrichissement peuvent déjà être dans custom_json_1 : mêmes clés, même lecture.
	var json1 map[string]json.RawMessage
	if r, ok := m["custom_json_1"]; ok {
		if json.Unmarshal(r, &json1) != nil {
			return row, ReasonBadContact
		}
	}
	for _, src := range []map[string]json.RawMessage{m, json1} {
		for _, k := range sirenKeys {
			if row.Siren == "" {
				row.Siren = NormalizeSiren(rawString(src[k]))
			}
		}
		for _, k := range domainKeys {
			row.Domains = addDomain(row.Domains, NormalizeDomain(rawString(src[k])))
		}
	}
	for k, v := range m {
		switch {
		case ignoredFields[k] || string(v) == "null":
		case k == "custom_json_1":
			for kk, vv := range json1 {
				if _, own := row.Extra[kk]; !own {
					row.Extra[kk] = vv
				}
			}
		case contactFields[k]:
			row.Fields[k] = v
		default:
			row.Extra[k] = v
		}
	}
	if row.Siren != "" {
		row.Extra["siren"] = json.RawMessage(`"` + row.Siren + `"`)
	}
	if len(row.Domains) > 0 {
		b, _ := json.Marshal(row.Domains[0])
		if _, ok := row.Extra["domaine"]; !ok {
			row.Extra["domaine"] = b
		}
	}
	return row, ""
}

// Missing : la part de la ligne qui complète une fiche déjà en base, sans jamais écraser une valeur présente (27/11/2026).
// Champs simples : seulement ceux encore vides (st.Filled). Données d'enrichissement (custom_json_1) : seulement les clés absentes.
// ok=false : la fiche n'a rien à recevoir. Le téléphone envoyé par Prospection n'atteignait jamais une fiche existante
// (contacts.importer force existing=attach) : c'est le cas que cela règle.
func (r Row) Missing(st Stored) (Row, bool) {
	out := Row{Index: r.Index, Email: r.Email, Siren: r.Siren, Domains: r.Domains, Fields: map[string]json.RawMessage{}, Extra: map[string]json.RawMessage{}}
	for k, v := range r.Fields {
		if !st.Filled[k] {
			out.Fields[k] = v
		}
	}
	var old map[string]json.RawMessage
	_ = json.Unmarshal(st.JSON1, &old)
	for k, v := range r.Extra {
		if _, ok := old[k]; !ok {
			out.Extra[k] = v
		}
	}
	return out, len(out.Fields) > 0 || len(out.Extra) > 0
}

// Contact construit la fiche. existing : custom_json_1 déjà en base (complété, jamais écrasé en bloc).
func (r Row) Contact(existing json.RawMessage) (*domain.Contact, error) {
	extra := map[string]json.RawMessage{}
	if len(existing) > 0 {
		var old map[string]json.RawMessage
		if json.Unmarshal(existing, &old) == nil {
			for k, v := range old {
				extra[k] = v
			}
		}
	}
	for k, v := range r.Extra {
		extra[k] = v
	}
	obj := map[string]json.RawMessage{}
	for k, v := range r.Fields {
		obj[k] = v
	}
	e, _ := json.Marshal(r.Email)
	obj["email"] = e
	if len(extra) > 0 {
		b, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		obj["custom_json_1"] = b
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	c, err := domain.FromJSON(raw)
	if err != nil {
		return nil, err
	}
	return c, c.Validate()
}

// Known : ce que l'espace sait déjà, pour les clés du lot.
type Known struct {
	Emails  map[string]bool
	Sirens  map[string]string // siren -> adresse qui le porte
	Domains map[string]string
	Blocked map[string]string // adresse -> raison
}

// Plan : ce qui sera écrit.
type Plan struct {
	New      []Row
	Existing []Row
	Skipped  map[string]int
	Details  []Detail
}

func (p *Plan) skip(row Row, reason, of string) {
	if p.Skipped == nil {
		p.Skipped = map[string]int{}
	}
	p.Skipped[reason]++
	if len(p.Details) < maxDetails {
		p.Details = append(p.Details, Detail{Index: row.Index, Email: row.Email, Reason: reason, DuplicateOf: of})
	}
}

// Build décide, contact par contact, dans l'ordre reçu : le premier gagne.
func Build(rows []Row, bad []Detail, k Known, d Dedupe) *Plan {
	p := &Plan{Skipped: map[string]int{}}
	for _, b := range bad {
		p.skip(Row{Index: b.Index, Email: b.Email}, b.Reason, "")
	}
	emails, sirens, domains := map[string]bool{}, map[string]string{}, map[string]string{}
	// Une fiche déjà là tient son SIREN et ses domaines pour tout le lot, quel que soit l'ordre : rejouer un lot n'ajoute personne.
	heldSiren, heldDomain := map[string]string{}, map[string]string{}
	for _, r := range rows {
		if !k.Emails[r.Email] {
			continue
		}
		if r.Siren != "" {
			heldSiren[r.Siren] = r.Email
		}
		for _, dom := range r.Domains {
			heldDomain[dom] = r.Email
		}
	}
	for s, of := range k.Sirens {
		heldSiren[s] = of
	}
	for dom, of := range k.Domains {
		heldDomain[dom] = of
	}
next:
	for _, r := range rows {
		if emails[r.Email] {
			p.skip(r, ReasonBatchEmail, r.Email)
			continue
		}
		emails[r.Email] = true
		if reason := k.Blocked[r.Email]; reason != "" {
			p.skip(r, reason, "")
			continue
		}
		if k.Emails[r.Email] {
			p.Existing = append(p.Existing, r)
			continue
		}
		if d.Siren && r.Siren != "" {
			if of, ok := sirens[r.Siren]; ok {
				p.skip(r, ReasonBatchSiren, of)
				continue
			}
			if of, ok := heldSiren[r.Siren]; ok {
				p.skip(r, ReasonSiren, of)
				continue
			}
		}
		if d.Domain {
			for _, dom := range r.Domains {
				if of, ok := domains[dom]; ok {
					p.skip(r, ReasonBatchDomain, of)
					continue next
				}
				if of, ok := heldDomain[dom]; ok {
					p.skip(r, ReasonDomain, of)
					continue next
				}
			}
		}
		if r.Siren != "" {
			sirens[r.Siren] = r.Email
		}
		for _, dom := range r.Domains {
			domains[dom] = r.Email
		}
		p.New = append(p.New, r)
	}
	return p
}
