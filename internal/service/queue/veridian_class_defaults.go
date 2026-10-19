package queue

import "github.com/Notifuse/notifuse/internal/domain"

// veridianClassDefaultsEnabled : les plafonds et débits PAR DÉFAUT par classe
// (domain/veridian_class_defaults.go) s'appliquent à tout workspace sans configuration.
// Vrai en production, toujours. Seul le TestMain de ce paquet le coupe : les ~80 tests
// historiques du worker décrivent le chemin « configuration explicite » avec un dépôt
// d'historique de test qui n'implémente pas la réservation atomique, et ne doivent pas
// être réécrits un par un. Les tests du chemin par défaut le rallument explicitement
// (enableVeridianClassDefaults dans veridian_class_defaults_test.go). Ne JAMAIS écrire
// ici dans du code de production.
var veridianClassDefaultsEnabled = true

// veridianCapForClass lit le plafond de classe : surcharge, sinon défaut serveur.
func veridianCapForClass(caps map[string]int, class string) (int, bool) {
	if veridianClassDefaultsEnabled {
		return domain.VeridianCapForClass(caps, class)
	}
	return domain.VeridianConfiguredCapForClass(caps, class)
}

// veridianRateForClass lit le débit de classe : surcharge, sinon défaut serveur.
func veridianRateForClass(rates map[string]float64, class string) (float64, bool) {
	if veridianClassDefaultsEnabled {
		return domain.VeridianRateForClass(rates, class)
	}
	return domain.VeridianConfiguredRateForClass(rates, class)
}
