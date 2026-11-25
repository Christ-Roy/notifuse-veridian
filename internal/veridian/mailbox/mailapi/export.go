package mailapi

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
)

// Export d'un dossier d'une boîte en .mbox (fiche 92) : se relit dans Thunderbird, Apple Mail ou chez un autre fournisseur. Le droit est relu à
// chaque lot, un seul export à la fois par espace.

const exportBatch = 50

var exportFolders = map[string]mailbox.BoxFolder{"inbox": mailbox.BoxInbox, "sent": mailbox.BoxSent, "rejects": mailbox.BoxRejects, "trash": mailbox.BoxTrash}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	p, err := readParams(r, maxBody)
	if err != nil {
		h.fail(w, err)
		return
	}
	ws, profile := p.str("workspace_id"), p.str("profile_id")
	ctx, err := h.Svc.AuthorizeStream(r.Context(), ws)
	if err != nil {
		h.fail(w, err)
		return
	}
	folder, ok := exportFolders[p.str("folder")]
	if !ok || profile == "" {
		h.fail(w, invalid("profile_id requis, folder : inbox, sent, rejects ou trash"))
		return
	}
	if !h.acquireExport(ws) {
		writeErr(w, http.StatusTooManyRequests, "too_many_exports", "un export est déjà en cours pour cet espace")
		return
	}
	defer h.releaseExport(ws)
	index, err := h.Svc.Store.BoxIndex(ctx, ws, profile, folder)
	if err != nil {
		h.fail(w, err)
		return
	}
	name := fmt.Sprintf("%s-%s-%s.mbox", ws, p.str("folder"), h.Svc.now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/mbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	h.Log.WithFields(map[string]interface{}{"workspace_id": ws, "profile_id": profile, "folder": string(folder), "messages": len(index)}).Info("Mail API: mailbox exported")
	for len(index) > 0 {
		n := min(exportBatch, len(index))
		batch := index[:n]
		index = index[n:]
		// Droit perdu ou panne en cours d'export : la connexion est coupée, un fichier tronqué ne passe pas pour complet.
		if _, err := h.Svc.AuthorizeStream(r.Context(), ws); err != nil {
			panic(http.ErrAbortHandler)
		}
		ids := make([]string, 0, n)
		for _, e := range batch {
			ids = append(ids, e.ID)
		}
		ms, err := h.Svc.Store.BoxMessages(ctx, ws, profile, ids)
		if err != nil {
			h.Log.WithField("error", err.Error()).Error("Mail API: export interrupted")
			panic(http.ErrAbortHandler)
		}
		for _, e := range batch {
			if m := ms[e.ID]; m != nil {
				if err := writeMbox(w, m.From, e.At, mailbox.BoxRaw(m)); err != nil {
					return
				}
			}
		}
		if fl := flusherOf(w); fl != nil {
			fl.Flush()
		}
	}
}

func (h *Handler) acquireExport(ws string) bool {
	h.streamMu.Lock()
	defer h.streamMu.Unlock()
	if h.exports == nil {
		h.exports = map[string]bool{}
	}
	if h.exports[ws] {
		return false
	}
	h.exports[ws] = true
	return true
}

func (h *Handler) releaseExport(ws string) {
	h.streamMu.Lock()
	delete(h.exports, ws)
	h.streamMu.Unlock()
}

// writeMbox écrit un message au format mboxrd : ligne « From », fins de ligne LF, toute ligne « >*From » du corps protégée d'un « > » de plus.
func writeMbox(w io.Writer, from string, at time.Time, raw []byte) error {
	addr := "MAILER-DAEMON"
	if a, err := mail.ParseAddress(from); err == nil {
		addr = a.Address
	}
	out := bufio.NewWriter(w)
	fmt.Fprintf(out, "From %s %s\n", addr, at.UTC().Format(time.ANSIC))
	for _, line := range bytes.Split(bytes.TrimRight(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), "\n"), []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimLeft(line, ">"), []byte("From ")) {
			_ = out.WriteByte('>')
		}
		_, _ = out.Write(line)
		_ = out.WriteByte('\n')
	}
	_ = out.WriteByte('\n')
	return out.Flush()
}
