package domain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
)

// Veridian fork, lot L3 « politique d'envoi et stock » (29/10/2026).
//
// Les règles d'envoi PAR DÉFAUT de chaque classe de fournisseur destinataire sont des
// DONNÉES (veridian_class_presets.json, embarqué dans le binaire), plus des constantes
// Go : plafond journalier, débit par minute, part de la chauffe, seuil de ralentissement
// du fusible, et ce qu'on a OBSERVÉ (envois, rebonds, réponses humaines) avec sa date et
// sa source. Ce fichier est la SEULE source : les tables historiques
// (veridianDefaultClassDailyCap, veridianDefaultClassRatePerMin) en sont dérivées, le
// worker, EffectivePlan et le module internal/veridian/policy la lisent.
//
// Un workspace neuf reçoit donc ces règles sans rien régler. Une surcharge (par
// workspace, en base, via policy.set ; ou les niveaux historiques profil, broadcast)
// prime toujours. Les valeurs de plafond et de débit sont celles de la fiche 69,
// reprises à l'identique : un test échoue si la donnée et le défaut diffèrent d'une
// classe canonique.

//go:embed veridian_class_presets.json
var veridianClassPresetsJSON []byte

// VeridianObservedEngagement : ce qui a été mesuré pour une classe.
type VeridianObservedEngagement struct {
	Sent         int `json:"sent"`
	Bounced      int `json:"bounced"`
	RepliedHuman int `json:"replied_human"`
}

// VeridianClassPreset : les règles par défaut d'une classe.
type VeridianClassPreset struct {
	Class string `json:"class"`
	Label string `json:"label"`
	// DailyCap : envois max par jour et par (domaine d'envoi x classe). Toujours > 0.
	DailyCap int `json:"daily_cap"`
	// RatePerMin : débit max en mails par minute (fractions permises). Toujours > 0.
	RatePerMin float64 `json:"rate_per_min"`
	// WarmupShare : part (0 à 1] du plafond de chauffe du domaine d'envoi que cette
	// classe peut absorber tant que le domaine chauffe. Ne s'applique pas quand le
	// plafond de la classe est posé à la main (profil, workspace).
	WarmupShare float64 `json:"warmup_share"`
	// SlowdownThreshold : taux de rebonds durs (ou de refus de politique) sur 7 jours
	// à partir duquel le débit de la classe est divisé par 2 (par 4 au double).
	SlowdownThreshold float64 `json:"slowdown_threshold"`
	// Observed : envois, rebonds et réponses humaines mesurés pour la classe.
	Observed VeridianObservedEngagement `json:"observed"`
	// Why : la raison de ces valeurs, en français.
	Why string `json:"why"`
}

// VeridianClassPresetCatalog : le catalogue complet, daté et sourcé.
type VeridianClassPresetCatalog struct {
	Version int    `json:"version"`
	AsOf    string `json:"as_of"`
	Source  string `json:"source"`
	// ReplyPrior : taux de réponse global observé, a priori pour lisser les classes
	// à petit échantillon. ReplySmoothingWeight : poids de cet a priori en envois.
	ReplyPrior           VeridianObservedEngagement `json:"reply_prior"`
	ReplySmoothingWeight int                        `json:"reply_smoothing_weight"`
	Classes              []VeridianClassPreset      `json:"classes"`
}

