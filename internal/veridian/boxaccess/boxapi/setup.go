package boxapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
)

// Réglage d'une application mail par la personne elle-même (fiche 92) : désactiver, profil Apple, test de ce qu'elle a saisi.

// Limite du test de connexion : il vérifie un mot de passe, il ne sert pas à en essayer.
const (
	LimitTest  = "box_access_test"
	TestTries  = 30
	TestWindow = 10 * time.Minute
)

// Limiter : limiteur de débit de l'application.
type Limiter interface {
	Allow(namespace, key string) bool
}

func (h *Handler) registerSetup(mux *http.ServeMux) {
	mux.Handle("POST /api/veridian/mail.access.setDisabled", h.RequireAuth(http.HandlerFunc(h.setDisabled)))
	mux.Handle("POST /api/veridian/mail.access.profileLink", h.RequireAuth(http.HandlerFunc(h.profileLink)))
	mux.Handle("POST /api/veridian/mail.access.test", h.RequireAuth(http.HandlerFunc(h.test)))
	// Publique : ouverte par le téléphone de la personne. Le jeton est signé, court, et ne donne que des réglages.
	mux.Handle("GET /api/veridian/mail.access.profile", http.HandlerFunc(h.profile))
}

func (h *Handler) setDisabled(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
		Disabled    bool   `json:"disabled"`
	}
	if !decode(w, r, &q) {
		return
	}
	ctx, actor, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeWrite)
	if !ok {
		return
	}
	a, err := h.Store.SetDisabled(ctx, q.WorkspaceID, q.ID, q.Disabled)
	if err != nil {
		h.fail(w, err)
		return
	}
	if q.Disabled {
		boxaccess.CloseSessions(a.ID)
	}
	h.Log.WithFields(map[string]interface{}{"workspace_id": a.WorkspaceID, "access_id": a.ID, "by": actor.Label(), "disabled": q.Disabled}).Info("Box access switched")
	writeJSON(w, http.StatusOK, map[string]any{"access": a})
}

func (h *Handler) profileLink(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
	}
	if !decode(w, r, &q) {
		return
	}
	ctx, _, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeRead)
	if !ok {
		return
	}
	a, err := h.Store.Get(ctx, q.WorkspaceID, q.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	if a.RevokedAt != nil || len(h.LinkKey) == 0 {
		writeErr(w, http.StatusConflict, "unavailable", "cet accès n'a plus de profil à installer")
		return
	}
	exp := time.Now().Add(linkLife).UTC()
	u := strings.TrimRight(h.BaseURL, "/") + "/api/veridian/mail.access.profile?t=" + url.QueryEscape(SignLink(h.LinkKey, a.WorkspaceID, a.ID, exp))
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "expires_at": exp.Format(time.RFC3339)})
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	ws, id, err := ReadLink(h.LinkKey, r.URL.Query().Get("t"), time.Now())
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "lien expiré : redemandez-le dans les réglages de votre espace")
		return
	}
	a, err := h.Store.Get(r.Context(), ws, id)
	if err != nil || a.RevokedAt != nil {
		writeErr(w, http.StatusNotFound, "not_found", "cet accès n'existe plus")
		return
	}
	org := ws
	if x, err := h.WS.GetByID(r.Context(), ws); err == nil && x != nil && x.Name != "" {
		org = x.Name
	}
	b, err := MobileConfig(a, h.Connection, org)
	if err != nil {
		writeErr(w, http.StatusConflict, "unavailable", "profil non disponible sur cette instance")
		return
	}
	w.Header().Set("Content-Type", profileMIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": strings.ReplaceAll(a.Address, "@", "-") + ".mobileconfig"}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	_, _ = w.Write(b)
}

// Check : un point vérifié. Expected : la valeur à mettre, quand ok est faux et qu'elle se dit.
type Check struct {
	Code     string `json:"code"`
	OK       bool   `json:"ok"`
	Expected string `json:"expected,omitempty"`
}

type testRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Address     string `json:"address"`
	Via         string `json:"via"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

// securityOf : mode attendu d'une porte, dans les mots de l'écran.
func securityOf(e *Endpoint) string {
	if strings.EqualFold(e.Security, "starttls") {
		return "starttls"
	}
	return "ssl"
}

// wayFor : le réglage servi sur ce port, sinon le réglage conseillé.
func wayFor(e *Endpoint, port int) Way {
	for _, w := range e.Also {
		if w.Port == port && port != e.Port {
			return w
		}
	}
	return Way{Port: e.Port, Security: securityOf(e)}
}

// test compare ce que la personne a saisi dans son application à ce qui marche, puis vérifie la porte et le mot de passe. Seule NOTRE porte est
// jointe (celle de Connection), jamais l'adresse saisie.
func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	var q testRequest
	if !decode(w, r, &q) {
		return
	}
	ctx, actor, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeRead)
	if !ok {
		return
	}
	var e *Endpoint
	switch q.Via {
	case "imap":
		e = h.Connection.IMAP
	case "smtp":
		e = h.Connection.SMTP
	}
	if e == nil || (q.Security != "ssl" && q.Security != "starttls" && q.Security != "none") || len(q.Password) > 200 || len(q.Host) > 255 {
		writeErr(w, http.StatusBadRequest, "invalid", "via imap ou smtp (servi sur cette instance), security ssl, starttls ou none")
		return
	}
	if h.Limiter == nil || !h.Limiter.Allow(LimitTest, q.WorkspaceID+"|"+actor.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "trop d'essais, réessayez dans quelques minutes")
		return
	}
	ws, err := h.WS.GetByID(ctx, q.WorkspaceID)
	if err != nil {
		h.fail(w, err)
		return
	}
	addr := boxaccess.NormalizeAddress(q.Address)
	if !owns(ws, addr) {
		writeErr(w, http.StatusBadRequest, "invalid", "cette adresse n'est pas une adresse d'envoi de l'espace")
		return
	}
	var out []Check
	add := func(code string, ok bool, expected string) {
		if ok {
			expected = ""
		}
		out = append(out, Check{Code: code, OK: ok, Expected: expected})
	}
	add("host", strings.EqualFold(strings.TrimSpace(q.Host), e.Host), e.Host)
	want := wayFor(e, q.Port)
	add("port", q.Port == want.Port, strconv.Itoa(want.Port))
	add("security", q.Security == want.Security, want.Security)
	add("username", boxaccess.NormalizeAddress(q.Username) == addr, addr)
	if derr := h.door(ctx, q.Via, e); derr != nil {
		h.Log.WithFields(map[string]interface{}{"via": q.Via, "error": derr.Error()}).Warn("Box access test: door not reached")
		add("door", false, "")
	} else {
		add("door", true, "")
	}
	if strings.TrimSpace(q.Password) == "" {
		add("password_empty", false, "")
	} else if err := h.checkPassword(ctx, ws, addr, q, add); err != nil {
		h.fail(w, err)
		return
	}
	all := true
	for _, c := range out {
		all = all && c.OK
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": all, "checks": out})
}

func (h *Handler) checkPassword(ctx context.Context, ws *domain.Workspace, addr string, q testRequest, add func(string, bool, string)) error {
	a, err := h.Store.Match(ctx, q.WorkspaceID, addr, q.Password)
	if err != nil {
		return err
	}
	add("credentials", a != nil, "")
	if a == nil {
		return nil
	}
	state := ""
	switch {
	case a.RevokedAt != nil:
		state = "revoked"
	case a.DisabledAt != nil:
		state = "disabled"
	default:
		if state, err = h.Store.Reason(ctx, a); err != nil {
			return err
		}
	}
	add("access_open", state == "", state)
	if q.Via != "smtp" {
		return nil
	}
	add("can_send", a.CanSend, "")
	add("not_suspended", !a.ReadOnly, "")
	p := boxaccess.ProfileOf(ws, a.ProfileID, a.Address)
	add("profile_active", p != nil && !p.EmailProvider.VeridianPaused, "")
	return nil
}

func owns(ws *domain.Workspace, addr string) bool {
	if addr == "" {
		return false
	}
	for i := range ws.Integrations {
		if boxaccess.ProfileOf(ws, ws.Integrations[i].ID, addr) != nil {
			return true
		}
	}
	return false
}

const doorTimeout = 6 * time.Second

// door joint NOTRE porte comme le ferait une application : connexion, TLS, bannière.
func (h *Handler) door(ctx context.Context, via string, e *Endpoint) error {
	if h.Door != nil {
		return h.Door(ctx, via, e)
	}
	return dialDoor(ctx, via, e)
}

func dialDoor(ctx context.Context, via string, e *Endpoint) error {
	ctx, cancel := context.WithTimeout(ctx, doorTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(e.Host, strconv.Itoa(e.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	if securityOf(e) == "ssl" {
		t := tls.Client(conn, &tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12})
		if err := t.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = t
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	want := "* OK"
	if via == "smtp" {
		want = "220"
	}
	if !strings.HasPrefix(line, want) {
		return fmt.Errorf("bannière inattendue")
	}
	return nil
}
