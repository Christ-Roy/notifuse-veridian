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
var veridianDefaultClassDailyCap = map[string]int{
	ProviderClassGoogle:            30,
	ProviderClassMicrosoft:         30,
	ProviderClassYahooAol:          10,
	ProviderClassFreemailFR:        20,
	ProviderClassCorporate:         30,
	ProviderClassOVH:               40,
	ProviderClassIonos:             20,
	ProviderClassAppleICloud:       10,
	ProviderClassSecurityGateway:   10,
	ProviderClassOtherHoster:       30,
	ProviderClassCorporateSelfhost: 30,
	ProviderClassInfomaniak:        25,
	ProviderClassGandi:             25,
	ProviderClassHostinger:         25,
	ProviderClassO2switch:          25,
	ProviderClassLWS:               25,
	ProviderClassScaleway:          25,
	ProviderClassWebsiteBuilder:    20,
	ProviderClassZoho:              20,
	ProviderClassProton:            10,
}

// veridianDefaultClassRatePerMin : débit max (emails / minute) par (intégration x classe).
var veridianDefaultClassRatePerMin = map[string]float64{
	ProviderClassGoogle:            0.3,
	ProviderClassMicrosoft:         0.3,
	ProviderClassYahooAol:          0.1,
	ProviderClassFreemailFR:        0.2,
	ProviderClassCorporate:         0.3,
	ProviderClassOVH:               0.4,
	ProviderClassIonos:             0.2,
	ProviderClassAppleICloud:       0.1,
	ProviderClassSecurityGateway:   0.1,
	ProviderClassOtherHoster:       0.3,
	ProviderClassCorporateSelfhost: 0.3,
	ProviderClassInfomaniak:        0.25,
	ProviderClassGandi:             0.25,
	ProviderClassHostinger:         0.25,
	ProviderClassO2switch:          0.25,
	ProviderClassLWS:               0.25,
	ProviderClassScaleway:          0.25,
	ProviderClassWebsiteBuilder:    0.2,
	ProviderClassZoho:              0.2,
	ProviderClassProton:            0.1,
}

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
