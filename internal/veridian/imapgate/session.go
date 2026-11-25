package imapgate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
)

const delim = '/'

type folderDef struct {
	name  string
	box   mailbox.BoxFolder
	attrs []imap.MailboxAttr
}

var folders = []folderDef{
	{"INBOX", mailbox.BoxInbox, nil},
	{"Rejets", mailbox.BoxRejects, nil},
	{"Sent", mailbox.BoxSent, []imap.MailboxAttr{imap.MailboxAttrSent}},
	{"Trash", mailbox.BoxTrash, []imap.MailboxAttr{imap.MailboxAttrTrash}},
}

func folderOf(name string) (folderDef, bool) {
	for _, f := range folders {
		if f.name == name || (f.box == mailbox.BoxInbox && strings.EqualFold(name, "INBOX")) {
			return f, true
		}
	}
	return folderDef{}, false
}

func no(code imap.ResponseCode, text string) error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: text}
}

var (
	errNoFolder  = no(imap.ResponseCodeNonExistent, "dossier inconnu")
	errReadOnly  = no(imap.ResponseCodeCannot, "operation non disponible sur cette boite")
	errUnavail   = no(imap.ResponseCodeUnavailable, "service momentanement indisponible")
	errLimited   = no(imap.ResponseCodeUnavailable, "trop d'essais, reessayez plus tard")
	errTooMany   = no(imap.ResponseCodeLimit, "trop de connexions sur cet acces")
	errNotOpened = no(imap.ResponseCodeClientBug, "aucun dossier ouvert")
)

type entry struct {
	mailbox.BoxEntry
	uid imap.UID
}

func (e *entry) has(f imap.Flag) bool {
	for _, x := range e.Flags {
		if strings.EqualFold(x, string(f)) {
			return true
		}
	}
	return false
}

func (e *entry) imapFlags() []imap.Flag {
	out := make([]imap.Flag, 0, len(e.Flags))
	for _, f := range e.Flags {
		out = append(out, imap.Flag(f))
	}
	return out
}

type session struct {
	srv *Server
	ip  string

	acc     *boxaccess.Access
	checked time.Time

	sel      *folderDef
	readOnly bool
	list     []*entry
	loaded   time.Time
}

var (
	_ imapserver.SessionMove      = (*session)(nil)
	_ imapserver.SessionNamespace = (*session)(nil)
)

func (s *Server) newSession(ip string) *session { return &session{srv: s, ip: ip} }

func opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

// fail journalise une panne (jamais de contenu) et rend une erreur neutre au client.
func (s *session) fail(op string, err error) error {
	f := map[string]interface{}{"op": op, "error": err.Error()}
	if s.acc != nil {
		f["workspace_id"], f["access_id"] = s.acc.WorkspaceID, s.acc.ID
	}
	s.srv.cfg.Logger.WithFields(f).Warn("IMAP gate: operation failed")
	return errUnavail
}

func (s *session) Close() error {
	if s.acc != nil {
		s.srv.release(s.acc.ID)
		s.acc = nil
	}
	return nil
}

func (s *session) Login(username, password string) error {
	addr := boxaccess.NormalizeAddress(username)
	lim, from := s.srv.cfg.Limiter, boxaccess.LimitKey(s.ip)
	key := addr + "|" + from
	if !lim.Allow(LimitIP, from) || !lim.Allow(LimitAddress, key) {
		s.srv.cfg.Logger.WithFields(map[string]interface{}{"remote_ip": s.ip, "box": boxaccess.Fingerprint(addr)}).Warn("IMAP gate: too many login attempts")
		return errLimited
	}
	ctx, cancel := opCtx()
	defer cancel()
	acc, err := s.srv.cfg.Auth.Verify(ctx, addr, password)
	if errors.Is(err, boxaccess.ErrAuth) {
		s.srv.cfg.Logger.WithFields(map[string]interface{}{"remote_ip": s.ip, "box": boxaccess.Fingerprint(addr)}).Warn("IMAP gate: login refused")
		return imapserver.ErrAuthFailed
	}
	if err != nil {
		return s.fail("login", err)
	}
	if !s.srv.acquire(acc.ID) {
		return errTooMany
	}
	lim.Reset(LimitIP, from)
	lim.Reset(LimitAddress, key)
	s.acc, s.checked = acc, time.Now()
	if err := s.srv.cfg.Auth.Touch(ctx, acc.ID); err != nil {
		s.srv.cfg.Logger.WithField("error", err.Error()).Debug("IMAP gate: touch failed")
	}
	s.srv.cfg.Logger.WithFields(map[string]interface{}{"workspace_id": acc.WorkspaceID, "access_id": acc.ID, "remote_ip": s.ip}).Info("IMAP gate: login")
	return nil
}

