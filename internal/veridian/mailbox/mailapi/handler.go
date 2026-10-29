package mailapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Handler : routes /api/veridian/mail.*
type Handler struct {
	Svc         *Service
	Log         logger.Logger
	RequireAuth func(http.Handler) http.Handler

	// PollEvery : période de lecture du flux d'événements (défaut 2 s ; réduite par les tests).
	PollEvery time.Duration

	streamMu sync.Mutex
	streams  map[string]int
}

const (
	maxBody         = 1 << 20
	maxSendBody     = 4 << 20
	maxStreamsPerWS = 8
	streamLifetime  = 5 * time.Minute
	streamPoll      = 2 * time.Second
	streamHeartbeat = 15 * time.Second
)

// RegisterRoutes branche les routes. GET et POST sont routés explicitement (le routeur Go exige
// la méthode dans le motif : une méthode oubliée tomberait sur la page de la console).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	both := func(path string, fn http.HandlerFunc) {
		hd := h.RequireAuth(fn)
		mux.Handle("GET "+path, hd)
		mux.Handle("POST "+path, hd)
	}
	both("/api/veridian/mail.mailboxes.list", h.mailboxes)
	both("/api/veridian/mail.messages.list", h.list)
	both("/api/veridian/mail.messages.get", h.get)
	both("/api/veridian/mail.messages.raw", h.raw)
	both("/api/veridian/mail.messages.attachment", h.attachment)
	both("/api/veridian/mail.threads.get", h.threadGet)
	both("/api/veridian/mail.threads.list", h.threadList)
	mux.Handle("GET /api/veridian/mail.events", h.RequireAuth(http.HandlerFunc(h.events)))
	mux.Handle("POST /api/veridian/mail.messages.update", h.RequireAuth(http.HandlerFunc(h.update)))
	mux.Handle("POST /api/veridian/mail.messages.send", h.RequireAuth(http.HandlerFunc(h.send)))
	mux.Handle("POST /api/veridian/mail.messages.reply", h.RequireAuth(http.HandlerFunc(h.reply)))
}

// params fusionne le corps JSON (POST) et la query string (la query l'emporte).
type params map[string]any

func (p params) str(k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (p params) list(k string) []string {
	switch v := p[k].(type) {
	case string:
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func (p params) boolPtr(k string) (*bool, error) {
	switch v := p[k].(type) {
	case nil:
		return nil, nil
	case bool:
		return &v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, invalid("%s doit valoir true ou false", k)
		}
		return &b, nil
	}
	return nil, invalid("%s doit valoir true ou false", k)
}

func (p params) intVal(k string) (int, error) {
	s := p.str(k)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, invalid("%s doit être un entier positif", k)
	}
	return n, nil
}

func (p params) timePtr(k string) (*time.Time, error) {
	s := p.str(k)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		t = t.UTC()
		return &t, nil
	}
	return nil, invalid("%s: date invalide (RFC 3339 ou AAAA-MM-JJ)", k)
}

func readParams(r *http.Request, limit int64) (params, error) {
	p := params{}
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			return nil, invalid("corps illisible")
		}
		if int64(len(b)) > limit {
			return nil, invalid("corps trop gros (%d octets au plus)", limit)
		}
		if len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal(b, &p); err != nil {
				return nil, invalid("JSON invalide")
			}
		}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, invalid("paramètres d'URL invalides")
	}
	for k, v := range q {
		if len(v) > 0 {
			p[k] = v[0]
		}
	}
	return p, nil
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	var inv *ErrInvalid
	var authErr *domain.ErrAuthenticationFailed
	var unauthorized *domain.ErrUnauthorized
	var perm *domain.PermissionError
	switch {
	case errors.As(err, &inv):
		writeErr(w, http.StatusBadRequest, "invalid_request", inv.Msg)
	case errors.Is(err, mailbox.ErrInvalidCursor):
		writeErr(w, http.StatusBadRequest, "invalid_request", "curseur invalide")
	case errors.Is(err, mailbox.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "introuvable")
	case errors.As(err, &authErr):
		writeErr(w, http.StatusUnauthorized, "unauthorized", authErr.Error())
	case errors.As(err, &unauthorized), errors.As(err, &perm):
		writeErr(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, ErrNotImplemented):
		writeErr(w, http.StatusNotImplemented, "not_implemented", err.Error())
	case errors.As(err, new(*RefusedError)):
		var re *RefusedError
		errors.As(err, &re)
		writeJSON(w, http.StatusConflict, map[string]any{"error": re.Reason, "code": "refused", "refusal": re})
	default:
		// jamais de contenu de mail dans les journaux : l'erreur ne porte que des identifiants et des causes techniques
		h.Log.WithField("error", err.Error()).Error("Mail API: internal error")
		writeErr(w, http.StatusInternalServerError, "internal_error", "erreur interne")
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code, "message": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) mailboxes(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	out, err := h.Svc.Mailboxes(r.Context(), p.str("workspace_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": nonNilMailboxes(out)})
}

