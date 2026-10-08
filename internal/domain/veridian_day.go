package domain

import (
	"strings"
	"time"
)

// Veridian fork, lot 4 (08/10/2026) : le « jour » des plafonds journaliers.
//
// Avant ce lot, tous les compteurs « aujourd'hui » (portes du worker, réservation
// atomique veridian_daily_quota_counters, overview) se remettaient à zéro à minuit
// UTC, alors que la fenêtre d'envoi d'un profil raisonne en Europe/Paris : un
// plafond « 30 par jour » se vidait à 02h00 l'été, en pleine nuit, et l'écran
// montrait « aujourd'hui » avec la mauvaise frontière. Le jour de compte est
// désormais le jour civil du FUSEAU DE LA FENÊTRE D'ENVOI du profil (celle du
// profil, sinon celle du workspace, sinon le fuseau du workspace, sinon UTC).
//
// Une fenêtre de jour = trois valeurs :
//   - Label : minuit UTC de la date civile locale. C'est la clé du compteur
//     (colonne quota_day, type DATE) : la date civile ne dépend pas de l'heure d'été ;
//   - Start / End : les instants UTC exacts du début et de la fin de ce jour local.
//     Un jour d'heure d'été dure 23 h, un jour d'heure d'hiver 25 h : les bornes se
//     calculent en heure locale, jamais par « + 24 h ».

// VeridianDay est le jour de compte d'un profil à un instant donné.
type VeridianDay struct {
	Label    time.Time // minuit UTC de la date civile locale (clé du compteur)
	Start    time.Time // début du jour local, en UTC (inclus)
	End      time.Time // début du jour local suivant, en UTC (exclu)
	Location string    // nom IANA du fuseau utilisé
}

// LabelDate donne la date civile « AAAA-MM-JJ », valeur du paramètre DATE du compteur.
func (d VeridianDay) LabelDate() string { return d.Label.Format("2006-01-02") }

// VeridianDayAt calcule le jour de compte contenant `now` dans le fuseau `loc`.
// loc nil = UTC (comportement antérieur au lot 4).
func VeridianDayAt(now time.Time, loc *time.Location) VeridianDay {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	y, m, d := local.Date()
	return VeridianDay{
		Label:    time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
		Start:    time.Date(y, m, d, 0, 0, 0, 0, loc).UTC(),
		End:      time.Date(y, m, d+1, 0, 0, 0, 0, loc).UTC(),
		Location: loc.String(),
	}
}

// VeridianDayLocation résout le fuseau du jour de compte d'un profil :
// fenêtre valide du profil (son fuseau, sinon celui du workspace), sinon fenêtre
// valide du workspace, sinon fuseau du workspace, sinon UTC. Un nom IANA invalide
// retombe sur le niveau suivant, jamais d'erreur. Même précédence que la porte de
// fenêtre d'envoi (veridianResolveSendingWindow), sans l'éventuel override de
// diffusion : un compteur keyé par profil ne peut pas changer de jour message
// par message.
func VeridianDayLocation(ws *Workspace, provider *EmailProvider) *time.Location {
	workspaceTZ := ""
	if ws != nil {
		workspaceTZ = ws.Settings.Timezone
	}
	var window *VeridianSendingWindow
	switch {
	case provider != nil && provider.VeridianSendingWindow.IsValid():
		window = provider.VeridianSendingWindow
	case ws != nil && ws.Settings.VeridianSendingWindow.IsValid():
		window = ws.Settings.VeridianSendingWindow
	}
	if window != nil {
		return window.resolveLocation(workspaceTZ)
	}
	if tz := strings.TrimSpace(workspaceTZ); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// VeridianDayFor est le raccourci : le jour de compte de ce profil à `now`.
func VeridianDayFor(ws *Workspace, provider *EmailProvider, now time.Time) VeridianDay {
	return VeridianDayAt(now, VeridianDayLocation(ws, provider))
}