// VeridianParseClassPresets lit et valide un catalogue. Toute classe canonique doit y
// figurer exactement une fois, avec des valeurs dans les bornes.
func VeridianParseClassPresets(raw []byte) (*VeridianClassPresetCatalog, error) {
	var cat VeridianClassPresetCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("catalogue de presets illisible: %w", err)
	}
	seen := map[string]bool{}
	for _, p := range cat.Classes {
		if !IsValidProviderClass(p.Class) {
			return nil, fmt.Errorf("preset pour une classe inconnue %q", p.Class)
		}
		if seen[p.Class] {
			return nil, fmt.Errorf("preset en double pour %q", p.Class)
		}
		seen[p.Class] = true
		if err := VeridianValidateClassPolicyValues(p.DailyCap, p.RatePerMin, p.WarmupShare, p.SlowdownThreshold); err != nil {
			return nil, fmt.Errorf("preset %q: %w", p.Class, err)
		}
		if p.DailyCap <= 0 || p.RatePerMin <= 0 || p.WarmupShare <= 0 || p.SlowdownThreshold <= 0 {
			return nil, fmt.Errorf("preset %q: plafond, débit, part de chauffe et seuil sont obligatoires", p.Class)
		}
		if strings.TrimSpace(p.Label) == "" || strings.TrimSpace(p.Why) == "" {
			return nil, fmt.Errorf("preset %q: libellé et explication obligatoires", p.Class)
		}
	}
	for _, c := range VeridianAllProviderClasses() {
		if !seen[c] {
			return nil, fmt.Errorf("classe %q sans preset", c)
		}
	}
	if cat.ReplySmoothingWeight <= 0 {
		return nil, fmt.Errorf("reply_smoothing_weight doit être > 0")
	}
	return &cat, nil
}

// Bornes des valeurs de politique (catalogue ET surcharges).
const (
	VeridianPolicyMaxDailyCap = 1000
	VeridianPolicyMinRate     = 0.01
	VeridianPolicyMaxRate     = 10.0
)

// VeridianValidateClassPolicyValues valide des valeurs de politique ; 0 = non posé
// (accepté). Le seuil suit les bornes du fusible par profil.
func VeridianValidateClassPolicyValues(dailyCap int, ratePerMin, warmupShare, slowdownThreshold float64) error {
	if dailyCap < 0 || dailyCap > VeridianPolicyMaxDailyCap {
		return fmt.Errorf("daily_cap doit être entre 1 et %d (reçu %d)", VeridianPolicyMaxDailyCap, dailyCap)
	}
	if bad(ratePerMin) || ratePerMin < 0 || (ratePerMin > 0 && (ratePerMin < VeridianPolicyMinRate || ratePerMin > VeridianPolicyMaxRate)) {
		return fmt.Errorf("rate_per_min doit être entre %v et %v mails par minute (reçu %v)", VeridianPolicyMinRate, VeridianPolicyMaxRate, ratePerMin)
	}
	if bad(warmupShare) || warmupShare < 0 || warmupShare > 1 {
		return fmt.Errorf("warmup_share doit être entre 0,01 et 1 (reçu %v)", warmupShare)
	}
	if warmupShare > 0 && warmupShare < 0.01 {
		return fmt.Errorf("warmup_share doit être entre 0,01 et 1 (reçu %v)", warmupShare)
	}
	if bad(slowdownThreshold) || slowdownThreshold < 0 ||
		(slowdownThreshold > 0 && (slowdownThreshold < VeridianMinHardBounceFreezeThreshold || slowdownThreshold > VeridianMaxHardBounceFreezeThreshold)) {
		return fmt.Errorf("slowdown_threshold doit être entre %.2f et %.2f (reçu %v)",
			VeridianMinHardBounceFreezeThreshold, VeridianMaxHardBounceFreezeThreshold, slowdownThreshold)
	}
	return nil
}

func bad(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }

var veridianClassPresetCatalog = mustParseClassPresets()

func mustParseClassPresets() *VeridianClassPresetCatalog {
	cat, err := VeridianParseClassPresets(veridianClassPresetsJSON)
	if err != nil {
		// Donnée embarquée : une erreur ici est un défaut de build, jamais d'exécution.
		panic("veridian_class_presets.json invalide: " + err.Error())
	}
	return cat
}

// VeridianClassPresets rend le catalogue embarqué (lecture seule, ne pas modifier).
func VeridianClassPresets() *VeridianClassPresetCatalog { return veridianClassPresetCatalog }

// VeridianClassPresetFor rend le preset d'une classe ; ok=false si la classe est inconnue.
func VeridianClassPresetFor(class string) (VeridianClassPreset, bool) {
	for _, p := range veridianClassPresetCatalog.Classes {
		if p.Class == class {
			return p, true
		}
	}
	return VeridianClassPreset{}, false
}

// --- Tables historiques dérivées du catalogue (une seule source) ---