func nonNilMailboxes(m []mailbox.Mailbox) []mailbox.Mailbox {
	if m == nil {
		return []mailbox.Mailbox{}
	}
	return m
}

func parseKinds(p params) []mailbox.Kind {
	var out []mailbox.Kind
	for _, k := range append(p.list("kind"), p.list("kinds")...) {
		out = append(out, mailbox.Kind(k))
	}
	return out
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	lp := ListParams{
		Workspace: p.str("workspace_id"), Mailbox: p.str("mailbox"), Contact: p.str("contact_email"),
		Thread: p.str("thread_id"), Kinds: parseKinds(p), Direction: mailbox.Direction(p.str("direction")),
		Q: p.str("q"), Cursor: p.str("cursor"),
	}
	if lp.Handled, err = p.boolPtr("handled"); err != nil {
		h.fail(w, err)
		return
	}
	bs, err := p.boolPtr("body")
	if err != nil {
		h.fail(w, err)
		return
	}
	lp.BodySearch = bs != nil && *bs
	if lp.Since, err = p.timePtr("since"); err != nil {
		h.fail(w, err)
		return
	}
	if lp.Until, err = p.timePtr("until"); err != nil {
		h.fail(w, err)
		return
	}
	if lp.Limit, err = p.intVal("limit"); err != nil {
		h.fail(w, err)
		return
	}
	items, next, err := h.Svc.List(r.Context(), lp)
	if err != nil {
		h.fail(w, err)
		return
	}
	if items == nil {
		items = []mailbox.Summary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items, "next_cursor": next})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	m, err := h.Svc.Get(r.Context(), p.str("workspace_id"), p.str("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": m})
}

