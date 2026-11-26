package service

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/domauth"
)

type dkimKeys struct {
	ws, domain string
	key        *rsa.PrivateKey
}

func (k dkimKeys) SigningKey(_ context.Context, ws, d string) (string, *rsa.PrivateKey, error) {
	if ws == k.ws && d == k.domain {
		return "vd202611abcdef", k.key, nil
	}
	return "", nil, nil
}

// rawSink : serveur SMTP minimal qui garde le message OCTET POUR OCTET (le mock commun retire les blancs de tête des lignes, ce qui
// détruit les en-têtes repliés comme DKIM-Signature).
type rawSink struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs [][]byte
}

func newRawSink(t *testing.T) *rawSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &rawSink{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *rawSink) serve(c net.Conn) {
	defer c.Close()
	tp := textproto.NewConn(c)
	_ = tp.PrintfLine("220 sink")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(line); {
		case strings.HasPrefix(cmd, "EHLO"):
			_ = tp.PrintfLine("250 sink")
		case strings.HasPrefix(cmd, "DATA"):
			_ = tp.PrintfLine("354 go")
			raw, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			// DotReader rend des LF : le message transmis était en CRLF.
			raw = bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
			s.mu.Lock()
			s.msgs = append(s.msgs, raw)
			s.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case strings.HasPrefix(cmd, "QUIT"):
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 ok")
		}
	}
}

func (s *rawSink) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *rawSink) all() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte{}, s.msgs...)
}

func dkimRequest(port int, ws, from string) domain.SendEmailProviderRequest {
	return domain.SendEmailProviderRequest{
		WorkspaceID: ws, IntegrationID: "integration-123", MessageID: "11111111-2222-3333-4444-555555555555",
		FromAddress: from, FromName: "Client", To: "prospect@exemple.test", Subject: "Bonjour", Content: "<p>Bonjour, voici l'essai.</p>",
		Provider: &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "127.0.0.1", Port: port, UseTLS: false}},
	}
}

// VER-166 : le message réellement transmis au relais est signé d=<domaine vérifié de l'espace> et la signature se vérifie
// (go-msgauth, DNS simulé ne connaissant que notre clé publique). Sans domaine vérifié, rien ne change.
func TestSMTPService_SendEmail_VeridianSignsVerifiedDomain(t *testing.T) {
	priv, pub, err := domauth.GenerateKey()
	require.NoError(t, err)
	key, err := domauth.ParsePrivateKey(priv)
	require.NoError(t, err)
	signer := &domauth.Signer{Keys: dkimKeys{ws: "wsa", domain: "client.fr", key: key}}
	SetVeridianOutgoingSigner(signer.Sign)
	t.Cleanup(func() { SetVeridianOutgoingSigner(nil) })

	server := newRawSink(t)
	svc := NewSMTPService(&noopLogger{})

	require.NoError(t, svc.SendEmail(context.Background(), dkimRequest(server.port(), "wsa", "bonjour@client.fr")))
	require.NoError(t, svc.SendEmail(context.Background(), dkimRequest(server.port(), "wsb", "bonjour@client.fr")))
	require.NoError(t, svc.SendEmail(context.Background(), dkimRequest(server.port(), "wsa", "bonjour@autre.fr")))
	msgs := server.all()
	require.Len(t, msgs, 3)

	lookup := func(n string) ([]string, error) {
		if n == "vd202611abcdef._domainkey.client.fr" {
			return []string{"v=DKIM1; k=rsa; p=" + pub}, nil
		}
		return nil, errors.New("inconnu")
	}
	v, err := dkim.VerifyWithOptions(bytes.NewReader(msgs[0]), &dkim.VerifyOptions{LookupTXT: lookup})
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.NoError(t, v[0].Err)
	assert.Equal(t, "client.fr", v[0].Domain)

	assert.NotContains(t, string(msgs[1]), "DKIM-Signature", "un autre espace ne signe jamais avec la clé de A")
	assert.NotContains(t, string(msgs[2]), "DKIM-Signature", "From hors domaine vérifié : message intact")
}

func TestVeridianSignOutgoing_SansSignataireIntact(t *testing.T) {
	SetVeridianOutgoingSigner(nil)
	buf := bytes.NewBufferString("From: a@b.fr\r\n\r\nx")
	veridianSignOutgoing(context.Background(), domain.SendEmailProviderRequest{WorkspaceID: "w", FromAddress: "a@b.fr"}, buf)
	assert.Equal(t, "From: a@b.fr\r\n\r\nx", buf.String())

	SetVeridianOutgoingSigner(func(_ context.Context, ws, from string, raw []byte) ([]byte, bool) {
		return append([]byte("DKIM-Signature: "+ws+" "+from+"\r\n"), raw...), true
	})
	t.Cleanup(func() { SetVeridianOutgoingSigner(nil) })
	veridianSignOutgoing(context.Background(), domain.SendEmailProviderRequest{WorkspaceID: "w", FromAddress: "a@b.fr"}, buf)
	assert.Equal(t, "DKIM-Signature: w a@b.fr\r\nFrom: a@b.fr\r\n\r\nx", buf.String())
}
