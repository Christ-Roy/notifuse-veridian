package activite

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func pendings(n int, f func(i int) Event) []Pending {
	out := make([]Pending, n)
	for i := range out {
		out[i] = Pending{ID: int64(i + 1), Event: f(i)}
	}
	return out
}

func small(i int) Event {
	return Event{Event: "message.sent", OccurredAt: "2026-11-26T10:00:00Z", RFCMessageID: fmt.Sprintf("m%d@x.fr", i), RecipientEmail: "a@b.fr", Nature: NatureCampaign}
}

// 201 événements : deux lots, 200 puis 1, dans l'ordre, chacun avec son lot_id.
func TestSplit_201EvenementsCoupes(t *testing.T) {
	lots := Split("ws1", pendings(201, small), MaxEvents, MaxBytes)
	if len(lots) != 2 || len(lots[0].Lot.Events) != 200 || len(lots[1].Lot.Events) != 1 {
		t.Fatalf("201 événements : %d lots %v", len(lots), sizes(lots))
	}
	if lots[1].IDs[0] != 201 || lots[0].Lot.LotID == lots[1].Lot.LotID || lots[0].Lot.WorkspaceID != "ws1" {
		t.Fatalf("ordre, lot_id ou espace : %+v", lots[1].IDs)
	}
	var back Lot
	if err := json.Unmarshal(lots[0].Body, &back); err != nil || len(back.Events) != 200 || back.LotID != lots[0].Lot.LotID {
		t.Fatalf("le corps est le lot : %v", err)
	}
	if strings.Contains(string(lots[0].Body), "hub_tenant_id") {
		t.Fatal("pas de hub_tenant_id dans le corps (409 au moindre désaccord)")
	}
}

// 1 Mo : des événements énormes (jamais produits par ReadReady, qui borne chaque champ) coupent le lot avant la borne.
func TestSplit_UnMegaOctetRespecte(t *testing.T) {
	big := strings.Repeat("é", 3000) // 6 000 octets
	rows := pendings(200, func(i int) Event { e := small(i); e.Subject = big; return e })
	lots := Split("ws1", rows, MaxEvents, MaxBytes)
	if len(lots) < 2 {
		t.Fatalf("200 x 6 Ko dépassent 1 Mo : %d lot(s)", len(lots))
	}
	total := 0
	for _, l := range lots {
		if len(l.Body) > MaxBytes {
			t.Fatalf("lot de %d octets > %d", len(l.Body), MaxBytes)
		}
		total += len(l.IDs)
	}
	if total != 200 {
		t.Fatalf("aucun événement perdu : %d", total)
	}
	// Une borne plus basse est respectée au plus près.
	for _, l := range Split("ws1", pendings(50, small), MaxEvents, 2000) {
		if len(l.Body) > 2000 {
			t.Fatalf("borne de 2000 octets : %d", len(l.Body))
		}
	}
}

// Champs bornés comme ReadReady les borne : 200 événements au pire tiennent dans un lot sous 1 Mo.
func TestSplit_PireCasBorneTientEnUnLot(t *testing.T) {
	worst := clip(strings.Repeat("😀", 400), maxSubject)
	mail := cleanEmail(strings.Repeat("a", 400) + "@x.fr")
	rows := pendings(200, func(i int) Event {
		e := small(i)
		e.Subject, e.FromName, e.RecipientEmail, e.FromEmail = worst, worst, mail, mail
		return e
	})
	lots := Split("ws1", rows, MaxEvents, MaxBytes)
	if len(lots) != 1 || len(lots[0].Body) > MaxBytes {
		t.Fatalf("%v", sizes(lots))
	}
}

func sizes(l []Batch) []int {
	var s []int
	for _, b := range l {
		s = append(s, len(b.IDs))
	}
	return s
}

// L'URL d'un clic est réduite à son hôte : ni chemin, ni paramètres (jetons), ni port, ni identifiants.
func TestHostOf_SeulementLHote(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@Shop.Exemple.COM:8443/panier?token=abc123&u=marie@x.fr#ancre": "shop.exemple.com",
		"http://exemple.fr/":              "exemple.fr",
		"https://exemple.fr.":             "exemple.fr",
		"mailto:marie@exemple.fr":         "",
		"tel:+33600000000":                "",
		"javascript:alert(1)":             "",
		"pas une url":                     "",
		"https://[2001:db8::1]:443/x?y=1": "2001:db8::1",
		"":                                "",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, attendu %q", in, got, want)
		}
	}
}

