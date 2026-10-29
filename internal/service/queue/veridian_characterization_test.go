package queue

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

// Tests de caractérisation du gate de réputation et de la capacité d'un profil (lot L4, 29/10/2026),
// écrits depuis les mutants qui survivaient : seuil nul, égalité de taux, bornes de l'arrêt en bloc,
// plafond nul. Ce gate décide quand un domaine d'envoi ralentit ou s'arrête : une borne décalée
// ralentit trop tard (réputation) ou gèle un domaine sain (volume perdu).

func counts(sent, hard, policy int) domain.VeridianReputationCounts {
	return domain.VeridianReputationCounts{Sent: sent, HardBounces: hard, PolicyRefusals: policy}
}

func TestVeridianChar_Slowdown_SeuilNulDesactiveLeRalentissement(t *testing.T) {
	// Seuil 0 (ou négatif) = surveillance coupée : jamais de ralentissement, même à 50 % de rebonds.
	for _, th := range []float64{0, -0.5} {
		f, reason := veridianClassSlowdown(counts(100, 50, 0), th)
		assert.Equal(t, 1, f, "seuil %v", th)
		assert.Equal(t, "", reason)
	}
}

func TestVeridianChar_Slowdown_BornesDuTauxEtDuVolumeMinimal(t *testing.T) {
	const th = 0.05
	cases := []struct {
		name         string
		c            domain.VeridianReputationCounts
		wantFactor   int
		wantReason   string
	}{
		{"19 envois : trop peu pour réagir", counts(19, 19, 0), 1, ""},
		{"20 envois, 1 rebond (5 % = seuil) : ÷2", counts(20, 1, 0), 2, "hard_bounce_rate"},
		{"20 envois, 0 rebond", counts(20, 0, 0), 1, ""},
		{"100 envois, 4 rebonds (4 %) : sous le seuil", counts(100, 4, 0), 1, ""},
		{"100 envois, 5 rebonds (5 %) : ÷2", counts(100, 5, 0), 2, "hard_bounce_rate"},
		{"100 envois, 9 rebonds (9 %) : encore ÷2", counts(100, 9, 0), 2, "hard_bounce_rate"},
		{"100 envois, 10 rebonds (2 x le seuil) : ÷4", counts(100, 10, 0), 4, "hard_bounce_rate"},
		{"refus de politique seuls à 12 %", counts(100, 0, 12), 4, "policy_refusal_rate"},
		{"taux égaux : le motif reste le rebond dur", counts(100, 6, 6), 2, "hard_bounce_rate"},
		{"refus de politique plus fréquents que les rebonds", counts(100, 5, 6), 2, "policy_refusal_rate"},
	}
	for _, c := range cases {
		f, reason := veridianClassSlowdown(c.c, th)
		assert.Equal(t, c.wantFactor, f, c.name)
		assert.Equal(t, c.wantReason, reason, c.name)
	}
}

func TestVeridianChar_ArretEnBloc_Bornes(t *testing.T) {
	assert.False(t, veridianBulkRefusal(19, 19), "moins de 20 envois récents : jamais d'arrêt")
	assert.False(t, veridianBulkRefusal(20, 10), "exactement 50 % : pas d'arrêt (strictement plus de la moitié)")
	assert.True(t, veridianBulkRefusal(20, 11), "11 refus sur 20 : arrêt")
	assert.True(t, veridianBulkRefusal(40, 21))
	assert.False(t, veridianBulkRefusal(40, 20))

	assert.False(t, veridianNeedsBulkCheck(counts(19, 0, 19)), "moins de 20 envois sur 7 jours")
	assert.False(t, veridianNeedsBulkCheck(counts(20, 0, 10)), "10 refus ne peuvent pas dépasser 50 % de 20")
	assert.True(t, veridianNeedsBulkCheck(counts(20, 0, 11)), "20 envois et 11 refus : à vérifier sur les 20 derniers")
}

