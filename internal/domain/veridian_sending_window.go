package domain

import (
	"strconv"
	"strings"
	"time"
)

// Veridian fork — FENÊTRE D'ENVOI (sending window) cold outbound.
//
// En cold outreach, marteler une boîte à 3h du matin ou le dimanche est un
// signal anti-spam (et inutile : personne ne lit). On veut un envoi cantonné
// aux heures ouvrables (ex. 9h-18h, lundi-vendredi), dans le fuseau du
// workspace ou de l'infra. Hors fenêtre → l'entrée est re-planifiée à la
// prochaine ouverture (gate worker skip-and-reschedule, même pattern que le
// throttle minute et le cap journalier : SetNextRetry SANS incrément d'attempts,
// délai borné). Aucune fenêtre configurée = envoi 24/7 (non-régression upstream
// stricte).
//
// La fenêtre se règle dans la MÊME cascade que les rates/caps cold (du plus
// spécifique au plus général) : broadcast (metadata) → infra (EmailProvider) →
// workspace (settings) → rien = pas de fenêtre. Le premier niveau qui définit
// une fenêtre VALIDE gagne (pas de merge entre niveaux).

// VeridianSendingWindowMetadataKey est la clé broadcast.metadata portant la
// fenêtre d'envoi (niveau le plus spécifique de la cascade).
const VeridianSendingWindowMetadataKey = "veridian_sending_window"