// alive coupe une session dont l'accès a été révoqué (révoqué, ou son auteur retiré de l'espace ; relu au plus toutes les 5 s).
func (s *session) alive() error {
	if s.acc == nil || time.Since(s.checked) < aliveEvery {
		return nil
	}
	ctx, cancel := opCtx()
	defer cancel()
	if _, err := s.srv.cfg.Auth.Alive(ctx, s.acc.ID); err != nil {
		if errors.Is(err, boxaccess.ErrAuth) {
			return &imap.Error{Type: imap.StatusResponseTypeBye, Code: imap.ResponseCodeUnavailable, Text: "acces revoque"}
		}
		return s.fail("alive", err)
	}
	s.checked = time.Now()
	return nil
}

func (s *session) uidValidity(f folderDef) uint32 {
	v := crc32.ChecksumIEEE([]byte(s.acc.WorkspaceID + "|" + s.acc.ProfileID + "|" + string(f.box)))
	if v == 0 {
		v = 1
	}
	return v
}

func (s *session) index(f folderDef) ([]*entry, error) {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := s.srv.cfg.Boxes.BoxIndex(ctx, s.acc.WorkspaceID, s.acc.ProfileID, f.box)
	if err != nil {
		return nil, s.fail("index", err)
	}
	out := make([]*entry, 0, len(rows))
	for i := range rows {
		if rows[i].Seq <= 0 || rows[i].Seq >= 1<<32-1 {
			continue
		}
		out = append(out, &entry{BoxEntry: rows[i], uid: imap.UID(rows[i].Seq)})
	}
	return out, nil
}

func uidNext(l []*entry) imap.UID {
	if len(l) == 0 {
		return 1
	}
	return l[len(l)-1].uid + 1
}

func count(l []*entry, f imap.Flag) uint32 {
	var n uint32
	for _, e := range l {
		if e.has(f) {
			n++
		}
	}
	return n
}

func (s *session) Select(name string, o *imap.SelectOptions) (*imap.SelectData, error) {
	f, ok := folderOf(name)
	if !ok {
		return nil, errNoFolder
	}
	if err := s.alive(); err != nil {
		return nil, err
	}
	l, err := s.index(f)
	if err != nil {
		return nil, err
	}
	s.sel, s.list, s.loaded, s.readOnly = &f, l, time.Now(), o != nil && o.ReadOnly
	flags := []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft}
	d := &imap.SelectData{Flags: flags, PermanentFlags: append(append([]imap.Flag{}, flags...), imap.FlagWildcard),
		NumMessages: uint32(len(l)), UIDNext: uidNext(l), UIDValidity: s.uidValidity(f)}
	for i, e := range l {
		if !e.has(imap.FlagSeen) {
			d.FirstUnseenSeqNum = uint32(i + 1)
			break
		}
	}
	return d, nil
}

func (s *session) Unselect() error {
	s.sel, s.list = nil, nil
	return nil
}

func (s *session) Status(name string, o *imap.StatusOptions) (*imap.StatusData, error) {
	f, ok := folderOf(name)
	if !ok {
		return nil, errNoFolder
	}
	l, err := s.index(f)
	if err != nil {
		return nil, err
	}
	d := &imap.StatusData{Mailbox: f.name}
	n := uint32(len(l))
	if o.NumMessages {
		d.NumMessages = &n
	}
	if o.UIDNext {
		d.UIDNext = uidNext(l)
	}
	if o.UIDValidity {
		d.UIDValidity = s.uidValidity(f)
	}
	if o.NumUnseen {
		u := n - count(l, imap.FlagSeen)
		d.NumUnseen = &u
	}
	if o.NumDeleted {
		x := count(l, imap.FlagDeleted)
		d.NumDeleted = &x
	}
	if o.NumRecent {
		z := uint32(0)
		d.NumRecent = &z
	}
	if o.Size {
		var t int64
		for _, e := range l {
			t += e.Size
		}
		d.Size = &t
	}
	return d, nil
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, o *imap.ListOptions) error {
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: delim})
	}
	for _, f := range folders {
		match := false
		for _, p := range patterns {
			match = match || imapserver.MatchList(f.name, delim, ref, p)
		}
		if !match {
			continue
		}
		d := &imap.ListData{Mailbox: f.name, Delim: delim, Attrs: append([]imap.MailboxAttr{imap.MailboxAttrHasNoChildren}, f.attrs...)}
		if o != nil && (o.SelectSubscribed || o.ReturnSubscribed) {
			d.Attrs = append(d.Attrs, imap.MailboxAttrSubscribed)
		}
		if o != nil && o.ReturnStatus != nil {
			st, err := s.Status(f.name, o.ReturnStatus)
			if err != nil {
				return err
			}
			d.Status = st
		}
		if err := w.WriteList(d); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{Personal: []imap.NamespaceDescriptor{{Delim: delim}}}, nil
}

