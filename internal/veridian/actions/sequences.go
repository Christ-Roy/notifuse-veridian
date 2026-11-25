package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Inscrire une personne à une séquence depuis une autre app (CRM, Prospection ; fiche 87). Le geste passe par les mêmes routes que la console et
// l'IA : contacts.intake (fiche créée ou gardée intacte, désinscrits, rebonds et plaintes refusés) puis reservoir.activate (séquence active, déjà
// répondu, déjà en séquence, déjà contacté). Rien ne part « tout de suite » : fenêtre, plafonds, chauffe et listes noires décident à l'envoi.

const (
	maxEnrollPerCall = 50
	// maxEnrollPerDay : par utilisateur et par espace, toutes apps confondues.
	maxEnrollPerDay = 200
)

// Les états rendus par reservoir.activate, dans les mots du contrat.
var enrollReasons = map[string]string{"plainte": "plainte", "desinscrit": "desinscrit", "rebond": "rebond", "replied": "a_deja_repondu",
	"already": "deja_dans_la_sequence", "in_sequence": "dans_une_autre_sequence", "contacted": "deja_contacte"}

// Les refus de contacts.intake, dans les mêmes mots.
var intakeReasons = map[string]string{"plainte": "plainte", "desinscrit": "desinscrit", "rebond": "rebond", "email_invalide": "adresse_invalide",
	"contact_invalide": "fiche_refusee", "ecriture_refusee": "fiche_refusee"}

type enrollContact struct {
	Email           string `json:"email"`
	FirstName       string `json:"first_name,omitempty"`
	LastName        string `json:"last_name,omitempty"`
	Phone           string `json:"phone,omitempty"`
	JobTitle        string `json:"job_title,omitempty"`
	NePlusContacter bool   `json:"ne_plus_contacter,omitempty"`
}

type enrollRequest struct {
	SequenceID string          `json:"sequence_id"`
	Contacts   []enrollContact `json:"contacts"`
	// InclureDejaContactes : reprendre une personne qui a déjà reçu un mail commercial (choix explicite de l'utilisateur).
	InclureDejaContactes bool `json:"inclure_deja_contactes"`
	DryRun               bool `json:"dry_run"`
}

// Enrolled : un résultat par personne.
type Enrolled struct {
	DryRun   bool           `json:"dry_run"`
	Received int            `json:"received"`
	Inscrits int            `json:"inscrits"`
	Skipped  map[string]int `json:"skipped"`
	Details  []EnrollDetail `json:"details"`
	Note     string         `json:"note"`
}

// EnrollDetail : ce qui est arrivé à une personne. Motif vide : inscrite (ou le serait, en simulation).
type EnrollDetail struct {
	Email string `json:"email"`
	Motif string `json:"motif,omitempty"`
}

const enrollNote = "rien ne part tout de suite : la fenêtre, les plafonds et les listes noires de l'espace décident à l'envoi"

func buildEnroll(ws, app string, raw []byte) (*call, error) {
	var req enrollRequest
	if err := strict(raw, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.SequenceID) == "" {
		return nil, errors.New("sequence_id requis")
	}
	if len(req.Contacts) == 0 || len(req.Contacts) > maxEnrollPerCall {
		return nil, fmt.Errorf("1 à %d personnes par appel", maxEnrollPerCall)
	}
	motif, order, seen, keep := map[string]string{}, []string{}, map[string]bool{}, []map[string]any{}
	for _, c := range req.Contacts {
		e := domain.NormalizeEmail(c.Email)
		if seen[e] {
			continue
		}
		seen[e] = true
		order = append(order, e)
		if a, err := mail.ParseAddress(e); err != nil || a.Address != e {
			motif[e] = "adresse_invalide"
			continue
		}
		if c.NePlusContacter {
			motif[e] = "ne_plus_contacter"
			continue
		}
		m := map[string]any{"email": e, "veridian_source": strings.TrimPrefix(app, "app:")}
		for k, v := range map[string]string{"first_name": c.FirstName, "last_name": c.LastName, "phone": c.Phone, "job_title": c.JobTitle} {
			if v = strings.TrimSpace(v); v != "" {
				m[k] = v
			}
		}
		keep = append(keep, m)
	}
	return &call{readOnly: req.DryRun, people: len(keep), run: func(do request, g gesture) (int, []byte, error) {
		return enroll(do, g, ws, req, order, motif, keep)
	}}, nil
}