func TestVeridianChar_EvaluationDuCouple(t *testing.T) {
	healthy := veridianEvaluateCouple(0, counts(100, 0, 0), 0, 0, 0.05)
	assert.Equal(t, 1, healthy.Factor)
	assert.False(t, healthy.Stopped)
	assert.Equal(t, "", healthy.Reason)

	// Une plainte ralentit tout le domaine à ÷4, même sans rebond.
	complaint := veridianEvaluateCouple(1, counts(100, 0, 0), 0, 0, 0.05)
	assert.Equal(t, 4, complaint.Factor)
	assert.Equal(t, "complaint", complaint.Reason)
	// Un ralentissement déjà à ÷4 garde son motif (la plainte ne le remplace pas).
	strong := veridianEvaluateCouple(1, counts(100, 10, 0), 0, 0, 0.05)
	assert.Equal(t, 4, strong.Factor)
	assert.Equal(t, "hard_bounce_rate", strong.Reason)
	// Plainte + ÷2 : la plainte l'emporte (÷4).
	mild := veridianEvaluateCouple(1, counts(100, 5, 0), 0, 0, 0.05)
	assert.Equal(t, 4, mild.Factor)
	assert.Equal(t, "complaint", mild.Reason)

	// Arrêt en bloc : le motif devient bulk_policy_refusal.
	stopped := veridianEvaluateCouple(0, counts(100, 0, 60), 20, 12, 0.05)
	assert.True(t, stopped.Stopped)
	assert.Equal(t, "bulk_policy_refusal", stopped.Reason)
}

func TestVeridianChar_PlafondRalenti(t *testing.T) {
	cases := []struct{ cap, factor, want int }{
		{0, 4, 0},    // 0 = pas de plafond : jamais transformé en plafond de 1
		{-1, 4, -1},  // valeur négative laissée telle quelle
		{100, 1, 100}, // facteur 1 = débit normal
		{100, 0, 100},
		{100, 2, 50},
		{10, 4, 3}, // arrondi au supérieur
		{1, 4, 1},  // plancher à 1 : un plafond positif ne devient jamais 0
		{3, 4, 1},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, veridianSlowCap(c.cap, c.factor), "cap=%d facteur=%d", c.cap, c.factor)
	}
}

func intPtr(v int) *int { return &v }

func TestVeridianChar_Capacite_LimiteQuiBorneLaSomme(t *testing.T) {
	classes := []domain.VeridianPlanClass{{Class: "google"}, {Class: "ovh"}, {Class: "orange"}}
	rate, capL := domain.VeridianCapacityLimiterRate, domain.VeridianCapacityLimiterClassCap

	// Tout est bloqué (aucune classe n'a de capacité positive) : classes bloquées.
	l, c := veridianClassesLimit([]*int{intPtr(0), nil, intPtr(0)}, []string{rate, rate, capL}, classes)
	assert.Equal(t, domain.VeridianCapacityLimitBlocked, l)
	assert.Equal(t, "", c)

	// Les débits pèsent autant que les plafonds : ce sont les débits qui bornent.
	l, c = veridianClassesLimit([]*int{intPtr(30), intPtr(30)}, []string{rate, capL}, classes[:2])
	assert.Equal(t, domain.VeridianCapacityLimitClassRates, l)
	assert.Equal(t, "google", c, "la classe la plus lourde (la première à égalité)")

	// Les plafonds pèsent plus : ce sont eux.
	l, c = veridianClassesLimit([]*int{intPtr(10), intPtr(50)}, []string{rate, capL}, classes[:2])
	assert.Equal(t, domain.VeridianCapacityLimitClassCaps, l)
	assert.Equal(t, "ovh", c)

	// Aucune limite chiffrée (valeurs sans limite) mais au moins une classe positive : aucune limite de classe.
	l, c = veridianClassesLimit([]*int{intPtr(5), nil}, []string{"autre", rate}, classes[:2])
	assert.Equal(t, domain.VeridianCapacityLimitNone, l)
	assert.Equal(t, "google", c)
}

func TestVeridianChar_Stock_ResteRepartiSansPerteNiExces(t *testing.T) {
	// 5 profils bloqués aujourd'hui, 7 contacts : 2,2,1,1,1 (la somme est le stock, jamais plus).
	zero := 0
	absorb := map[string]map[string]*int{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		absorb[id] = map[string]*int{"ovh": &zero}
	}
	shares := VeridianAllocateStock(map[string]int{"ovh": 7}, absorb)
	sum := 0
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		sum += shares[id]["ovh"]
	}
	assert.Equal(t, 7, sum)
	assert.Equal(t, 2, shares["a"]["ovh"])
	assert.Equal(t, 2, shares["b"]["ovh"])
	assert.Equal(t, 1, shares["c"]["ovh"])
}
