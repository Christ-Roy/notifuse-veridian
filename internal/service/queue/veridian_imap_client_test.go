package queue

import (
	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmersionIMAPDialer(t *testing.T) {
	d := newEmersionIMAPDialer()
	require.NotNil(t, d)
	var _ veridianIMAPDialer = d
}

// TestEmersionIMAPDialer_Dial_Guards couvre les garde-fous AVANT toute IO
// réseau (settings nil, password non déchiffré). On ne teste pas le dial réseau
// réel ici (pas de serveur IMAP en CI) — c'est le poller_test, via le fake
// dialer, qui couvre le comportement best-effort sur échec de dial.
func TestEmersionIMAPDialer_Dial_Guards(t *testing.T) {
	d := &emersionIMAPDialer{}
	ctx := context.Background()

	t.Run("nil settings", func(t *testing.T) {
		c, err := d.Dial(ctx, nil)
		require.Error(t, err)
		assert.Nil(t, c)
		assert.Contains(t, err.Error(), "imap settings required")
	})

	t.Run("empty (undecrypted) password", func(t *testing.T) {
		c, err := d.Dial(ctx, &domain.IMAPSettings{Host: "h", Port: 993, Username: "u"})
		require.Error(t, err)
		assert.Nil(t, c)
		assert.Contains(t, err.Error(), "password not decrypted")
	})
}

// TestEmersionIMAPClient_UIDValidity vérifie l'exposition de l'UIDVALIDITY
// capturé au SELECT (clé d'idempotence).
func TestEmersionIMAPClient_UIDValidity(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 12345, folder: "INBOX"}
	assert.Equal(t, uint32(12345), c.UIDValidity())
}

// TestEmersionIMAPClient_ToDomainMessage vérifie la conversion buffer go-imap
// -> DTO domain neutre (le contrat transmis aux lots 2/3).
func TestEmersionIMAPClient_ToDomainMessage(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 99, folder: "Bounces"}

	sentAt := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	buf := &imapclient.FetchMessageBuffer{
		UID: imap.UID(77),
		Envelope: &imap.Envelope{
			MessageID: "<msg-1@example.com>",
			Subject:   "Mail delivery failed",
			Date:      sentAt,
			InReplyTo: []string{"<orig@example.com>"},
			From:      []imap.Address{{Mailbox: "mailer-daemon", Host: "relay.example.com"}},
			To: []imap.Address{
				{Mailbox: "bounce", Host: "agences-veridian.fr"},
				{Mailbox: "", Host: ""}, // group marker / empty → skipped
			},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("Delivery to the following recipient failed permanently")},
		},
	}

	msg := c.toDomainMessage(buf)

	assert.Equal(t, uint32(77), msg.UID)
	assert.Equal(t, uint32(99), msg.UIDValidity)
	assert.Equal(t, "Bounces", msg.Folder)
	assert.Equal(t, "<msg-1@example.com>", msg.MessageID)
	assert.Equal(t, "Mail delivery failed", msg.Subject)
	assert.Equal(t, sentAt, msg.Date)
	assert.Equal(t, "<orig@example.com>", msg.InReplyTo)
	assert.Equal(t, "mailer-daemon@relay.example.com", msg.From)
	assert.Equal(t, []string{"bounce@agences-veridian.fr"}, msg.To)
	assert.Equal(t, []byte("Delivery to the following recipient failed permanently"), msg.RawBody)
}

// TestEmersionIMAPClient_ToDomainMessage_NilEnvelope : un buffer sans envelope
// (fetch partiel / message malformé) ne doit pas paniquer.
func TestEmersionIMAPClient_ToDomainMessage_NilEnvelope(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 1, folder: "INBOX"}
	buf := &imapclient.FetchMessageBuffer{UID: imap.UID(5)}

	var msg *domain.VeridianIMAPMessage
	assert.NotPanics(t, func() { msg = c.toDomainMessage(buf) })
	require.NotNil(t, msg)
	assert.Equal(t, uint32(5), msg.UID)
	assert.Empty(t, msg.From)
	assert.Empty(t, msg.RawBody)
}

// TestEmersionIMAPClient_ToDomainMessage_EmptyBodyTakesFirstNonEmpty vérifie
// qu'on prend la première section de corps NON vide.
func TestEmersionIMAPClient_ToDomainMessage_EmptyBodyTakesFirstNonEmpty(t *testing.T) {
	c := &emersionIMAPClient{uidValidity: 1, folder: "INBOX"}
	buf := &imapclient.FetchMessageBuffer{
		UID: imap.UID(5),
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: nil},
			{Bytes: []byte("real body")},
		},
	}
	msg := c.toDomainMessage(buf)
	assert.Equal(t, []byte("real body"), msg.RawBody)
}