func (h *Handler) raw(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	raw, m, err := h.Svc.Raw(r.Context(), p.str("workspace_id"), p.str("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "message-" + m.ID + ".eml"}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Veridian-Reconstructed", strconv.FormatBool(m.Direction == mailbox.DirectionOut))
	_, _ = w.Write(raw)
}

func (h *Handler) attachment(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	idx, err := p.intVal("index")
	if err != nil {
		h.fail(w, err)
		return
	}
	att, data, err := h.Svc.Attachment(r.Context(), p.str("workspace_id"), p.str("id"), idx)
	if err != nil {
		if err != nil && !errors.Is(err, mailbox.ErrNotFound) && !isAuth(err) {
			err = mailbox.ErrNotFound
		}
		h.fail(w, err)
		return
	}
	// Jamais servie comme page : téléchargement forcé, aucun sniffing, bac à sable total.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("X-Attachment-Content-Type", att.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func isAuth(err error) bool {
	return errors.As(err, new(*domain.ErrAuthenticationFailed)) || errors.As(err, new(*domain.ErrUnauthorized)) || errors.As(err, new(*domain.PermissionError))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	up := UpdateParams{Workspace: p.str("workspace_id"), ID: p.str("id")}
	if up.Handled, err = p.boolPtr("handled"); err != nil {
		h.fail(w, err)
		return
	}
	if raw, ok := p["labels"]; ok {
		l := []string{}
		switch v := raw.(type) {
		case []any:
			for _, x := range v {
				s, isStr := x.(string)
				if !isStr {
					h.fail(w, invalid("labels: liste de textes"))
					return
				}
				l = append(l, s) // les valeurs vides sont refusées par le service, pas ignorées
			}
		case string:
			if v != "" {
				l = strings.Split(v, ",")
			}
		default:
			h.fail(w, invalid("labels: liste de textes"))
			return
		}
		up.Labels = &l
	}
	m, err := h.Svc.Update(r.Context(), up)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": m})
}

func (h *Handler) threadGet(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	res, err := h.Svc.GetThread(r.Context(), p.str("workspace_id"), p.str("thread_id"), p.str("contact_email"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) threadList(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	limit, err := p.intVal("limit")
	if err != nil {
		h.fail(w, err)
		return
	}
	out, err := h.Svc.ListThreads(r.Context(), p.str("workspace_id"), p.str("contact_email"), limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	if out == nil {
		out = []mailbox.Thread{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": out})
}

func (h *Handler) acquireStream(ws string) bool {
	h.streamMu.Lock()
	defer h.streamMu.Unlock()
	if h.streams == nil {
		h.streams = map[string]int{}
	}
	if h.streams[ws] >= maxStreamsPerWS {
		return false
	}
	h.streams[ws]++
	return true
}

func (h *Handler) releaseStream(ws string) {
	h.streamMu.Lock()
	h.streams[ws]--
	h.streamMu.Unlock()
}

// events : flux serveur (SSE) des nouveaux messages. Reprise exacte avec Last-Event-ID ou since_seq :
// chaque événement porte son numéro d'ordre, rien n'est perdu entre deux connexions.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	ws := p.str("workspace_id")
	ctx, err := h.Svc.AuthorizeStream(r.Context(), ws)
	if err != nil {
		h.fail(w, err)
		return
	}
	dir := mailbox.Direction(p.str("direction"))
	switch p.str("direction") {
	case "", "in":
		dir = mailbox.DirectionIn
	case "out":
		dir = mailbox.DirectionOut
	case "any":
		dir = ""
	default:
		h.fail(w, invalid("direction: in, out ou any"))
		return
	}
	kinds := parseKinds(p)
	for _, k := range kinds {
		if !mailbox.ValidKind(k) {
			h.fail(w, invalid("nature inconnue %q", k))
			return
		}
	}
	var last int64
	switch {
	case r.Header.Get("Last-Event-ID") != "":
		last, err = strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	case p.str("since_seq") != "":
		last, err = strconv.ParseInt(p.str("since_seq"), 10, 64)
	default:
		last, err = h.Svc.MaxSeq(ctx, ws)
	}
	if err != nil || last < 0 {
		h.fail(w, invalid("since_seq invalide"))
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		h.fail(w, errors.New("flux non pris en charge"))
		return
	}
	if !h.acquireStream(ws) {
		writeErr(w, http.StatusTooManyRequests, "too_many_streams", fmt.Sprintf("%d flux ouverts au plus par workspace", maxStreamsPerWS))
		return
	}
	defer h.releaseStream(ws)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 3000\n: flux ouvert, reprise après le numéro %d\n\n", last)
	fl.Flush()

	deadline := time.NewTimer(streamLifetime)
	defer deadline.Stop()
	every := h.PollEvery
	if every <= 0 {
		every = streamPoll
	}
	poll := time.NewTicker(every)
	defer poll.Stop()
	beat := time.NewTicker(streamHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			fmt.Fprint(w, "event: reconnect\ndata: {}\n\n")
			fl.Flush()
			return
		case <-beat.C:
			fmt.Fprint(w, ": battement\n\n")
			fl.Flush()
		case <-poll.C:
			evs, err := h.Svc.Events(ctx, ws, last, dir, kinds, 100)
			if err != nil {
				h.Log.WithField("error", err.Error()).Warn("Mail API: events poll failed")
				continue
			}
			for _, e := range evs {
				b, _ := json.Marshal(e)
				name := "message.received"
				if e.Direction == mailbox.DirectionOut {
					name = "message.sent"
				}
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, name, b)
				last = e.Seq
			}
			if len(evs) > 0 {
				fl.Flush()
			}
		}
	}
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request)  { h.sendOrReply(w, r, false) }
func (h *Handler) reply(w http.ResponseWriter, r *http.Request) { h.sendOrReply(w, r, true) }

func (h *Handler) sendOrReply(w http.ResponseWriter, r *http.Request, reply bool) {
	if h.Svc.Sender == nil {
		h.fail(w, ErrNotImplemented)
		return
	}
	p, err := readParams(r, maxSendBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	req := SendRequest{
		Workspace: p.str("workspace_id"), ProfileID: p.str("profile_id"), To: p.str("to"), Cc: p.list("cc"),
		Subject: p.str("subject"), ReplyToMessage: p.str("in_reply_to_message"),
	}
	if v, ok := p["text"].(string); ok {
		req.Text = v
	}
	if v, ok := p["html"].(string); ok {
		req.HTML = v
	}
	if reply {
		if req.ReplyToMessage == "" {
			h.fail(w, invalid("in_reply_to_message requis pour une réponse"))
			return
		}
	} else if req.ReplyToMessage != "" {
		h.fail(w, invalid("in_reply_to_message appartient à messages.reply"))
		return
	}
	res, err := h.Svc.SendMail(r.Context(), req)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

// RefusedError : l'envoi est refusé par une porte de la politique d'envoi (même vocabulaire que les séquences).
type RefusedError struct {
	Reason  string `json:"reason"`
	Gate    string `json:"gate,omitempty"`
	Detail  string `json:"detail,omitempty"`
	RetryAt string `json:"retry_at,omitempty"`
}

func (e *RefusedError) Error() string { return e.Reason }

