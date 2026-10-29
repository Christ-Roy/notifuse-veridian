package mailbox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/netguard"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// UIDInfo : un message d'une boîte, repéré par son UID et sa taille.
type UIDInfo struct {
	UID  uint32
	Size int64
}

// RawMessage : un message brut lu sur IMAP.
type RawMessage struct {
	UID          uint32
	Raw          []byte
	InternalDate time.Time
	// Truncated : le message dépasse MaxRawBytes, seule sa tête a été lue.
	Truncated bool
}

// Session est une connexion ouverte sur un dossier (lecture seule).
type Session interface {
	UIDValidity() uint32
	// ListUIDs rend, par UID croissant, les messages dont l'UID dépasse afterUID ; since (si non
	// nul) borne en plus par date de réception (premier branchement : on ne remonte pas tout).
	ListUIDs(ctx context.Context, afterUID uint32, since time.Time) ([]UIDInfo, error)
	// FetchRaw lit les messages complets (sans changer aucun drapeau).
	FetchRaw(ctx context.Context, uids []uint32) ([]RawMessage, error)
	Close() error
}

// Dialer ouvre une session sur une boîte.
type Dialer interface {
	Dial(ctx context.Context, s *domain.IMAPSettings) (Session, error)
}

const imapDialTimeout = 15 * time.Second

// imapAllowlist : variable de paquet pour que les tests joignent un faux serveur local.
var imapAllowlist = netguard.DefaultAllowlist

type emersionDialer struct{}

// NewDialer rend le dialer de production (go-imap v2, TLS vérifié, garde réseau partagée).
func NewDialer() Dialer { return emersionDialer{} }

func (emersionDialer) Dial(ctx context.Context, s *domain.IMAPSettings) (Session, error) {
	if s == nil || s.Password == "" {
		return nil, fmt.Errorf("réglages IMAP incomplets (mot de passe non déchiffré)")
	}
	dialer := netguard.GuardDialer(&net.Dialer{Timeout: imapDialTimeout}, imapAllowlist())
	opts := &imapclient.Options{
		Dialer:    dialer,
		TLSConfig: &tls.Config{ServerName: s.GetTLSServerName(), MinVersion: tls.VersionTLS12},
	}
	var (
		c   *imapclient.Client
		err error
	)
	if s.UseTLS {
		c, err = imapclient.DialTLS(s.Address(), opts)
	} else {
		c, err = imapclient.DialStartTLS(s.Address(), opts)
	}
	if err != nil {
		return nil, fmt.Errorf("connexion IMAP %s: %w", s.Address(), err)
	}
	if err := c.Login(s.Username, s.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("authentification IMAP: %w", err)
	}
	sel, err := c.Select(s.GetFolder(), &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		_ = c.Logout().Wait()
		_ = c.Close()
		return nil, fmt.Errorf("sélection du dossier %q: %w", s.GetFolder(), err)
	}
	return &emersionSession{c: c, validity: sel.UIDValidity}, nil
}

type emersionSession struct {
	c        *imapclient.Client
	validity uint32
}

func (s *emersionSession) UIDValidity() uint32 { return s.validity }

func (s *emersionSession) Close() error {
	_ = s.c.Logout().Wait()
	return s.c.Close()
}

func (s *emersionSession) ListUIDs(ctx context.Context, afterUID uint32, since time.Time) ([]UIDInfo, error) {
	crit := &imap.SearchCriteria{}
	if afterUID > 0 {
		var set imap.UIDSet
		set.AddRange(imap.UID(afterUID+1), 0)
		crit.UID = []imap.UIDSet{set}
	}
	if !since.IsZero() {
		crit.Since = since
	}
	sd, err := s.c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("recherche des UID: %w", err)
	}
	var uids []imap.UID
	for _, u := range sd.AllUIDs() {
		if uint32(u) > afterUID { // « N:* » rend toujours le dernier message, même si N le dépasse
			uids = append(uids, u)
		}
	}
	if len(uids) == 0 {
		return nil, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	var set imap.UIDSet
	set.AddNum(uids...)
	bufs, err := s.c.Fetch(set, &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("lecture des tailles: %w", err)
	}
	out := make([]UIDInfo, 0, len(bufs))
	for _, b := range bufs {
		out = append(out, UIDInfo{UID: uint32(b.UID), Size: b.RFC822Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (s *emersionSession) FetchRaw(ctx context.Context, uids []uint32) ([]RawMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	bufs, err := s.c.Fetch(set, &imap.FetchOptions{
		UID: true, InternalDate: true, RFC822Size: true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true, Partial: &imap.SectionPartial{Offset: 0, Size: MaxRawBytes}}},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("lecture des messages: %w", err)
	}
	out := make([]RawMessage, 0, len(bufs))
	for _, b := range bufs {
		rm := RawMessage{UID: uint32(b.UID), InternalDate: b.InternalDate, Truncated: b.RFC822Size > MaxRawBytes}
		for _, sec := range b.BodySection {
			if len(sec.Bytes) > 0 {
				rm.Raw = sec.Bytes
				break
			}
		}
		out = append(out, rm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}