// VeridianSendingWindow décrit une plage horaire ouvrable hebdomadaire.
//
//	Days     : jours autorisés (0=dimanche … 6=samedi, convention time.Weekday).
//	           Vide = tous les jours.
//	StartHour: heure d'ouverture incluse (0-23), minute StartMinute (0-59).
//	EndHour  : heure de fermeture EXCLUSIVE (0-24), minute EndMinute (0-59).
//	           24h00 = fin de journée (minuit du lendemain non inclus).
//	Timezone : nom IANA (ex. "Europe/Paris"). Vide = fallback résolu par
//	           l'appelant (timezone workspace, sinon UTC).
//
// Une fenêtre où Start == End (et StartMinute == EndMinute) est considérée
// INVALIDE (plage vide) et traitée comme "pas de fenêtre" par la cascade : on
// ne veut jamais geler définitivement le pipeline sur une config absurde.
type VeridianSendingWindow struct {
	Days        []int  `json:"days,omitempty"`
	StartHour   int    `json:"start_hour"`
	StartMinute int    `json:"start_minute,omitempty"`
	EndHour     int    `json:"end_hour"`
	EndMinute   int    `json:"end_minute,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

// IsZero retourne true si la fenêtre n'est pas configurée (struct nil/vide).
func (w *VeridianSendingWindow) IsZero() bool {
	if w == nil {
		return true
	}
	return len(w.Days) == 0 &&
		w.StartHour == 0 && w.StartMinute == 0 &&
		w.EndHour == 0 && w.EndMinute == 0 &&
		w.Timezone == ""
}

// startMinutes / endMinutes convertissent la borne en minutes depuis minuit pour
// une comparaison simple (et borner proprement le passage à minuit).
func (w *VeridianSendingWindow) startMinutes() int { return w.StartHour*60 + w.StartMinute }
func (w *VeridianSendingWindow) endMinutes() int   { return w.EndHour*60 + w.EndMinute }

// IsValid retourne true si la fenêtre définit une plage horaire NON vide et
// cohérente. Une plage vide (start == end) ou inversée (end < start) est rejetée
// → traitée comme "pas de fenêtre" (envoi 24/7) plutôt que de tout bloquer.
// Les jours, eux, sont optionnels (vide = tous les jours).
func (w *VeridianSendingWindow) IsValid() bool {
	if w == nil {
		return false
	}
	if w.StartHour < 0 || w.StartHour > 23 || w.StartMinute < 0 || w.StartMinute > 59 {
		return false
	}
	if w.EndHour < 0 || w.EndHour > 24 || w.EndMinute < 0 || w.EndMinute > 59 {
		return false
	}
	// Plage strictement croissante : end > start (en minutes). Pas de fenêtre
	// qui enjambe minuit (24/7 = pas de fenêtre du tout, pas un "wrap").
	return w.endMinutes() > w.startMinutes()
}

// allowsDay retourne true si le jour de la semaine donné est autorisé.
// Days vide = tous les jours.
func (w *VeridianSendingWindow) allowsDay(d time.Weekday) bool {
	if len(w.Days) == 0 {
		return true
	}
	for _, day := range w.Days {
		if day == int(d) {
			return true
		}
	}
	return false
}

// resolveLocation retourne la *time.Location de la fenêtre. Précédence :
// Timezone de la fenêtre → fallback fourni par l'appelant → UTC. Un nom IANA
// invalide retombe sur le fallback (best-effort, jamais d'erreur).
func (w *VeridianSendingWindow) resolveLocation(fallbackTZ string) *time.Location {
	for _, tz := range []string{w.Timezone, fallbackTZ} {
		if tz == "" {
			continue
		}
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// IsWithinWindow retourne true si `now` tombe dans la fenêtre (jour autorisé ET
// heure dans [start, end[). fallbackTZ est le timezone à utiliser si la fenêtre
// n'en porte pas (typiquement le timezone du workspace). Une fenêtre invalide
// laisse tout passer (return true) — non-régression.
func (w *VeridianSendingWindow) IsWithinWindow(now time.Time, fallbackTZ string) bool {
	if !w.IsValid() {
		return true
	}
	local := now.In(w.resolveLocation(fallbackTZ))
	if !w.allowsDay(local.Weekday()) {
		return false
	}
	minutes := local.Hour()*60 + local.Minute()
	return minutes >= w.startMinutes() && minutes < w.endMinutes()
}

// NextOpening retourne le prochain instant (>= now) où la fenêtre s'ouvre.
// Si `now` est déjà dans la fenêtre, retourne `now`. Sinon avance jour par jour
// (max 7 jours, suffisant pour couvrir une config Days exotique) jusqu'au
// prochain créneau ouvrable et renvoie son heure de début dans le fuseau résolu,
// puis reconverti en UTC pour SetNextRetry (qui raisonne en absolu).
// Fenêtre invalide → retourne `now` (pas d'attente).
func (w *VeridianSendingWindow) NextOpening(now time.Time, fallbackTZ string) time.Time {
	if !w.IsValid() {
		return now
	}
	loc := w.resolveLocation(fallbackTZ)
	local := now.In(loc)

	if w.IsWithinWindow(now, fallbackTZ) {
		return now
	}

	// Pour aujourd'hui : si on est AVANT l'ouverture et que le jour est autorisé,
	// le prochain créneau est l'ouverture du jour même.
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(),
		w.StartHour, w.StartMinute, 0, 0, loc)
	if w.allowsDay(local.Weekday()) && !startOfToday.Before(local) {
		return startOfToday.UTC()
	}

	// Sinon, avance jusqu'au prochain jour autorisé et prends son ouverture.
	for i := 1; i <= 7; i++ {
		day := local.AddDate(0, 0, i)
		if w.allowsDay(day.Weekday()) {
			opening := time.Date(day.Year(), day.Month(), day.Day(),
				w.StartHour, w.StartMinute, 0, 0, loc)
			return opening.UTC()
		}
	}
	// Garde-fou théorique (Days contient des valeurs hors 0-6) : ne jamais
	// bloquer indéfiniment, retomber sur now.
	return now
}

// VeridianSendingWindowFromMetadata parse une fenêtre d'envoi depuis le metadata
// broadcast (clé veridian_sending_window). Tolérant : accepte une map JSON déjà
// désérialisée (cas metadata MapOfAny). Retourne nil si absent, malformé ou
// invalide (→ la cascade passe au niveau suivant).
//
// Forme attendue (JSON) :
//
//	"veridian_sending_window": {
//	  "days": [1,2,3,4,5], "start_hour": 9, "end_hour": 18,
//	  "timezone": "Europe/Paris"
//	}
func VeridianSendingWindowFromMetadata(metadata MapOfAny) *VeridianSendingWindow {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[VeridianSendingWindowMetadataKey]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	w := &VeridianSendingWindow{}
	if v, ok := veridianToInt(m["start_hour"]); ok {
		w.StartHour = v
	}
	if v, ok := veridianToInt(m["start_minute"]); ok {
		w.StartMinute = v
	}
	if v, ok := veridianToInt(m["end_hour"]); ok {
		w.EndHour = v
	}
	if v, ok := veridianToInt(m["end_minute"]); ok {
		w.EndMinute = v
	}
	if tz, ok := m["timezone"].(string); ok {
		w.Timezone = strings.TrimSpace(tz)
	}
	if days, ok := m["days"].([]any); ok {
		for _, d := range days {
			if v, ok := veridianToInt(d); ok && v >= 0 && v <= 6 {
				w.Days = append(w.Days, v)
			}
		}
	}

	if !w.IsValid() {
		return nil
	}
	return w
}

// veridianParseWeekdayList parse une liste de jours depuis une chaîne CSV
// ("1,2,3,4,5") — utilitaire pour les surfaces de config texte (UI, env). Tolère
// les espaces. Ignore les valeurs hors 0-6. Exporté pour réutilisation côté HTTP.
func veridianParseWeekdayList(csv string) []int {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(csv, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || v < 0 || v > 6 {
			continue
		}
		out = append(out, v)
	}
	return out
}
