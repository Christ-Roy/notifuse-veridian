package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// cspLog garde les lignes journalisées : message + champs.
type cspLog struct {
	mu     sync.Mutex
	fields map[string]interface{}
	lines  []string
}

func (l *cspLog) rec(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf("%s %s %v", level, msg, l.fields))
}
func (l *cspLog) Debug(m string) { l.rec("debug", m) }
func (l *cspLog) Info(m string)  { l.rec("info", m) }
func (l *cspLog) Warn(m string)  { l.rec("warn", m) }
func (l *cspLog) Error(m string) { l.rec("error", m) }
func (l *cspLog) Fatal(m string) { l.rec("fatal", m) }
func (l *cspLog) WithField(k string, v interface{}) logger.Logger {
	return l.WithFields(map[string]interface{}{k: v})
}
func (l *cspLog) WithFields(f map[string]interface{}) logger.Logger {
	n := &cspLog{fields: map[string]interface{}{}}
	for k, v := range l.fields {
		n.fields[k] = v
	}
	for k, v := range f {
		n.fields[k] = v
	}
	// les lignes remontent au journal racine
	return &cspChild{root: l, fields: n.fields}
}

type cspChild struct {
	root   *cspLog
	fields map[string]interface{}
}

func (c *cspChild) emit(level, m string) {
	c.root.mu.Lock()
	defer c.root.mu.Unlock()
	c.root.lines = append(c.root.lines, fmt.Sprintf("%s %s %v", level, m, c.fields))
}
func (c *cspChild) Debug(m string) { c.emit("debug", m) }
func (c *cspChild) Info(m string)  { c.emit("info", m) }
func (c *cspChild) Warn(m string)  { c.emit("warn", m) }
func (c *cspChild) Error(m string) { c.emit("error", m) }
func (c *cspChild) Fatal(m string) { c.emit("fatal", m) }
func (c *cspChild) WithField(k string, v interface{}) logger.Logger {
	return c.WithFields(map[string]interface{}{k: v})
}
func (c *cspChild) WithFields(f map[string]interface{}) logger.Logger {
	m := map[string]interface{}{}
	for k, v := range c.fields {
		m[k] = v
	}
	for k, v := range f {
		m[k] = v
	}
	return &cspChild{root: c.root, fields: m}
}

func postCSP(t *testing.T, ct, body string) (*cspLog, int) {
	t.Helper()
	l := &cspLog{}
	mux := http.NewServeMux()
	(&VeridianCSPReportHandler{Logger: l}).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return l, rec.Code
}

// Format report-uri (application/csp-report) : une ligne csp_violation, URL réduites à origine + chemin.
func TestVeridianCSPReport_FormatReportURI(t *testing.T) {
	l, code := postCSP(t, "application/csp-report", `{"csp-report":{"document-uri":"https://notifuse.app.veridian.site/console/workspace/w1/contacts?email=alice%40client.fr#x",
	  "effective-directive":"script-src-elem","blocked-uri":"https://evil.example/x.js?token=SECRET","source-file":"https://notifuse.app.veridian.site/console/assets/index.js?v=1","line-number":12,"disposition":"report"}}`)
	if code != http.StatusNoContent || len(l.lines) != 1 {
		t.Fatalf("%d %v", code, l.lines)
	}
	line := l.lines[0]
	for _, want := range []string{"warn csp_violation", "directive:script-src-elem", "blocked:https://evil.example/x.js", "document:https://notifuse.app.veridian.site/console/workspace/w1/contacts", "line:12", "disposition:report"} {
		if !strings.Contains(line, want) {
			t.Fatalf("manque %q dans %s", want, line)
		}
	}
	for _, leak := range []string{"SECRET", "alice", "client.fr", "token", "?v=1"} {
		if strings.Contains(line, leak) {
			t.Fatalf("fuite %q dans le journal : %s", leak, line)
		}
	}
}

// Format report-to (application/reports+json) : un tableau, seules les csp-violation comptent ; mots-clés gardés.
func TestVeridianCSPReport_FormatReportTo(t *testing.T) {
	l, code := postCSP(t, "application/reports+json", `[{"type":"csp-violation","body":{"documentURL":"https://notifuse.app.veridian.site/console/","effectiveDirective":"script-src-attr","blockedURL":"inline","disposition":"enforce","lineNumber":3}},
	  {"type":"deprecation","body":{"id":"x"}},
	  {"type":"csp-violation","body":{"documentURL":"https://notifuse.app.veridian.site/console/","effectiveDirective":"img-src","blockedURL":"data:image/png;base64,AAAA"}}]`)
	if code != http.StatusNoContent || len(l.lines) != 2 {
		t.Fatalf("%d %v", code, l.lines)
	}
	if !strings.Contains(l.lines[0], "blocked:inline") || !strings.Contains(l.lines[0], "directive:script-src-attr") || !strings.Contains(l.lines[1], "blocked:data ") && !strings.Contains(l.lines[1], "blocked:data]") {
		t.Fatalf("%v", l.lines)
	}
	if strings.Contains(strings.Join(l.lines, " "), "AAAA") {
		t.Fatalf("contenu data: journalisé : %v", l.lines)
	}
}

// Corps illisible, vide ou d'un autre type : rien de journalisé, toujours 204 (jamais d'erreur vers le navigateur).
func TestVeridianCSPReport_CorpsIllisible(t *testing.T) {
	for _, b := range []string{"", "pas du json", `{"autre":1}`, `[{"type":"deprecation"}]`} {
		if l, code := postCSP(t, "application/json", b); code != http.StatusNoContent || len(l.lines) != 0 {
			t.Fatalf("%q : %d %v", b, code, l.lines)
		}
	}
}

// La route écoute exactement le chemin annoncé aux navigateurs par la politique (report-uri, Reporting-Endpoints).
func TestVeridianCSPReport_CheminDeLaPolitique(t *testing.T) {
	if middleware.VeridianCSPReportPath != "/api/veridian/csp-report" {
		t.Fatalf("la route et la politique divergent : %q", middleware.VeridianCSPReportPath)
	}
}
