package mailbox

import (
	"context"
	"sort"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

// Rangement par adresse (fiche 84). La boîte d'un profil se définit par l'adresse qui a REÇU le mail : la boîte IMAP
// d'où il a été lu (Gmail commun à plusieurs profils) ou l'envoi qu'il cite (relance partie d'un autre profil) ne
// désignent pas le bon profil. Mesuré le 23/11/2026 sur greenbeamcraft : 24 réponses sur 159 rangées ailleurs.

// Owners : adresse d'expéditeur (minuscules) -> profils d'envoi qui la portent.
type Owners map[string][]string

// OwnersOf construit la table des adresses d'un workspace.
func OwnersOf(ws *domain.Workspace) Owners {
	o := Owners{}
	if ws == nil {
		return o
	}
	for i := range ws.Integrations {
		p := &ws.Integrations[i]
		if p.Type != domain.IntegrationTypeEmail {
			continue
		}
		for _, s := range p.EmailProvider.Senders {
			a := strings.ToLower(strings.TrimSpace(s.Email))
			if a == "" || containsStr(o[a], p.ID) {
				continue
			}
			o[a] = append(o[a], p.ID)
		}
	}
	return o
}

// Resolve rend le profil qui possède l'une des adresses de réception. Une adresse portée par plusieurs profils est
// départagée par prefer (profil de la boîte lue, puis de l'envoi cité). Des destinataires de profils différents, ou une
// adresse partagée que rien ne départage : "" (l'appelant garde son choix).
func (o Owners) Resolve(addrs []string, prefer ...string) string {
	cands := map[string]struct{}{}
	for _, a := range addrs {
		for _, p := range o[strings.ToLower(strings.TrimSpace(a))] {
			cands[p] = struct{}{}
		}
	}
	if len(cands) == 1 {
		for p := range cands {
			return p
		}
	}
	for _, p := range prefer {
		if _, ok := cands[p]; ok {
			return p
		}
	}
	return ""
}

// recipientAddrs : les adresses de réception d'un mail, en-têtes de remise compris (une redirection garde l'adresse
// d'origine dans Delivered-To ou X-Forwarded-To quand To est une liste ou vide).
func recipientAddrs(p *Parsed) []string {
	out := append(append([]string{}, p.To...), p.Cc...)
	for _, h := range []string{"Delivered-To", "X-Original-To", "X-Forwarded-To", "Envelope-To"} {
		for _, v := range p.Headers[h] {
			for _, a := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '<' || r == '>' }) {
				if strings.Contains(a, "@") {
					out = append(out, a)
				}
			}
		}
	}
	return out
}

// ReassignOwnersSQL range un profil par l'adresse de réception pour le courrier déjà stocké. $1 profil, $2 ses adresses
// (portées par lui seul), $3 les adresses des autres profils : un mail adressé à deux profils n'est pas déplacé.
const ReassignOwnersSQL = `UPDATE veridian_mail_message m SET profile_id = $1, updated_at = NOW()
WHERE m.direction = 'in' AND m.profile_id IS DISTINCT FROM $1
  AND EXISTS (SELECT 1 FROM unnest(m.to_addrs || m.cc_addrs) a WHERE lower(a) = ANY($2))
  AND NOT EXISTS (SELECT 1 FROM unnest(m.to_addrs || m.cc_addrs) a WHERE lower(a) = ANY($3))`

// ReassignOwners applique ReassignOwnersSQL à chaque profil. Idempotent : sans écart, aucune ligne touchée.
func (s *Store) ReassignOwners(ctx context.Context, ws string, o Owners) (int64, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return 0, err
	}
	sole, all := map[string][]string{}, map[string][]string{}
	for a, ps := range o {
		if len(ps) == 1 {
			sole[ps[0]] = append(sole[ps[0]], a)
		}
		for _, p := range ps {
			all[p] = append(all[p], a)
		}
	}
	profiles := make([]string, 0, len(sole))
	for p := range sole {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)
	var total int64
	for _, p := range profiles {
		var others []string
		for a := range o {
			if !containsStr(all[p], a) {
				others = append(others, a)
			}
		}
		res, err := db.ExecContext(ctx, ReassignOwnersSQL, p, pq.Array(sole[p]), pq.Array(nonNilStr(others)))
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func containsStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
