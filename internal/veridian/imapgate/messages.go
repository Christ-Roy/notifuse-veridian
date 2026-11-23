package imapgate

import (
	"bufio"
	"bytes"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
)

const (
	fetchBatch = 25
	// searchBodyMax : une recherche dans le texte porte sur les N messages les plus récents du dossier.
	searchBodyMax = 1500
	maxKeywords   = 20
	maxKeywordLen = 64
)

// pick rend les rangs (base 0) des messages désignés.
func (s *session) pick(set imap.NumSet) []int {
	var out []int
	if len(s.list) == 0 {
		return out
	}
	switch v := set.(type) {
	case imap.SeqSet:
		max := uint32(len(s.list))
		for i := range s.list {
			if inSeq(v, uint32(i+1), max) {
				out = append(out, i)
			}
		}
	case imap.UIDSet:
		max := s.list[len(s.list)-1].uid
		for i, e := range s.list {
			if inUID(v, e.uid, max) {
				out = append(out, i)
			}
		}
	}
	return out
}

// between dit si n est dans [a, b], 0 valant « * » (le plus grand). « n:* » avec n au-delà désigne quand même le dernier.
func between(n, a, b, max uint32) bool {
	if a == 0 {
		a = max
	}
	if b == 0 {
		b = max
	}
	if a > b {
		a, b = b, a
	}
	return n >= a && n <= b
}

func inSeq(set imap.SeqSet, n, max uint32) bool {
	for _, r := range set {
		if between(n, r.Start, r.Stop, max) {
			return true
		}
	}
	return false
}

func inUID(set imap.UIDSet, n, max imap.UID) bool {
	for _, r := range set {
		if between(uint32(n), uint32(r.Start), uint32(r.Stop), uint32(max)) {
			return true
		}
	}
	return false
}

// messages lit les messages entiers des rangs donnés.
func (s *session) messages(idx []int) (map[string]*mailbox.Message, error) {
	ids := make([]string, 0, len(idx))
	for _, i := range idx {
		ids = append(ids, s.list[i].ID)
	}
	ctx, cancel := opCtx()
	defer cancel()
	ms, err := s.srv.cfg.Boxes.BoxMessages(ctx, s.acc.WorkspaceID, s.acc.ProfileID, ids)
	if err != nil {
		return nil, s.fail("load", err)
	}
	return ms, nil
}

// load rend les octets des messages des rangs donnés.
func (s *session) load(idx []int) (map[string][]byte, error) {
	ms, err := s.messages(idx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(ms))
	for id, m := range ms {
		raw := mailbox.BoxRaw(m)
		out[id] = raw
		s.srv.putSize(id, int64(len(raw)))
	}
	return out, nil
}

