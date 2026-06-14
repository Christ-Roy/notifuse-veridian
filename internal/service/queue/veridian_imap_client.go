package queue

// Veridian fork — adapter IMAP (Lot 1 sprint cold outbound, 2026-06-15).
//
// On définit une interface NARROW (veridianIMAPClient + veridianIMAPDialer) que
// le poller consomme, et une implémentation concrète (emersionIMAPClient /
// emersionIMAPDialer) qui wrappe github.com/emersion/go-imap/v2. Conséquence :
//   - le poller est testable SANS serveur IMAP réel (on mocke l'interface).
//   - on ne mocke JAMAIS la lib elle-même (qui a une API command/promise lourde),
//     on mocke notre propre contrat minimal.
//   - go-imap/v2 reste un détail d'implémentation isolé dans ce seul fichier.
//
// Choix de la lib : même écosystème emersion que go-smtp/go-sasl déjà présents
// dans go.mod. go-imap/v2 est le client IMAP4rev2 maintenu de référence en Go.

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// veridianIMAPDialTimeout borne la phase de connexion+login. Court par design :
// le poller ne doit JAMAIS bloquer le reste de l'app sur une boîte injoignable.
const veridianIMAPDialTimeout = 15 * time.Second

// veridianIMAPClient est le contrat minimal dont le poller a besoin pour une
// session sur une boîte. Une instance == une connexion ouverte sur un dossier
// sélectionné. Close() doit toujours être appelé (defer).
type veridianIMAPClient interface {
	// UIDValidity du dossier sélectionné (clé d'idempotence IMAP).
	UIDValidity() uint32
	// FetchSince retourne les messages reçus depuis `since` (filtre serveur
	// SEARCH SINCE, borné par maxIMAPMessagesPerPoll). On filtre côté serveur
	// pour ne pas rapatrier tout l'historique d'une grosse boîte. Les messages
	// sont retournés triés par UID croissant.
	FetchSince(ctx context.Context, since time.Time, limit int) ([]*domain.VeridianIMAPMessage, error)
	// Close ferme proprement la session (logout best-effort + close socket).
	Close() error
}

// veridianIMAPDialer ouvre une session sur une boîte. Abstrait pour les tests.
type veridianIMAPDialer interface {
	Dial(ctx context.Context, settings *domain.IMAPSettings) (veridianIMAPClient, error)
}

// emersionIMAPDialer est l'implémentation réelle basée sur go-imap/v2.
type emersionIMAPDialer struct{}

// newEmersionIMAPDialer crée le dialer de production.
func newEmersionIMAPDialer() veridianIMAPDialer {
	return &emersionIMAPDialer{}
}

// Dial ouvre la connexion, login, et sélectionne le dossier. Timeout court.
//
// useTLS=true => DialTLS (port 993 typique). useTLS=false => DialStartTLS
// (port 143 + STARTTLS) : on REFUSE le full-cleartext, STARTTLS est toujours
// négocié pour ne jamais transmettre les creds en clair sur le réseau.
func (d *emersionIMAPDialer) Dial(ctx context.Context, settings *domain.IMAPSettings) (veridianIMAPClient, error) {
	if settings == nil {
		return nil, fmt.Errorf("imap settings required")
	}
	if settings.Password == "" {
		return nil, fmt.Errorf("imap password not decrypted (empty)")
	}

	dialer := &net.Dialer{Timeout: veridianIMAPDialTimeout}
	options := &imapclient.Options{
		Dialer:    dialer,
		TLSConfig: &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12},
	}

	var (
		client *imapclient.Client
		err    error
	)
	if settings.UseTLS {
		client, err = imapclient.DialTLS(settings.Address(), options)
	} else {
		client, err = imapclient.DialStartTLS(settings.Address(), options)
	}
	if err != nil {
		return nil, fmt.Errorf("imap dial %s: %w", settings.Address(), err)
	}

	// À partir d'ici, toute erreur doit fermer la connexion pour ne pas fuiter.
	if err := client.Login(settings.Username, settings.Password).Wait(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("imap login %s: %w", settings.Username, err)
	}

	folder := settings.GetFolder()
	selectData, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		_ = client.Logout().Wait()
		_ = client.Close()
		return nil, fmt.Errorf("imap select %q: %w", folder, err)
	}

	return &emersionIMAPClient{
		client:      client,
		folder:      folder,
		uidValidity: selectData.UIDValidity,
	}, nil
}

// emersionIMAPClient wrappe une session go-imap/v2 ouverte sur un dossier.
type emersionIMAPClient struct {
	client      *imapclient.Client
	folder      string
	uidValidity uint32
}

func (c *emersionIMAPClient) UIDValidity() uint32 {
	return c.uidValidity
}

func (c *emersionIMAPClient) Close() error {
	// Logout best-effort puis fermeture socket. On ne remonte que l'erreur de
	// Close (la plus parlante côté ressource).
	_ = c.client.Logout().Wait()
	return c.client.Close()
}

// FetchSince fait un SEARCH SINCE côté serveur puis FETCH (envelope + body) des
// UID trouvés, bornés à `limit` (les plus récents). Retourne des DTO neutres.
func (c *emersionIMAPClient) FetchSince(ctx context.Context, since time.Time, limit int) ([]*domain.VeridianIMAPMessage, error) {
	// 1) SEARCH SINCE : ne récupère que les UID des messages récents (le filtre
	//    "since" est appliqué par le serveur sur la date INTERNALDATE).
	searchData, err := c.client.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap uid search since %s: %w", since.Format(time.RFC3339), err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	// Trier croissant et borner aux `limit` plus récents (queue de la liste).
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if limit > 0 && len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}

	// 2) FETCH des UID retenus.
	var uidSet imap.UIDSet
	uidSet.AddNum(uids...)

	fetchOptions := &imap.FetchOptions{
		UID:      true,
		Envelope: true,
		BodySection: []*imap.FetchItemBodySection{
			{}, // section vide = message complet (RFC822)
		},
	}

	buffers, err := c.client.Fetch(uidSet, fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap fetch: %w", err)
	}

	messages := make([]*domain.VeridianIMAPMessage, 0, len(buffers))
	for _, buf := range buffers {
		messages = append(messages, c.toDomainMessage(buf))
	}
	// Garantir l'ordre UID croissant en sortie (Collect ne le garantit pas).
	sort.Slice(messages, func(i, j int) bool { return messages[i].UID < messages[j].UID })
	return messages, nil
}

// toDomainMessage convertit un buffer go-imap en DTO domain neutre.
func (c *emersionIMAPClient) toDomainMessage(buf *imapclient.FetchMessageBuffer) *domain.VeridianIMAPMessage {
	msg := &domain.VeridianIMAPMessage{
		UID:         uint32(buf.UID),
		UIDValidity: c.uidValidity,
		Folder:      c.folder,
	}

	if env := buf.Envelope; env != nil {
		msg.MessageID = env.MessageID
		msg.Subject = env.Subject
		msg.Date = env.Date
		if len(env.InReplyTo) > 0 {
			msg.InReplyTo = env.InReplyTo[0]
		}
		if len(env.From) > 0 {
			msg.From = env.From[0].Addr()
		}
		for i := range env.To {
			if addr := env.To[i].Addr(); addr != "" {
				msg.To = append(msg.To, addr)
			}
		}
	}

	// Corps brut : première section de body fetchée.
	for _, section := range buf.BodySection {
		if len(section.Bytes) > 0 {
			msg.RawBody = section.Bytes
			break
		}
	}

	return msg
}
