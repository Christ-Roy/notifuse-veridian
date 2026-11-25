// Package contactimport complète la réponse de POST /api/contacts.import (amont, non touché) par un compte rendu relu dans l'espace : combien de
// fiches, dans quelles listes, et pourquoi les autres manquent. Fiche 94.
//
// Origine (25/11/2026) : un client lisait 806 réussites alors que rien n'était écrit. L'amont répond 200 avec une erreur par contact, perd la vraie
// raison d'un contact illisible (« email is required ») et tait l'échec d'inscription aux listes. Rien n'est retiré de sa réponse, tout s'ajoute.
package contactimport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/tidwall/gjson"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Route : la route amont enveloppée. « POST /chemin » est plus précis que « /chemin » pour ServeMux, il gagne ; l'amont reste joignable par les autres
// méthodes, donc par Handler (membergate, fiche 86).
const Route = "/api/contacts.import"

// Les trois états, et l'échec entier à part : il se lit sans compter.
const (
	StateComplete = "complet"     // tout ce qui a été reçu est dans l'espace et dans les listes demandées
	StatePartial  = "partiel"     // une partie seulement
	StateFailed   = "echec"       // rien n'est entré
	StateUnknown  = "non_verifie" // la relecture a échoué : rien n'est affirmé
)

const (
	maxBody      = 64 << 20
	verifyBudget = 20 * time.Second
)

// Conn donne la base d'un espace.
type Conn interface {
	GetConnection(ctx context.Context, workspaceID string) (*sql.DB, error)
}

func init() {
	platform.Register(platform.Capability{
		Name: "contactimport",
		Wire: func(h platform.Host) error {
			if h.Mux() == nil || h.WorkspaceRepository() == nil {
				return nil
			}
			Mount(h.Mux(), h.WorkspaceRepository(), h.Logger())
			return nil
		},
	})
}

// ListReport : une liste demandée, relue.
type ListReport struct {
	// Subscribed : inscrits actifs. Other : présents avec un autre statut (désinscrit, rebond...), gardé. Absent : pas dans la liste.
	Subscribed int `json:"subscribed"`
	Other      int `json:"other"`
	Absent     int `json:"absent"`
	// Unknown : la liste n'existe pas (ou est supprimée) dans l'espace. L'amont y inscrit quand même.
	Unknown bool `json:"unknown,omitempty"`
}

// Report : le compte rendu ajouté à la réponse.
type Report struct {
	State    string `json:"state"`
	Received int    `json:"received"`
	Created  int    `json:"created"`
	Updated  int    `json:"updated"`
	Failed   int    `json:"failed"`
	// Duplicates : lignes de même adresse, la dernière gagne.
	Duplicates int `json:"duplicates"`
	// Verified : fiches de cet import retrouvées dans l'espace après coup. Nil : relecture non aboutie.
	Verified *int                  `json:"verified"`
	Lists    map[string]ListReport `json:"lists,omitempty"`
}

