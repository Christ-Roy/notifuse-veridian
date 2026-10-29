package mailapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

func TestSend_BuildsQueueEntryWithSameGatesAsSequences(t *testing.T) {
	e, _ := newEnv(t)
	code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", map[string]any{
		"workspace_id": ws, "profile_id": "prof1", "to": "Paul Martin <Paul@Cabinet.fr>", "subject": "Une question", "text": "Bonjour Paul,\nvoir https://asd.fr",
	})
	if code != 202 {
		t.Fatalf("%d %v", code, out)
	}
	if out["status"] != "queued" || !strings.HasPrefix(out["message_id"].(string), "ws1_") || out["queue_entry_id"] == "" || out["thread_id"] == "" {
		t.Fatalf("%v", out)
	}
	if len(e.queue.entries) != 1 {
		t.Fatalf("une entrée en file attendue: %d", len(e.queue.entries))
	}
	en := e.queue.entries[0]
	if en.SourceType != domain.EmailQueueSourceMail || en.IntegrationID != "prof1" || en.ContactEmail != "paul@cabinet.fr" ||
		en.MessageID != out["message_id"] || en.Status != domain.EmailQueueStatusPending || en.ProviderKind != domain.EmailProviderKindSMTP {
		t.Fatalf("%+v", en)
	}
	p := en.Payload
	if p.FromAddress != "hugo.bourdat@animalservicedistribution.fr" || p.FromName != "Hugo Bourdat" || p.Subject != "Une question" ||
		!p.PlainTextOnly || p.TextContent != "Bonjour Paul,\nvoir https://asd.fr" || !strings.Contains(p.HTMLContent, `href="https://asd.fr"`) {
		t.Fatalf("%+v", p)
	}
	if p.EmailOptions.InReplyTo != "" || len(p.EmailOptions.References) != 0 {
		t.Fatal("un premier mail n'a pas de fil")
	}
	if en.Priority != domain.EmailQueuePriorityMarketing {
		t.Fatalf("un premier mail passe en priorité commerciale normale: %d", en.Priority)
	}
	got := e.auth.granted()
	if len(got) != 2 || got[0] != (grant{"message_history", "write"}) || got[1] != (grant{"transactional", "write"}) {
		t.Fatalf("droits d'écriture demandés: %v", got)
	}
}

func TestReply_ThreadsOnParentAndDefaultsFromIt(t *testing.T) {
	e, _ := newEnv(t)
	reply, _ := seed(t, e)
	code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.reply", map[string]any{
		"workspace_id": ws, "in_reply_to_message": reply.ID, "html": "<p>Avec plaisir, quand puis-je vous appeler ?</p>",
	})
	if code != 202 {
		t.Fatalf("%d %v", code, out)
	}
	en := e.queue.entries[0]
	if en.ContactEmail != "marie.reply@exemple-animaux.fr" {
		t.Fatalf("la réponse part vers le Reply-To du message: %q", en.ContactEmail)
	}
	if en.IntegrationID != "prof1" {
		t.Fatalf("la réponse part par le profil de la boîte qui a reçu: %q", en.IntegrationID)
	}
	p := en.Payload
	if p.Subject != "Re: Présentation ASD" {
		t.Fatalf("objet: %q", p.Subject)
	}
	if p.EmailOptions.InReplyTo != "<rep1@exemple-animaux.fr>" {
		t.Fatalf("In-Reply-To: %q", p.EmailOptions.InReplyTo)
	}
	if len(p.EmailOptions.References) != 2 || p.EmailOptions.References[0] != j0ref || p.EmailOptions.References[1] != "<rep1@exemple-animaux.fr>" {
		t.Fatalf("References: %v", p.EmailOptions.References)
	}
	if p.PlainTextOnly {
		t.Fatal("html fourni : pas texte seul")
	}
	if en.Priority != domain.EmailQueuePriorityTransactional {
		t.Fatalf("une réponse à un humain passe devant les relances: %d", en.Priority)
	}
	if out["thread_id"] != reply.ThreadID {
		t.Fatalf("la réponse reste dans le fil: %v / %s", out["thread_id"], reply.ThreadID)
	}
}