func (s *session) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	if s.sel == nil {
		return errNotOpened
	}
	seen := false
	for _, b := range o.BodySection {
		seen = seen || !b.Peek
	}
	for _, b := range o.BinarySection {
		seen = seen || !b.Peek
	}
	seen = seen && !s.readOnly
	body := o.Envelope || o.BodyStructure != nil || len(o.BodySection) > 0 || len(o.BinarySection) > 0 || len(o.BinarySectionSize) > 0

	idx := s.pick(set)
	for len(idx) > 0 {
		n := len(idx)
		if n > fetchBatch {
			n = fetchBatch
		}
		batch := idx[:n]
		idx = idx[n:]
		var need []int
		for _, i := range batch {
			e := s.list[i]
			if body {
				need = append(need, i)
				continue
			}
			if o.RFC822Size && e.Size == 0 {
				if _, ok := s.srv.size(e.ID); !ok {
					need = append(need, i)
				}
			}
		}
		var raws map[string][]byte
		if len(need) > 0 {
			var err error
			if raws, err = s.load(need); err != nil {
				return err
			}
		}
		for _, i := range batch {
			e := s.list[i]
			if body && raws[e.ID] == nil {
				continue // retiré entre-temps : annoncé au prochain passage
			}
			marked := false
			if seen && !e.has(imap.FlagSeen) {
				if err := s.setFlags(e, append(append([]string{}, e.Flags...), string(imap.FlagSeen))); err != nil {
					return err
				}
				marked = true
			}
			if err := s.writeFetch(w.CreateMessage(uint32(i+1)), e, raws[e.ID], o, marked); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *session) writeFetch(w *imapserver.FetchResponseWriter, e *entry, raw []byte, o *imap.FetchOptions, marked bool) error {
	w.WriteUID(e.uid)
	if o.Flags || marked {
		w.WriteFlags(e.imapFlags())
	}
	if o.InternalDate {
		w.WriteInternalDate(e.At)
	}
	if o.RFC822Size {
		n := e.Size
		if raw != nil {
			n = int64(len(raw))
		} else if n == 0 {
			n, _ = s.srv.size(e.ID)
		}
		w.WriteRFC822Size(n)
	}
	if o.Envelope {
		h, _ := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
		w.WriteEnvelope(imapserver.ExtractEnvelope(h))
	}
	if o.BodyStructure != nil {
		w.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
	}
	for _, b := range o.BodySection {
		buf := imapserver.ExtractBodySection(bytes.NewReader(raw), b)
		wc := w.WriteBodySection(b, int64(len(buf)))
		_, werr := wc.Write(buf)
		if cerr := wc.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	for _, b := range o.BinarySection {
		buf := imapserver.ExtractBinarySection(bytes.NewReader(raw), b)
		wc := w.WriteBinarySection(b, int64(len(buf)))
		_, werr := wc.Write(buf)
		if cerr := wc.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	for _, b := range o.BinarySectionSize {
		w.WriteBinarySectionSize(b, imapserver.ExtractBinarySectionSize(bytes.NewReader(raw), b))
	}
	return w.Close()
}

// ---------- drapeaux ----------

func (s *session) setFlags(e *entry, flags []string) error {
	ctx, cancel := opCtx()
	defer cancel()
	if err := s.srv.cfg.Boxes.BoxSetFlags(ctx, s.acc.WorkspaceID, s.acc.ProfileID, e.ID, flags, nil); err != nil {
		return s.fail("flags", err)
	}
	e.Flags = flags
	return nil
}

func applyFlags(cur []string, op imap.StoreFlagsOp, in []imap.Flag) []string {
	var out []string
	add := func(f string) {
		if f == "" || len(f) > maxKeywordLen || len(out) >= maxKeywords || strings.EqualFold(f, `\Recent`) || strings.HasPrefix(f, "$purged") {
			return
		}
		for _, x := range out {
			if strings.EqualFold(x, f) {
				return
			}
		}
		out = append(out, f)
	}
	in2 := map[string]bool{}
	for _, f := range in {
		in2[strings.ToLower(string(f))] = true
	}
	if op != imap.StoreFlagsSet {
		for _, f := range cur {
			if op == imap.StoreFlagsDel && in2[strings.ToLower(f)] {
				continue
			}
			add(f)
		}
	}
	if op != imap.StoreFlagsDel {
		for _, f := range in {
			add(string(f))
		}
	}
	return out
}

func (s *session) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, _ *imap.StoreOptions) error {
	if s.sel == nil {
		return errNotOpened
	}
	if s.readOnly {
		return errReadOnly
	}
	for _, i := range s.pick(set) {
		e := s.list[i]
		if err := s.setFlags(e, applyFlags(e.Flags, f.Op, f.Flags)); err != nil {
			return err
		}
		if f.Silent {
			continue
		}
		m := w.CreateMessage(uint32(i + 1))
		m.WriteUID(e.uid)
		m.WriteFlags(e.imapFlags())
		if err := m.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- retrait, corbeille ----------

// remove retire un message du dossier ouvert : masqué (donc dans la corbeille), ou vidé s'il y est déjà. Rien n'est supprimé.
func (s *session) remove(e *entry) error {
	ctx, cancel := opCtx()
	defer cancel()
	flags := applyFlags(e.Flags, imap.StoreFlagsDel, []imap.Flag{imap.FlagDeleted})
	var err error
	if s.sel.box == mailbox.BoxTrash {
		err = s.srv.cfg.Boxes.BoxPurge(ctx, s.acc.WorkspaceID, s.acc.ProfileID, e.ID, flags)
	} else {
		yes := true
		err = s.srv.cfg.Boxes.BoxSetFlags(ctx, s.acc.WorkspaceID, s.acc.ProfileID, e.ID, flags, &yes)
	}
	if err != nil {
		return s.fail("remove", err)
	}
	return nil
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if s.sel == nil {
		return errNotOpened
	}
	if s.readOnly {
		return errReadOnly
	}
	for i := len(s.list) - 1; i >= 0; i-- {
		e := s.list[i]
		if !e.has(imap.FlagDeleted) || (uids != nil && !uids.Contains(e.uid)) {
			continue
		}
		if err := s.remove(e); err != nil {
			return err
		}
		s.list = append(s.list[:i], s.list[i+1:]...)
		if w != nil { // CLOSE retire sans annoncer
			if err := w.WriteExpunge(uint32(i + 1)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Copy : copier vers la corbeille précède un retrait (« copier, marquer supprimé, purger ») et ne fait rien de plus. Les
// autres dossiers sont des vues du même courrier : y copier un message n'a pas de sens.
func (s *session) Copy(set imap.NumSet, dest string) (*imap.CopyData, error) {
	if s.sel == nil {
		return nil, errNotOpened
	}
	f, ok := folderOf(dest)
	if !ok {
		return nil, no(imap.ResponseCodeTryCreate, "dossier inconnu")
	}
	if f.box != mailbox.BoxTrash {
		return nil, errReadOnly
	}
	return s.copyData(s.pick(set), f), nil
}

func (s *session) copyData(idx []int, dest folderDef) *imap.CopyData {
	var uids imap.UIDSet
	for _, i := range idx {
		uids.AddNum(s.list[i].uid)
	}
	return &imap.CopyData{UIDValidity: s.uidValidity(dest), SourceUIDs: uids, DestUIDs: uids}
}

// Move : vers la corbeille (masqué), ou de la corbeille vers le dossier d'origine (remis).
func (s *session) Move(w *imapserver.MoveWriter, set imap.NumSet, dest string) error {
	if s.sel == nil {
		return errNotOpened
	}
	f, ok := folderOf(dest)
	if !ok {
		return no(imap.ResponseCodeTryCreate, "dossier inconnu")
	}
	toTrash, fromTrash := f.box == mailbox.BoxTrash, s.sel.box == mailbox.BoxTrash
	if s.readOnly || toTrash == fromTrash {
		return errReadOnly
	}
	idx := s.pick(set)
	if len(idx) == 0 {
		return nil
	}
	if err := w.WriteCopyData(s.copyData(idx, f)); err != nil {
		return err
	}
	for k := len(idx) - 1; k >= 0; k-- {
		i := idx[k]
		e := s.list[i]
		ctx, cancel := opCtx()
		err := s.srv.cfg.Boxes.BoxSetFlags(ctx, s.acc.WorkspaceID, s.acc.ProfileID, e.ID, e.Flags, &toTrash)
		cancel()
		if err != nil {
			return s.fail("move", err)
		}
		s.list = append(s.list[:i], s.list[i+1:]...)
		if err := w.WriteExpunge(uint32(i + 1)); err != nil {
			return err
		}
	}
	return nil
}

// ---------- recherche ----------

func (s *session) Search(kind imapserver.NumKind, c *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	if s.sel == nil {
		return nil, errNotOpened
	}
	msgs := map[string]*mailbox.Message{}
	if needsBody(c) {
		from := 0
		if len(s.list) > searchBodyMax {
			from = len(s.list) - searchBodyMax
		}
		for i := from; i < len(s.list); i += fetchBatch {
			var idx []int
			for j := i; j < len(s.list) && j < i+fetchBatch; j++ {
				idx = append(idx, j)
			}
			got, err := s.messages(idx)
			if err != nil {
				return nil, err
			}
			for id, m := range got {
				msgs[id] = m
			}
		}
	}
	d := &imap.SearchData{}
	var seqs imap.SeqSet
	var uids imap.UIDSet
	for i, e := range s.list {
		if !s.match(uint32(i+1), e, msgs[e.ID], c) {
			continue
		}
		n := uint32(i + 1)
		if kind == imapserver.NumKindUID {
			n = uint32(e.uid)
			uids.AddNum(e.uid)
		} else {
			seqs.AddNum(n)
		}
		if d.Min == 0 || n < d.Min {
			d.Min = n
		}
		if n > d.Max {
			d.Max = n
		}
		d.Count++
	}
	if kind == imapserver.NumKindUID {
		d.All = uids
	} else {
		d.All = seqs
	}
	return d, nil
}

func needsBody(c *imap.SearchCriteria) bool {
	if len(c.Header) > 0 || len(c.Body) > 0 || len(c.Text) > 0 || !c.SentSince.IsZero() || !c.SentBefore.IsZero() {
		return true
	}
	for i := range c.Not {
		if needsBody(&c.Not[i]) {
			return true
		}
	}
	for i := range c.Or {
		if needsBody(&c.Or[i][0]) || needsBody(&c.Or[i][1]) {
			return true
		}
	}
	return false
}

func inDay(t, since, before time.Time) bool {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return (since.IsZero() || !d.Before(since)) && (before.IsZero() || d.Before(before))
}

// match : m est nil si la recherche ne lit pas les messages.
func (s *session) match(n uint32, e *entry, m *mailbox.Message, c *imap.SearchCriteria) bool {
	max := uint32(len(s.list))
	for _, set := range c.SeqNum {
		if !inSeq(set, n, max) {
			return false
		}
	}
	for _, set := range c.UID {
		if !inUID(set, e.uid, s.list[len(s.list)-1].uid) {
			return false
		}
	}
	if !inDay(e.At, c.Since, c.Before) {
		return false
	}
	for _, f := range c.Flag {
		if !e.has(f) {
			return false
		}
	}
	for _, f := range c.NotFlag {
		if e.has(f) {
			return false
		}
	}
	size := e.Size
	if size == 0 {
		size, _ = s.srv.size(e.ID)
	}
	if (c.Larger != 0 && size <= c.Larger) || (c.Smaller != 0 && size >= c.Smaller) {
		return false
	}
	if needsBody(&imap.SearchCriteria{Header: c.Header, Body: c.Body, Text: c.Text, SentSince: c.SentSince, SentBefore: c.SentBefore}) {
		if m == nil || !matchMessage(m, c) {
			return false
		}
	}
	for i := range c.Not {
		if s.match(n, e, m, &c.Not[i]) {
			return false
		}
	}
	for i := range c.Or {
		if !s.match(n, e, m, &c.Or[i][0]) && !s.match(n, e, m, &c.Or[i][1]) {
			return false
		}
	}
	return true
}

func contains(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// matchMessage cherche dans les en-têtes et le texte DÉCODÉS (accents compris), pas dans les octets encodés.
func matchMessage(m *mailbox.Message, c *imap.SearchCriteria) bool {
	for _, h := range c.Header {
		ok := false
		for k, vs := range m.Headers {
			if !strings.EqualFold(k, h.Key) {
				continue
			}
			for _, v := range vs {
				ok = ok || contains(v, h.Value)
			}
		}
		if !ok {
			return false
		}
	}
	sent := m.OccurredAt
	if m.SentAt != nil {
		sent = *m.SentAt
	}
	if !inDay(sent, c.SentSince, c.SentBefore) {
		return false
	}
	body := m.BodyText
	if strings.TrimSpace(body) == "" {
		body = mailbox.HTMLToText(m.BodyHTMLSanitized)
	}
	for _, t := range c.Body {
		if !contains(body, t) {
			return false
		}
	}
	for _, t := range c.Text {
		ok := contains(body, t) || contains(m.Subject, t) || contains(m.FromName, t) || contains(m.From, t)
		for _, vs := range m.Headers {
			for _, v := range vs {
				ok = ok || contains(v, t)
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
