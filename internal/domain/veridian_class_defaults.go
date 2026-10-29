package domain

// Veridian fork (19/10/2026) — plafonds et débits PAR DÉFAUT de chaque classe de
// fournisseur destinataire. Décision Robert : Notifuse sert d'outil de prédilection
// pour charger les segments complets d'ODH ; chaque classe doit donc être bridée
// dès la création d'un workspace, sans réglage. Avant, une classe sans valeur
// configurée n'avait AUCUN frein (no-op strict) : un workspace neuf ou un profil
// créé avant l'existence d'une classe envoyait à pleine vitesse.
//
// Ces valeurs sont par COUPLE (domaine émetteur, classe), comme les surcharges.
// Elles sont volontairement prudentes (régime de chauffe élargi) : un workspace qui
// veut davantage pose sa propre table (profil, workspace ou broadcast), qui prime.
// Valeur par défaut pour une classe future inconnue : celle de corporate_selfhost.
//
// Cette table est la SEULE source : EffectivePlan, la porte de débit, la porte de
// plafond et la réservation atomique la lisent via VeridianCapForClass /
// VeridianRateForClass. Le contrôle veridian_class_defaults_test.go échoue si une
// classe canonique n'a pas de valeur.

// veridianDefaultClassDailyCap : envois max / jour / (domaine émetteur x classe).
// 29/10 (lot L3) : dérivé du catalogue de presets (veridian_class_presets.json), qui est
// désormais la seule source des valeurs. Les valeurs de la fiche 69 y sont reprises à
// l'identique (veridian_class_presets_test.go les fige).
var veridianDefaultClassDailyCap = veridianPresetDailyCaps()

// veridianDefaultClassRatePerMin : débit max (emails / minute) par (intégration x classe).
var veridianDefaultClassRatePerMin = veridianPresetRates()

// VeridianDefaultCapForClass : plafond journalier par défaut (toujours > 0).
func VeridianDefaultCapForClass(class string) int {
	if v, ok := veridianDefaultClassDailyCap[class]; ok && v > 0 {
		return v
	}
	return veridianDefaultClassDailyCap[ProviderClassCorporateSelfhost]
}

// VeridianDefaultRateForClass : débit par défaut en emails/minute (toujours > 0).
func VeridianDefaultRateForClass(class string) float64 {
	if v, ok := veridianDefaultClassRatePerMin[class]; ok && v > 0 {
		return v
	}
	return veridianDefaultClassRatePerMin[ProviderClassCorporateSelfhost]
}
