package module

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/boxsend"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

func TestCapabilityIsRegistered(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "boites" && c.Wire != nil && c.Start != nil && c.Stop != nil {
			return
		}
	}
	t.Fatal("la capacité boites n'est pas enregistrée")
}

func TestReadSettings(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	s, err := ReadSettings(env(nil), "relais.test")
	if err != nil || s.Enabled || s.Port != 1993 || s.ShowPort != 1993 || s.Host != "relais.test" || s.Proxy {
		t.Fatalf("par défaut rien n'écoute : %+v %v", s, err)
	}
	s, err = ReadSettings(env(map[string]string{"VERIDIAN_IMAP_ENABLED": "true", "VERIDIAN_IMAP_PORT": "2993", "VERIDIAN_IMAP_PUBLIC_PORT": "443", "VERIDIAN_BOX_HOST": "imap.test", "VERIDIAN_IMAP_BEHIND_TLS_PROXY": "true", "VERIDIAN_IMAP_PROXY_PROTOCOL": "true",
		"VERIDIAN_BOX_SMTP_PORT": "5465", "VERIDIAN_BOX_SMTP_HOST": "smtp.test", "VERIDIAN_BOX_SMTP_PUBLIC_PORT": "443"}), "x")
	if err != nil || !s.Enabled || s.Port != 2993 || s.ShowPort != 443 || s.Host != "imap.test" || !s.Proxy || !s.PROXYv2 || s.SMTPPort != 5465 || s.SMTPHost != "smtp.test" || s.SMTPShow != 443 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := ReadSettings(env(map[string]string{"VERIDIAN_IMAP_PORT": "99999"}), "x"); err == nil {
		t.Fatal("port invalide accepté")
	}
}

type accs struct{ a boxaccess.Access }

func (x accs) Verify(_ context.Context, u, p string) (*boxaccess.Access, error) {
	if u == "jc@boite.fr" && p == "bon" {
		return &x.a, nil
	}
	return nil, boxaccess.ErrAuth
}
func (x accs) Alive(context.Context, string) (*boxaccess.Access, error) {
	return nil, boxaccess.ErrAuth
}
func (x accs) Touch(context.Context, string) error            { return nil }
func (x accs) ReserveSend(context.Context, string, int) error { return nil }

type yes struct{}

func (yes) Allow(string, string) bool { return true }

// La porte SMTP publique n'ouvre qu'aux accès de boîte, et une révocation vaut dès le destinataire suivant.
func TestNewSMTP_BoxAccessOnly(t *testing.T) {
	snd := boxsend.New(boxsend.Deps{Accesses: accs{boxaccess.Access{ID: "a1"}}, Limiter: yes{}, Logger: logger.NewLogger()})
	auth := boxAuth(snd)
	if id, err := auth("jc@boite.fr", "bon"); err != nil || id != "box:a1" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := auth("jc@boite.fr", "faux"); err == nil {
		t.Fatal("mauvais mot de passe accepté")
	}
	if _, err := auth("cle@api.ws", "aa.bb.cc"); !errors.Is(err, boxaccess.ErrAuth) {
		t.Fatal("une clé d'API n'entre pas par la porte publique")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	srv, err := newSMTP(Settings{SMTPPort: port, SMTPHost: "smtp.test"}, snd, logger.NewLogger())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Start() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()
	var c *smtp.Client
	for i := 0; i < 50; i++ {
		if c, err = smtp.Dial(fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("jc@boite.fr"); err == nil {
		t.Fatal("envoi sans authentification accepté")
	}
	if err := c.Auth(smtp.PlainAuth("", "jc@boite.fr", "bon", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("jc@boite.fr"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("p@client.fr"); err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("accès révoqué : %v", err)
	}
}

func TestStartStop_WithoutServer(t *testing.T) {
	server, smtpServer = nil, nil
	if err := start(nil, nil); err != nil { //nolint:staticcheck // aucun contexte lu sans serveur
		t.Fatal(err)
	}
	if err := stop(nil); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
}