// Mount pose l'enveloppe.
func Mount(mux *http.ServeMux, ws Conn, log logger.Logger) {
	mux.HandleFunc("POST "+Route, func(w http.ResponseWriter, r *http.Request) {
		probe := r.Clone(r.Context())
		probe.Method = http.MethodGet // le motif sans méthode : l'amont, jamais nous
		upstream, _ := mux.Handler(probe)
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"import trop gros : 64 Mo au plus par appel"}`))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := &recorder{header: http.Header{}, code: http.StatusOK}
		upstream.ServeHTTP(rec, r)
		out := rec.body.Bytes()
		if rec.code == http.StatusOK {
			ctx, cancel := context.WithTimeout(r.Context(), verifyBudget)
			defer cancel()
			out = annotate(ctx, ws, log, body, out)
		}
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(rec.code)
		_, _ = w.Write(out)
	})
}

// recorder garde la réponse de l'amont avant de l'écrire.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

// annotate ajoute le compte rendu. Une réponse inattendue revient telle quelle.
func annotate(ctx context.Context, ws Conn, log logger.Logger, body, out []byte) []byte {
	var req domain.BatchImportContactsRequest
	var resp map[string]json.RawMessage
	var ops []domain.UpsertContactOperation
	if json.Unmarshal(body, &req) != nil || json.Unmarshal(out, &resp) != nil || json.Unmarshal(resp["operations"], &ops) != nil {
		return out
	}
	rows := gjson.ParseBytes(req.Contacts).Array()
	explain(rows, ops)
	rep := Report{Received: len(rows)}
	done := []string{}
	for _, o := range ops {
		switch o.Action {
		case domain.UpsertContactOperationCreate:
			rep.Created++
			done = append(done, o.Email)
		case domain.UpsertContactOperationUpdate:
			rep.Updated++
			done = append(done, o.Email)
		default:
			rep.Failed++
		}
	}
	rep.Duplicates = max(0, rep.Received-len(ops))
	if err := verify(ctx, ws, req.WorkspaceID, done, req.SubscribeToLists, &rep); err != nil {
		rep.Verified, rep.Lists = nil, nil
		log.WithFields(map[string]interface{}{"workspace_id": req.WorkspaceID, "error": err.Error()}).Error("contact import: result not verified")
	}
	rep.State = state(rep, len(done))
	if rep.State != StateComplete {
		log.WithFields(map[string]interface{}{"workspace_id": req.WorkspaceID, "state": rep.State, "received": rep.Received, "failed": rep.Failed}).Warn("contact import: not complete")
	}
	var err error
	if resp["operations"], err = json.Marshal(ops); err != nil {
		return out
	}
	if resp["report"], err = json.Marshal(rep); err != nil {
		return out
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return out
	}
	return b
}

// explain rend à chaque refus son adresse et sa vraie raison : l'amont remplace un contact illisible par un contact vide.
func explain(rows []gjson.Result, ops []domain.UpsertContactOperation) {
	for i := range ops {
		var n int
		if ops[i].Action != domain.UpsertContactOperationError || ops[i].Email != "" {
			continue
		}
		if _, err := fmt.Sscanf(ops[i].Error, "invalid contact at index %d:", &n); err != nil || n < 0 || n >= len(rows) {
			continue
		}
		if _, err := domain.FromJSON(rows[n]); err != nil {
			ops[i].Email = strings.TrimSpace(rows[n].Get("email").String())
			ops[i].Error = fmt.Sprintf("invalid contact at index %d: %v", n, err)
		}
	}
}

func state(r Report, done int) string {
	if r.Verified == nil {
		return StateUnknown
	}
	if *r.Verified == 0 {
		return StateFailed
	}
	if r.Failed > 0 || *r.Verified < done {
		return StatePartial
	}
	for _, l := range r.Lists {
		if l.Absent > 0 || l.Unknown {
			return StatePartial
		}
	}
	return StateComplete
}

// verify relit l'espace : fiches présentes, puis appartenance à chaque liste.
func verify(ctx context.Context, ws Conn, workspaceID string, emails, lists []string, rep *Report) error {
	db, err := ws.GetConnection(ctx, workspaceID)
	if err != nil {
		return err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE email = ANY($1)`, pq.Array(emails)).Scan(&n); err != nil {
		return err
	}
	rep.Verified = &n
	for _, id := range lists {
		var l ListReport
		if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE cl.status = 'active'), count(*) FILTER (WHERE cl.status <> 'active'), count(*) FILTER (WHERE cl.email IS NULL)
			FROM contacts c LEFT JOIN contact_lists cl ON cl.email = c.email AND cl.list_id = $2 AND cl.deleted_at IS NULL WHERE c.email = ANY($1)`,
			pq.Array(emails), id).Scan(&l.Subscribed, &l.Other, &l.Absent); err != nil {
			return err
		}
		if err := db.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM lists WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&l.Unknown); err != nil {
			return err
		}
		if rep.Lists == nil {
			rep.Lists = map[string]ListReport{}
		}
		rep.Lists[id] = l
	}
	return nil
}