// Dossiers fixes : créer, renommer ou supprimer n'a pas de sens ; s'abonner est toujours vrai.
func (s *session) Create(string, *imap.CreateOptions) error         { return errReadOnly }
func (s *session) Delete(string) error                              { return errReadOnly }
func (s *session) Rename(string, string, *imap.RenameOptions) error { return errReadOnly }
func (s *session) Subscribe(name string) error {
	if _, ok := folderOf(name); !ok {
		return errNoFolder
	}
	return nil
}
func (s *session) Unsubscribe(name string) error { return s.Subscribe(name) }

// Append : un client mail dépose dans « Sent » la copie de ce qu'il vient d'envoyer. Le SMTP de Notifuse l'a déjà rangée :
// la copie est acceptée sans doublon. Tout autre dépôt est refusé, jamais avalé en silence.
func (s *session) Append(name string, r imap.LiteralReader, _ *imap.AppendOptions) (*imap.AppendData, error) {
	f, ok := folderOf(name)
	if !ok {
		_, _ = io.Copy(io.Discard, r)
		return nil, no(imap.ResponseCodeTryCreate, "dossier inconnu")
	}
	raw, err := io.ReadAll(io.LimitReader(r, mailbox.MaxRawBytes))
	_, _ = io.Copy(io.Discard, r)
	if err != nil || f.box != mailbox.BoxSent {
		return nil, errReadOnly
	}
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, errReadOnly
	}
	id := strings.Trim(strings.TrimSpace(h.Get("Message-Id")), "<>")
	if id == "" {
		return nil, errReadOnly
	}
	ids := []string{"<" + id + ">"}
	local := id
	if at := strings.IndexByte(id, '@'); at > 0 {
		local = id[:at]
	}
	if rfc := domain.VeridianRFCMessageID(local, s.acc.Address); rfc != "" {
		ids = append(ids, "<"+rfc+">")
	}
	ctx, cancel := opCtx()
	defer cancel()
	known, err := s.srv.cfg.Boxes.BoxSentKnown(ctx, s.acc.WorkspaceID, s.acc.ProfileID, ids)
	if err != nil {
		return nil, s.fail("append", err)
	}
	if !known {
		return nil, no(imap.ResponseCodeCannot, "message non envoye par le SMTP de Notifuse : copie refusee")
	}
	return nil, nil
}

// ---------- mises à jour ----------

// refresh relit le dossier et annonce ce qui a changé. allowExpunge faux : les numéros ne peuvent pas bouger, un message
// disparu reste listé jusqu'au prochain passage qui le permet.
func (s *session) refresh(w *imapserver.UpdateWriter, allowExpunge, force bool) error {
	if s.sel == nil || (!force && time.Since(s.loaded) < refreshEvery) {
		return nil
	}
	fresh, err := s.index(*s.sel)
	if err != nil {
		return err
	}
	s.loaded = time.Now()
	byUID := make(map[imap.UID]*entry, len(fresh))
	for _, e := range fresh {
		byUID[e.uid] = e
	}
	if allowExpunge {
		kept := make([]*entry, 0, len(s.list))
		for i := len(s.list) - 1; i >= 0; i-- {
			if _, ok := byUID[s.list[i].uid]; !ok {
				if err := w.WriteExpunge(uint32(i + 1)); err != nil {
					return err
				}
				continue
			}
			kept = append(kept, s.list[i])
		}
		for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
			kept[i], kept[j] = kept[j], kept[i]
		}
		s.list = kept
	}
	for i, e := range s.list {
		if f, ok := byUID[e.uid]; ok && !sameFlags(e.Flags, f.Flags) {
			e.Flags = f.Flags
			if err := w.WriteMessageFlags(uint32(i+1), e.uid, e.imapFlags()); err != nil {
				return err
			}
		}
	}
	// Seuls les numéros plus grands sont annoncés (RFC 3501 : UID croissants). Un message remis dans le dossier avec son ancien
	// numéro apparaît à la prochaine ouverture.
	last := uidNext(s.list)
	added := false
	for _, e := range fresh {
		if e.uid >= last {
			s.list = append(s.list, e)
			added = true
		}
	}
	if added {
		return w.WriteNumMessages(uint32(len(s.list)))
	}
	return nil
}

func sameFlags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if err := s.alive(); err != nil {
		return err
	}
	return s.refresh(w, allowExpunge, false)
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	t := time.NewTicker(idleEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-t.C:
			if err := s.alive(); err != nil {
				return err
			}
			if err := s.refresh(w, true, true); err != nil {
				return err
			}
		}
	}
}