func enroll(do request, g gesture, ws string, req enrollRequest, order []string, motif map[string]string, keep []map[string]any) (int, []byte, error) {
	post := func(path string, body any, out any) (int, []byte, error) {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		status, resp, err := do(http.MethodPost, path, nil, b)
		if err != nil || status != http.StatusOK {
			return status, resp, err
		}
		if err := json.Unmarshal(resp, out); err != nil {
			return 0, nil, fmt.Errorf("%s : réponse illisible", path)
		}
		return status, nil, nil
	}
	entered := map[string]bool{}
	if len(keep) > 0 {
		var intake struct {
			Details []struct{ Email, Reason string } `json:"details"`
		}
		// Sans liste et sans déclencheur : la fiche seulement. La liste de la séquence est posée par l'activation, après son contrôle.
		if status, resp, err := post("/api/veridian/contacts.intake", map[string]any{"workspace_id": ws, "contacts": keep, "list_ids": []string{}, "dedupe": []string{"email"},
			"existing": "attach", "dry_run": req.DryRun, "allow_triggers": false}, &intake); err != nil || status != http.StatusOK {
			return status, resp, err
		}
		for _, d := range intake.Details {
			if m, ok := intakeReasons[d.Reason]; ok {
				motif[domain.NormalizeEmail(d.Email)] = m
			}
		}
		emails := []string{}
		for _, c := range keep {
			if e := c["email"].(string); motif[e] == "" {
				emails = append(emails, e)
			}
		}
		if len(emails) > 0 {
			batch := ""
			if g.key != "" {
				sum := sha256.Sum256([]byte(g.app + "\x00" + g.key))
				batch = "app-" + hex.EncodeToString(sum[:16])
			}
			var act struct {
				Emails []string          `json:"emails"`
				States map[string]string `json:"states"`
			}
			if status, resp, err := post("/api/veridian/reservoir.activate", map[string]any{"workspace_id": ws, "automation_id": req.SequenceID, "emails": emails,
				"limit": len(emails), "include_contacted": req.InclureDejaContactes, "dry_run": req.DryRun, "batch_id": batch}, &act); err != nil || status != http.StatusOK {
				return status, resp, err
			}
			for _, e := range act.Emails {
				entered[domain.NormalizeEmail(e)] = true
			}
			for _, e := range emails {
				switch state, known := act.States[e]; {
				case entered[e]:
				case !known && req.DryRun:
					entered[e] = true // fiche qui serait créée : rien ne l'écarte
				case !known:
					motif[e] = "non_inscrit"
				default:
					if motif[e] = enrollReasons[state]; motif[e] == "" {
						motif[e] = "non_inscrit"
					}
				}
			}
		}
	}
	res := Enrolled{DryRun: req.DryRun, Received: len(order), Skipped: map[string]int{}, Details: []EnrollDetail{}, Note: enrollNote}
	for _, e := range order {
		if entered[e] {
			res.Inscrits++
		} else {
			res.Skipped[motif[e]]++
		}
		m := motif[e]
		if entered[e] {
			m = ""
		}
		res.Details = append(res.Details, EnrollDetail{Email: e, Motif: m})
	}
	b, err := json.Marshal(res)
	return http.StatusOK, b, err
}

// buildSequences : les séquences de l'espace, et celles où l'on peut inscrire quelqu'un. Corps {} ou vide.
func buildSequences(ws, _ string, raw []byte) (*call, error) {
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := strict(raw, &struct{}{}); err != nil {
			return nil, err
		}
	}
	return &call{readOnly: true, run: func(do request, _ gesture) (int, []byte, error) {
		status, out, err := do(http.MethodGet, "/api/automations.list", map[string][]string{"workspace_id": {ws}, "limit": {"100"}}, nil)
		if err != nil || status != http.StatusOK {
			return status, out, err
		}
		var l struct {
			Automations []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
				ListID string `json:"list_id"`
			} `json:"automations"`
		}
		if json.Unmarshal(out, &l) != nil {
			return 0, nil, errors.New("automations.list : réponse illisible")
		}
		type seq struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Status       string `json:"status"`
			ListID       string `json:"list_id,omitempty"`
			Inscriptible bool   `json:"inscriptible"`
			Raison       string `json:"raison,omitempty"`
		}
		res := []seq{}
		for _, a := range l.Automations {
			x := seq{ID: a.ID, Name: a.Name, Status: a.Status, ListID: a.ListID, Inscriptible: a.Status == "live"}
			switch a.Status {
			case "live":
			case "draft":
				x.Raison = "brouillon"
			case "paused":
				x.Raison = "en_pause"
			default:
				x.Raison = "inactive"
			}
			res = append(res, x)
		}
		b, err := json.Marshal(map[string]any{"sequences": res})
		return http.StatusOK, b, err
	}}, nil
}

// buildRead : l'état de contacts, dans les mots du contrat. Lecture.
func buildRead(ws, _ string, raw []byte) (*call, error) {
	var req struct {
		Emails []string `json:"emails"`
	}
	if err := strict(raw, &req); err != nil {
		return nil, err
	}
	if len(req.Emails) == 0 || len(req.Emails) > maxEnrollPerCall {
		return nil, fmt.Errorf("1 à %d adresses par appel", maxEnrollPerCall)
	}
	return &call{readOnly: true, run: func(do request, _ gesture) (int, []byte, error) {
		b, err := json.Marshal(map[string]any{"workspace_id": ws, "emails": req.Emails})
		if err != nil {
			return 0, nil, err
		}
		status, out, err := do(http.MethodPost, "/api/veridian/contacts.state", nil, b)
		if err != nil || status != http.StatusOK {
			return status, out, err
		}
		var l struct {
			Contacts []struct {
				Email     string            `json:"email"`
				Exists    bool              `json:"exists"`
				Lists     []json.RawMessage `json:"lists"`
				Blocked   string            `json:"blocked"`
				Replied   bool              `json:"replied"`
				Sequences []json.RawMessage `json:"sequences"`
				LastSent  string            `json:"last_sent"`
			} `json:"contacts"`
		}
		if json.Unmarshal(out, &l) != nil {
			return 0, nil, errors.New("contacts.state : réponse illisible")
		}
		orNil := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}
		res := []map[string]any{}
		for _, c := range l.Contacts {
			res = append(res, map[string]any{"email": c.Email, "existe": c.Exists, "listes": c.Lists, "bloque": orNil(c.Blocked), "a_repondu": c.Replied,
				"sequences_en_cours": c.Sequences, "dernier_envoi": orNil(c.LastSent)})
		}
		b, err = json.Marshal(map[string]any{"contacts": res})
		return http.StatusOK, b, err
	}}, nil
}