// Les clés d'un événement sont celles du contrat, et aucune ne peut porter un corps ou une URL.
func TestEvent_ClesDuContratSansCorps(t *testing.T) {
	b, _ := json.Marshal(Event{Event: "message.clicked", OccurredAt: "x", RFCMessageID: "x", ThreadID: "x", RecipientEmail: "x", FromEmail: "x",
		FromName: "x", Subject: "x", Nature: "x", ListID: "x", AutomationID: "x", BroadcastID: "x", BounceType: "hard", Host: "x"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "automation_id,bounce_type,broadcast_id,event,from_email,from_name,host,list_id,nature,occurred_at,recipient_email,rfc_message_id,subject,thread_id"
	if strings.Join(keys, ",") != want {
		t.Fatalf("clés : %s", strings.Join(keys, ","))
	}
}

func TestClassify_VerdictsDuContrat(t *testing.T) {
	want := map[int]Verdict{200: Accepted, 202: Accepted, 400: Refused, 409: Refused, 413: Refused, 401: Unauthorized, 403: Unauthorized,
		429: Retry, 500: Retry, 502: Retry, 503: Retry, 404: Retry}
	for code, v := range want {
		if got := Classify(code); got != v {
			t.Errorf("%d : %s, attendu %s", code, got, v)
		}
	}
}

func TestBackoff_CroissantBorneRetryAfter(t *testing.T) {
	prev := time.Duration(0)
	for n := 1; n <= 12; n++ {
		d := Backoff(n, 0, 30*time.Minute)
		if d < prev || d > 30*time.Minute {
			t.Fatalf("n=%d : %s après %s", n, d, prev)
		}
		prev = d
	}
	if Backoff(1, 0, 30*time.Minute) != 30*time.Second || prev != 30*time.Minute {
		t.Fatalf("30 s puis plafond : %s", prev)
	}
	if Backoff(1, 10*time.Minute, 30*time.Minute) != 10*time.Minute {
		t.Fatal("Retry-After plus long l'emporte")
	}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	if retryAfter("120", now) != 2*time.Minute || retryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now) != 90*time.Second || retryAfter("x", now) != 0 {
		t.Fatal("Retry-After en secondes et en date HTTP")
	}
}

// Vecteur officiel de la spécification Standard Webhooks.
func TestSign_VecteurStandardWebhooks(t *testing.T) {
	got, err := Sign("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))
	if err != nil || got != "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := Sign("whsec_pas du base64 !", "id", 1, nil); err == nil {
		t.Fatal("secret illisible refusé")
	}
}

// hubVerify : portage ligne à ligne de veridian-hub/lib/evenements/signature.ts (verifierStandardWebhooks), la routine qui lira le lot.
func hubVerify(secret string, h http.Header, body string, now time.Time) string {
	id, ts, sig := h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature")
	if id == "" || len(id) > 128 || ts == "" || sig == "" {
		return "invalid_headers"
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(ts) < 9 || len(ts) > 13 {
		return "invalid_headers"
	}
	if d := now.Sub(time.Unix(n, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return "timestamp_expired"
	}
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "." + body))
	want := m.Sum(nil)
	ok := false
	for _, s := range strings.Split(sig, " ") {
		v, b64, _ := strings.Cut(s, ",")
		got, _ := base64.StdEncoding.DecodeString(b64)
		if v == "v1" && len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1 {
			ok = true
		}
	}
	if ok {
		return "ok"
	}
	return "invalid_signature"
}

// verifyBoth : la bibliothèque Standard Webhooks de référence ET la routine du Hub acceptent la requête.
func verifyBoth(t *testing.T, secret string, h http.Header, body []byte) {
	t.Helper()
	wh, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := wh.Verify(body, h); err != nil {
		t.Fatalf("bibliothèque Standard Webhooks : %v", err)
	}
	if v := hubVerify(secret, h, string(body), time.Now()); v != "ok" {
		t.Fatalf("routine du Hub : %s", v)
	}
}
