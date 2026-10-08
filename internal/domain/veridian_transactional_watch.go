package domain

import "fmt"

// Veridian fork, lot 5 (08/10/2026) : surveillance du profil transactionnel.
//
// Le profil réservé aux transactionnels n'a aucune porte : un mail transactionnel part
// toujours (lot 4). Deux risques restent sans garde-fou : une boucle côté application
// cliente qui envoie des milliers de mails, et une réputation qui se dégrade (rejets
// durs, plaintes, refus de politique). Cette surveillance les MESURE et les affiche
// (overview, tableau de bord). Elle ne bloque, ne retarde et ne met jamais rien en
// pause : l'alerte est une information pour l'opérateur, pas une porte.

// Niveaux de la surveillance.
const (
	VeridianWatchLevelOK      = "ok"
	VeridianWatchLevelWatch   = "watch"   // à surveiller
	VeridianWatchLevelAlert   = "alert"   // à traiter
	VeridianWatchLevelUnknown = "unknown" // mesure impossible : jamais présenté comme « ok »
)

// Codes d'alerte.
const (
	VeridianWatchCodeVolume        = "volume_spike"
	VeridianWatchCodeHardBounce    = "hard_bounce_rate"
	VeridianWatchCodeComplaint     = "complaint_rate"
	VeridianWatchCodePolicyRefusal = "policy_refusal_rate"
)

// Seuils. Le volume se compare à la moyenne des 7 jours de compte précédents, avec un
// plancher absolu (un profil neuf ou très calme ne déclenche pas pour 12 mails).
// Les taux se calculent sur les 7 derniers jours glissants et exigent un échantillon
// minimal. Valeurs d'usage courant des fournisseurs (plaintes 0,3 %, rejets durs 2 à 5 %),
// documentées dans la fiche 57.
const (
	VeridianWatchVolumeWatchFloor   = 100
	VeridianWatchVolumeAlertFloor   = 200
	VeridianWatchVolumeWatchFactor  = 3.0
	VeridianWatchVolumeAlertFactor  = 5.0
	VeridianWatchMinSample          = 20
	VeridianWatchHardBounceWatch    = 0.02
	VeridianWatchHardBounceAlert    = 0.05
	VeridianWatchComplaintWatch     = 0.001
	VeridianWatchComplaintAlert     = 0.003
	VeridianWatchPolicyRefusalWatch = 0.05
	VeridianWatchPolicyRefusalAlert = 0.15
)

// VeridianTransactionalWatchInput : comptes bruts lus pour le profil transactionnel.
type VeridianTransactionalWatchInput struct {
	SentToday int // envois du jour de compte
	// SentPrevious7Days : envois des 7 jours de compte qui précèdent aujourd'hui.
	SentPrevious7Days int
	// Fenêtre glissante de 7 jours jusqu'à maintenant :
	Sent7d           int
	HardBounces7d    int
	Complaints7d     int
	PolicyRefusals7d int
}

type VeridianTransactionalAlert struct {
	Code    string  `json:"code"`
	Level   string  `json:"level"` // watch | alert
	Value   float64 `json:"value"`
	Watch   float64 `json:"watch_threshold"`
	Alert   float64 `json:"alert_threshold"`
	Message string  `json:"message"`
}

type VeridianTransactionalWatch struct {
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	Level       string `json:"level"`
	// Blocking vaut toujours false : l'alerte ne bloque rien (affiché pour que le
	// contrat soit lisible côté client).
	Blocking          bool                         `json:"blocking"`
	SentToday         int                          `json:"sent_today"`
	BaselinePerDay    float64                      `json:"baseline_per_day"`
	Sent7d            int                          `json:"sent_7d"`
	HardBounceRate    float64                      `json:"hard_bounce_rate_7d"`
	ComplaintRate     float64                      `json:"complaint_rate_7d"`
	PolicyRefusalRate float64                      `json:"policy_refusal_rate_7d"`
	Alerts            []VeridianTransactionalAlert `json:"alerts"`
	// Error : la mesure a échoué (Level = unknown).
	Error string `json:"error,omitempty"`
}