func TestSend_Refusals(t *testing.T) {
	e, _ := newEnv(t)
	reply, bounce := seed(t, e)
	_, _ = e.db.Exec(`INSERT INTO contact_lists (email, list_id, status) VALUES ('desinscrit@x.fr','l1','unsubscribed'), ('plainte@x.fr','l1','complained')`)
	base := func() map[string]any {
		return map[string]any{"workspace_id": ws, "profile_id": "prof1", "to": "ok@x.fr", "subject": "S", "text": "T"}
	}
	with := func(k string, v any) map[string]any { m := base(); m[k] = v; return m }
	without := func(k string) map[string]any { m := base(); delete(m, k); return m }
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"destinataire absent", without("to"), 400},
		{"destinataire invalide", with("to", "pas une adresse"), 400},
		{"injection d'en-tête dans le destinataire", with("to", "a@x.fr\r\nBcc: z@x.fr"), 400},
		{"objet absent", without("subject"), 400},
		{"objet multiligne", with("subject", "a\r\nBcc: z@x.fr"), 400},
		{"objet énorme", with("subject", strings.Repeat("x", 301)), 400},
		{"corps absent", without("text"), 400},
		{"corps énorme", with("text", strings.Repeat("x", 300<<10)), 400},
		{"profil absent", without("profile_id"), 400},
		{"profil inconnu", with("profile_id", "nope"), 400},
		{"profil en pause", with("profile_id", "paused"), 409},
		{"profil IMAP, pas un profil d'envoi", with("profile_id", "imap1"), 400},
		{"trop de copies", with("cc", []string{"a@x.fr", "b@x.fr", "c@x.fr", "d@x.fr", "e@x.fr", "f@x.fr"}), 400},
		{"désinscrit", with("to", "desinscrit@x.fr"), 409},
		{"plainte", with("to", "plainte@x.fr"), 409},
		{"adresse en rebond définitif", with("to", bounceTarget(bounce.ContactEmail)), 409},
		{"réponse sans message", map[string]any{"workspace_id": ws}, 400},
	}
	for _, c := range cases {
		route := "/api/veridian/mail.messages.send"
		code, out, _ := e.do(t, "POST", route, c.body)
		if code != c.want {
			t.Errorf("%s: %d (%v), attendu %d", c.name, code, out, c.want)
		}
	}
	if len(e.queue.entries) != 0 {
		t.Fatalf("aucun refus ne doit mettre un mail en file: %d", len(e.queue.entries))
	}
	// refus de suppression: raison lisible, code machine
	_, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", with("to", "desinscrit@x.fr"))
	if out["code"] != "refused" || !strings.Contains(out["error"].(string), "désinscrit") {
		t.Fatalf("%v", out)
	}
	// reply: message_id requis ; send: in_reply_to_message interdit ; on ne répond pas à un envoi
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.reply", base()); code != 400 {
		t.Fatalf("reply sans in_reply_to_message: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", with("in_reply_to_message", reply.ID)); code != 400 {
		t.Fatalf("send avec in_reply_to_message: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.reply", map[string]any{"workspace_id": ws, "in_reply_to_message": "inconnu", "text": "x"}); code != 404 {
		t.Fatalf("parent inconnu: %d", code)
	}
	// droits: la permission transactional:write est exigée en plus
	e.auth.deny = map[grant]error{{"transactional", "write"}: domain.NewPermissionError(domain.PermissionResourceTransactional, domain.PermissionTypeWrite, "non")}
	if code, _, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", base()); code != 403 {
		t.Fatalf("sans transactional:write: %d", code)
	}
}

func bounceTarget(string) string { return "marie@exemple-animaux.fr" }

func TestSend_NoSenderConfigured(t *testing.T) {
	e, h := newEnv(t)
	h.Svc.Sender = nil
	if code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", map[string]any{"workspace_id": ws, "profile_id": "prof1", "to": "a@x.fr", "subject": "S", "text": "T"}); code != 501 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestSend_QueueFailureIsInternal(t *testing.T) {
	e, _ := newEnv(t)
	e.queue.err = context.DeadlineExceeded
	code, out, _ := e.do(t, "POST", "/api/veridian/mail.messages.send", map[string]any{"workspace_id": ws, "profile_id": "prof1", "to": "a@x.fr", "subject": "S", "text": "T"})
	if code != 500 || strings.Contains(out["error"].(string), "deadline") {
		t.Fatalf("%d %v", code, out)
	}
}

func TestActorLabel(t *testing.T) {
	if (Actor{ID: "k", Kind: "agent"}).Label() != "agent:k" || (Actor{Kind: "humain"}).Label() != "humain" {
		t.Fatal("libellé d'acteur")
	}
	_ = time.Now
}