func veridianPresetDailyCaps() map[string]int {
	m := make(map[string]int, len(veridianClassPresetCatalog.Classes))
	for _, p := range veridianClassPresetCatalog.Classes {
		m[p.Class] = p.DailyCap
	}
	return m
}

func veridianPresetRates() map[string]float64 {
	m := make(map[string]float64, len(veridianClassPresetCatalog.Classes))
	for _, p := range veridianClassPresetCatalog.Classes {
		m[p.Class] = p.RatePerMin
	}
	return m
}

// --- Politique effective d'un workspace : crochet entre le worker et le module policy ---

// VeridianClassPolicy : les valeurs EFFECTIVES d'une classe pour un workspace (surcharge
// du workspace, sinon preset). Zéro = non posé.
type VeridianClassPolicy struct {
	DailyCap          int
	RatePerMin        float64
	WarmupShare       float64
	SlowdownThreshold float64
}

// VeridianPolicySnapshot : la politique d'un workspace, immuable (ne pas modifier).
type VeridianPolicySnapshot struct {
	Classes map[string]VeridianClassPolicy
	// Caps et Rates : les mêmes plafonds et débits en tables prêtes à l'emploi pour les
	// portes du worker. Immuables.
	Caps  map[string]int
	Rates map[string]float64
	// Overridden : valeurs posées par le workspace (et non héritées du preset), par classe.
	Overridden map[string]VeridianPolicyOverridden
}

// VeridianPolicyOverridden dit quelles valeurs d'une classe viennent d'une surcharge.
type VeridianPolicyOverridden struct {
	DailyCap, RatePerMin, WarmupShare, SlowdownThreshold bool
}

// Any : au moins une valeur est surchargée.
func (o VeridianPolicyOverridden) Any() bool {
	return o.DailyCap || o.RatePerMin || o.WarmupShare || o.SlowdownThreshold
}

// VeridianPolicyProvider résout la politique d'un workspace. Implémenté par
// internal/veridian/policy, branché par le câblage. Interface étroite, sans E/S côté
// appelant : une implémentation garde ses lectures en cache.
type VeridianPolicyProvider interface {
	PolicyFor(workspaceID string) *VeridianPolicySnapshot
}

type veridianPolicyHolder struct{ p VeridianPolicyProvider }

var veridianPolicyProviderValue atomic.Value // veridianPolicyHolder

// SetVeridianPolicyProvider branche le fournisseur de politique (nil = presets seuls).
func SetVeridianPolicyProvider(p VeridianPolicyProvider) {
	veridianPolicyProviderValue.Store(veridianPolicyHolder{p: p})
}

var veridianPresetSnapshot = buildPresetSnapshot()

func buildPresetSnapshot() *VeridianPolicySnapshot {
	s := &VeridianPolicySnapshot{
		Classes:    make(map[string]VeridianClassPolicy, len(veridianClassPresetCatalog.Classes)),
		Caps:       veridianPresetDailyCaps(),
		Rates:      veridianPresetRates(),
		Overridden: map[string]VeridianPolicyOverridden{},
	}
	for _, p := range veridianClassPresetCatalog.Classes {
		s.Classes[p.Class] = VeridianClassPolicy{DailyCap: p.DailyCap, RatePerMin: p.RatePerMin, WarmupShare: p.WarmupShare, SlowdownThreshold: p.SlowdownThreshold}
	}
	return s
}

// VeridianPresetSnapshot : la politique sans aucune surcharge (workspace neuf).
func VeridianPresetSnapshot() *VeridianPolicySnapshot { return veridianPresetSnapshot }

// VeridianPolicyFor donne la politique effective d'un workspace : celle du fournisseur
// branché, sinon les presets. Ne rend jamais nil.
func VeridianPolicyFor(workspaceID string) *VeridianPolicySnapshot {
	if h, ok := veridianPolicyProviderValue.Load().(veridianPolicyHolder); ok && h.p != nil {
		if s := h.p.PolicyFor(workspaceID); s != nil {
			return s
		}
	}
	return veridianPresetSnapshot
}