// VeridianEvaluateTransactionalWatch applique les seuils. Pure et sans I/O.
func VeridianEvaluateTransactionalWatch(profileID, profileName string, in VeridianTransactionalWatchInput) VeridianTransactionalWatch {
	w := VeridianTransactionalWatch{
		ProfileID: profileID, ProfileName: profileName, Level: VeridianWatchLevelOK,
		SentToday: in.SentToday, Sent7d: in.Sent7d, Alerts: []VeridianTransactionalAlert{},
		BaselinePerDay: float64(in.SentPrevious7Days) / 7,
	}
	raise := func(a VeridianTransactionalAlert) {
		w.Alerts = append(w.Alerts, a)
		if a.Level == VeridianWatchLevelAlert {
			w.Level = VeridianWatchLevelAlert
		} else if w.Level != VeridianWatchLevelAlert {
			w.Level = VeridianWatchLevelWatch
		}
	}

	// Volume : le plus haut du plancher et du multiple de la moyenne.
	alertAt := maxFloat(VeridianWatchVolumeAlertFloor, VeridianWatchVolumeAlertFactor*w.BaselinePerDay)
	watchAt := maxFloat(VeridianWatchVolumeWatchFloor, VeridianWatchVolumeWatchFactor*w.BaselinePerDay)
	switch {
	case float64(in.SentToday) >= alertAt:
		raise(VeridianTransactionalAlert{Code: VeridianWatchCodeVolume, Level: VeridianWatchLevelAlert, Value: float64(in.SentToday), Watch: watchAt, Alert: alertAt,
			Message: fmt.Sprintf("%d mails transactionnels aujourd'hui, moyenne des 7 jours précédents %.0f par jour", in.SentToday, w.BaselinePerDay)})
	case float64(in.SentToday) >= watchAt:
		raise(VeridianTransactionalAlert{Code: VeridianWatchCodeVolume, Level: VeridianWatchLevelWatch, Value: float64(in.SentToday), Watch: watchAt, Alert: alertAt,
			Message: fmt.Sprintf("%d mails transactionnels aujourd'hui, moyenne des 7 jours précédents %.0f par jour", in.SentToday, w.BaselinePerDay)})
	}

	// Réputation : taux sur 7 jours, avec échantillon minimal.
	if in.Sent7d >= VeridianWatchMinSample {
		rate := func(n int) float64 { return float64(n) / float64(in.Sent7d) }
		w.HardBounceRate = rate(in.HardBounces7d)
		w.ComplaintRate = rate(in.Complaints7d)
		w.PolicyRefusalRate = rate(in.PolicyRefusals7d)
		rateAlert := func(code string, value, watch, alert float64, label string) {
			switch {
			case value >= alert:
				raise(VeridianTransactionalAlert{Code: code, Level: VeridianWatchLevelAlert, Value: value, Watch: watch, Alert: alert, Message: fmt.Sprintf("%s : %.1f %% sur 7 jours", label, value*100)})
			case value >= watch:
				raise(VeridianTransactionalAlert{Code: code, Level: VeridianWatchLevelWatch, Value: value, Watch: watch, Alert: alert, Message: fmt.Sprintf("%s : %.1f %% sur 7 jours", label, value*100)})
			}
		}
		rateAlert(VeridianWatchCodeHardBounce, w.HardBounceRate, VeridianWatchHardBounceWatch, VeridianWatchHardBounceAlert, "rejets durs")
		rateAlert(VeridianWatchCodeComplaint, w.ComplaintRate, VeridianWatchComplaintWatch, VeridianWatchComplaintAlert, "plaintes")
		rateAlert(VeridianWatchCodePolicyRefusal, w.PolicyRefusalRate, VeridianWatchPolicyRefusalWatch, VeridianWatchPolicyRefusalAlert, "refus de politique")
	}
	return w
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// VeridianUnknownTransactionalWatch : la mesure n'a pas pu être faite. Jamais « ok ».
func VeridianUnknownTransactionalWatch(profileID, profileName, reason string) VeridianTransactionalWatch {
	return VeridianTransactionalWatch{ProfileID: profileID, ProfileName: profileName, Level: VeridianWatchLevelUnknown, Alerts: []VeridianTransactionalAlert{}, Error: reason}
}
