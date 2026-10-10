package queue

import (
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — gate de FENÊTRE D'ENVOI (sending window) cold outbound, troisième
// frère des gates throttle minute (veridian_provider_throttle.go) et cap
// journalier (veridian_daily_cap.go). Appelé par worker.go:processEntry AVANT
// MarkAsProcessing, avec le MÊME contrat skip-and-reschedule : hors fenêtre,
// l'entrée est re-planifiée à la prochaine ouverture via SetNextRetry SANS
// incrémenter les attempts (pas de retry brûlé, pas de head-of-line blocking).
//
// Différence avec les deux autres gates : le délai n'est pas une estimation de
// token mais l'instant EXACT de réouverture (NextOpening), borné à 24h pour
// re-checker en cas de changement de config (relâchement de la fenêtre, fix
// timezone) sans laisser l'entrée endormie indéfiniment.
//
// Résolution de la config (cascade identique aux rates/caps, du plus spécifique
// au plus général) : payload broadcast → infra (EmailProvider) → workspace
// settings → rien = pas de fenêtre = envoi 24/7 (non-régression upstream).
// Le timezone de la fenêtre, s'il est vide, retombe sur le timezone du workspace.

// veridianSendingWindowMaxRetryDelay borne le report d'une entrée hors fenêtre.
// La prochaine ouverture peut être loin (vendredi soir → lundi 9h = ~60h) ; on
// re-checke au moins une fois par jour pour absorber tout changement de config.
const veridianSendingWindowMaxRetryDelay = 24 * time.Hour

// veridianResolveSendingWindow fusionne la fenêtre des trois niveaux de la
// cascade (du plus spécifique au plus général) : payload broadcast → infra
// (EmailProvider) → workspace. Le premier niveau qui porte une fenêtre VALIDE
// gagne (pas de merge entre niveaux). provider peut être nil (legacy) → niveau
// sauté. Retourne nil si aucun niveau ne définit de fenêtre valide.
func veridianResolveSendingWindow(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) *domain.VeridianSendingWindow {
	if w := entry.Payload.VeridianSendingWindow; w.IsValid() {
		return w
	}
	if provider != nil && provider.VeridianSendingWindow.IsValid() {
		return provider.VeridianSendingWindow
	}
	if workspace != nil && workspace.Settings.VeridianSendingWindow.IsValid() {
		return workspace.Settings.VeridianSendingWindow
	}
	return nil
}

// veridianSendingWindowGate décide si l'entrée doit être reportée parce qu'on
// est hors de la fenêtre d'envoi ouvrable. Retourne (délai jusqu'à réouverture,
// true) si l'entrée doit être re-planifiée, (0, false) si on est dans la fenêtre
// ou si aucune fenêtre ne s'applique (no-op strict). Le timezone de fallback est
// celui du workspace (la fenêtre peut le surcharger).
func (w *EmailQueueWorker) veridianSendingWindowGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	v := w.veridianSendingWindowVerdict(workspace, provider, entry)
	return v.Delay, v.Blocked()
}

var veridianWeekdayLabels = [7]string{"dim", "lun", "mar", "mer", "jeu", "ven", "sam"}

// veridianDescribeWindow rend la fenêtre en une ligne lisible pour la trace
// (« lun,mar,mer,jeu,ven 08:00-19:00 Europe/Paris »).
func veridianDescribeWindow(window *domain.VeridianSendingWindow, fallbackTZ string) string {
	days := "tous les jours"
	if len(window.Days) > 0 {
		labels := make([]string, 0, len(window.Days))
		for _, d := range window.Days {
			if d >= 0 && d <= 6 {
				labels = append(labels, veridianWeekdayLabels[d])
			}
		}
		days = strings.Join(labels, ",")
	}
	tz := window.Timezone
	if tz == "" {
		tz = fallbackTZ
	}
	if tz == "" {
		tz = "UTC"
	}
	return fmt.Sprintf("%s %02d:%02d-%02d:%02d %s", days, window.StartHour, window.StartMinute, window.EndHour, window.EndMinute, tz)
}

// veridianSendingWindowVerdict est la variante structurée de la porte (fiche 62) :
// valeur = jour et heure locaux, limite = la fenêtre, délai = jusqu'à la réouverture
// (borné à 24 h comme avant), détail = réouverture exacte. La fonction historique en
// est un wrapper.
func (w *EmailQueueWorker) veridianSendingWindowVerdict(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) veridianGateVerdict {
	window := veridianResolveSendingWindow(workspace, provider, entry)
	if window == nil {
		return veridianPassVerdict(domain.VeridianGateWindow, nil, nil, "", "no window configured")
	}

	fallbackTZ := ""
	if workspace != nil {
		fallbackTZ = workspace.Settings.Timezone
	}

	now := time.Now()
	loc := window.Location(fallbackTZ)
	localNow := fmt.Sprintf("%s %s", veridianWeekdayLabels[now.In(loc).Weekday()], now.In(loc).Format("15:04"))
	limit := veridianDescribeWindow(window, fallbackTZ)
	if window.IsWithinWindow(now, fallbackTZ) {
		return veridianPassVerdict(domain.VeridianGateWindow, localNow, limit, "", "")
	}
	opening := window.NextOpening(now, fallbackTZ)

	// Hors fenêtre : reporter à la prochaine ouverture, bornée à 24h pour
	// re-checker la config régulièrement.
	delay := window.NextOpening(now, fallbackTZ).Sub(now)
	if delay <= 0 {
		// Défensif : NextOpening n'a pas trouvé de créneau futur (config Days
		// dégénérée). On reporte d'un cycle de re-check plutôt que de partir
		// hors fenêtre.
		delay = veridianSendingWindowMaxRetryDelay
	}
	if delay > veridianSendingWindowMaxRetryDelay {
		delay = veridianSendingWindowMaxRetryDelay
	}
	if delay < time.Second {
		delay = time.Second
	}

	w.logger.WithFields(map[string]interface{}{
		"entry_id":       entry.ID,
		"integration_id": entry.IntegrationID,
		"recipient":      entry.ContactEmail,
		"retry_in":       delay.String(),
	}).Debug("Outside sending window, rescheduling without attempt increment")

	return veridianGateVerdict{
		Gate: domain.VeridianGateWindow, Verdict: domain.VeridianVerdictBlock,
		Value: localNow, Limit: limit, Delay: delay, Reason: domain.VeridianReasonWindowClosed,
		Detail: "reopens=" + opening.UTC().Format(time.RFC3339),
	}
}
