package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
)

// === Veridian — contrôle EN LECTURE SEULE des gabarits (incident préheader 2026-08-24) ===
//
// Le garde-fou `veridianTemplateLabelLeak` REFUSE l'envoi d'un mail dont le corps
// texte s'ouvre sur un libellé de gabarit résiduel. Conséquence opérationnelle :
// un gabarit encore cassé au moment du déploiement ne fera plus fuiter son nom
// interne — il fera ÉCHOUER ses envois. Il faut donc pouvoir répondre AVANT de
// déployer : « quels gabarits, dans quels workspaces, seraient refusés ? ».
//
// Ce contrôle fait tourner les FONCTIONS DE PRODUCTION (veridianHTMLToText +
// veridianTemplateLabelLeak) et la MÊME résolution du corps texte que
// SMTPService.SendEmail, sur un dump NDJSON de tous les gabarits de tous les
// workspaces. Il n'écrit rien, n'envoie rien, ne touche à aucune base.
//
// Usage :
//   scripts/veridian/audit-template-text-leak.sh          (dump prod + audit)
//   VERIDIAN_TEMPLATE_DUMP=/chemin/all_templates.ndjson \
//     go test ./internal/service/ -run TestVeridianAuditTemplateTextLeak -v
//
// Sans la variable d'environnement, le test se met en skip (il n'a pas sa place
// dans la CI hors audit).

type veridianAuditTemplate struct {
	WS            string `json:"ws"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       int    `json:"version"`
	PlainTextOnly bool   `json:"plain_text_only"`
	Text          string `json:"text"`
	Subject       string `json:"subject"`
	HTML          string `json:"html"`
	MJML          string `json:"mjml"`
}

// veridianAuditSampleData : jeu de données Liquid représentatif d'un envoi cold
// réel. Le corps texte d'un vrai envoi est dérivé du HTML APRÈS rendu Liquid ;
// auditer sur du Liquid non rendu donnerait un verdict faux (les `{{ }}` font
// échapper la première ligne au garde-fou).
var veridianAuditSampleData = notifuse_mjml.MapOfAny{
	"contact": map[string]any{
		"first_name":      "Marie",
		"last_name":       "Durand",
		"email":           "marie@example.test",
		"custom_string_1": "Boutique Durand",
		"custom_string_2": "boutique-durand.fr",
		"custom_string_3": "Lyon",
		"custom_string_4": "Lyon 3e",
		"custom_string_5": "prêt-à-porter",
	},
	"code":       "123456",
	"first_name": "Marie",
}

// veridianAuditCompileHTML compile le `mjml_source` du gabarit avec le MÊME
// compilateur que l'envoi (notifuse_mjml.CompileTemplate → gomjml). Indispensable :
// la colonne `compiled_preview` en base n'est PAS du HTML compilé fiable (on y
// trouve du MJML brut, parfois d'une version antérieure du gabarit) — s'y fier
// rendrait l'audit menteur.
func veridianAuditCompileHTML(t *testing.T, tpl veridianAuditTemplate) (string, string) {
	if strings.TrimSpace(tpl.MJML) == "" {
		return tpl.HTML, "html-stocké"
	}
	src := tpl.MJML
	resp, err := notifuse_mjml.CompileTemplate(notifuse_mjml.CompileTemplateRequest{
		WorkspaceID:  tpl.WS,
		MessageID:    "audit-" + tpl.ID,
		MjmlSource:   &src,
		Subject:      &tpl.Subject,
		TemplateData: veridianAuditSampleData,
		Channel:      "email",
	})
	if err != nil || resp == nil || !resp.Success || resp.HTML == nil {
		msg := "erreur"
		if resp != nil && resp.Error != nil {
			msg = resp.Error.Message
		} else if err != nil {
			msg = err.Error()
		}
		t.Logf("⚠ compilation MJML impossible pour %s/%s (%s) — repli sur le HTML stocké", tpl.WS, tpl.ID, msg)
		return tpl.HTML, "html-stocké(compil KO)"
	}
	return *resp.HTML, "mjml-compilé"
}

// veridianAuditResolvePlainPart reproduit À L'IDENTIQUE la résolution du corps
// texte de SMTPService.SendEmail (cf. smtp_service.go, bloc « incident préheader
// 2026-08-24 »). Toute divergence ici rendrait l'audit menteur.
func veridianAuditResolvePlainPart(tpl veridianAuditTemplate, html string) (string, bool) {
	switch {
	case tpl.PlainTextOnly && strings.TrimSpace(tpl.Text) != "":
		return tpl.Text, false
	case strings.TrimSpace(tpl.Text) != "":
		return strings.TrimSpace(tpl.Text), false
	default:
		return veridianHTMLToText(html), true
	}
}

func TestVeridianAuditTemplateTextLeak(t *testing.T) {
	path := os.Getenv("VERIDIAN_TEMPLATE_DUMP")
	if path == "" {
		t.Skip("VERIDIAN_TEMPLATE_DUMP non défini — audit hors CI (cf. scripts/veridian/audit-template-text-leak.sh)")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("ouverture du dump %s : %v", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	var total, refused, preview int
	var rows []string

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var tpl veridianAuditTemplate
		if err := json.Unmarshal([]byte(line), &tpl); err != nil {
			t.Fatalf("ligne NDJSON illisible : %v", err)
		}
		total++

		hasPreview := strings.Contains(strings.ToLower(tpl.MJML), "<mj-preview")
		hasTitle := strings.Contains(strings.ToLower(tpl.MJML), "<mj-title")
		if hasPreview {
			preview++
		}

		html, origin := veridianAuditCompileHTML(t, tpl)
		plain, derivedFromHTML := veridianAuditResolvePlainPart(tpl, html)
		leak := veridianTemplateLabelLeak(plain)
		// Même décision que SMTPService.SendEmail : sur le chemin « dérivé du
		// HTML », on ne refuse que si la ligne suspecte vient d'un nœud non rendu.
		if leak != "" && derivedFromHTML && !veridianTextComesFromHiddenNode(leak, html) {
			leak = ""
		}

		firstLine := ""
		for _, l := range strings.Split(strings.ReplaceAll(plain, "\r\n", "\n"), "\n") {
			if strings.TrimSpace(l) != "" {
				firstLine = strings.TrimSpace(l)
				break
			}
		}
		if len(firstLine) > 60 {
			firstLine = firstLine[:60] + "…"
		}

		verdict := "OK"
		if leak != "" {
			verdict = "REFUSÉ"
			refused++
		}
		flags := ""
		if hasPreview {
			flags += " mj-preview"
		}
		if hasTitle {
			flags += " mj-title"
		}
		rows = append(rows, fmt.Sprintf("%-8s %-22s %-24s %-6s %-20s 1re ligne=%q%s",
			verdict, tpl.WS, tpl.ID, map[bool]string{true: "plain", false: "html"}[tpl.PlainTextOnly],
			origin, firstLine, flags))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("lecture du dump : %v", err)
	}

	t.Logf("\n=== AUDIT GABARITS — garde-fou veridianTemplateLabelLeak ===\n%s\n"+
		"total=%d  refusés=%d  porteurs de <mj-preview>=%d",
		strings.Join(rows, "\n"), total, refused, preview)

	if refused > 0 {
		t.Errorf("%d gabarit(s) seraient REFUSÉS à l'envoi — corriger avant déploiement", refused)
	}
}
