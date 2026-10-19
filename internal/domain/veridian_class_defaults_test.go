package domain

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Aucune classe canonique ne peut être sans plafond ni sans débit par défaut.
func TestVeridianClassDefaults_CoverEveryCanonicalClass(t *testing.T) {
	for _, c := range VeridianAllProviderClasses() {
		cp, ok := veridianDefaultClassDailyCap[c]
		assert.True(t, ok && cp > 0, "classe %q sans plafond journalier par défaut", c)
		rt, ok := veridianDefaultClassRatePerMin[c]
		assert.True(t, ok && rt > 0, "classe %q sans débit par défaut", c)
	}
	// Pas de ligne orpheline : toute clé des tables est une classe canonique.
	for c := range veridianDefaultClassDailyCap {
		assert.True(t, IsValidProviderClass(c), "plafond par défaut pour une classe inconnue %q", c)
	}
	for c := range veridianDefaultClassRatePerMin {
		assert.True(t, IsValidProviderClass(c), "débit par défaut pour une classe inconnue %q", c)
	}
	// Une classe future inconnue reçoit quand même un frein.
	assert.Greater(t, VeridianDefaultCapForClass("classe_future"), 0)
	assert.Greater(t, VeridianDefaultRateForClass("classe_future"), 0.0)
}

// Les surcharges priment ; sans surcharge (nil/vide/0) le défaut s'applique à TOUTES les classes.
func TestVeridianClassDefaults_AppliedWithoutAnyConfig(t *testing.T) {
	for _, c := range VeridianAllProviderClasses() {
		capV, ok := VeridianCapForClass(nil, c)
		assert.True(t, ok, c)
		assert.Equal(t, VeridianDefaultCapForClass(c), capV, c)
		rate, ok := VeridianRateForClass(map[string]float64{}, c)
		assert.True(t, ok, c)
		assert.InDelta(t, VeridianDefaultRateForClass(c), rate, 1e-9, c)
	}
	capV, _ := VeridianCapForClass(map[string]int{ProviderClassOVH: 7}, ProviderClassOVH)
	assert.Equal(t, 7, capV, "la surcharge prime")
}

// Classe taguée non canonique (ex. « godaddy », « Infomaniak ! ») : ignorée à l'import
// (tag vide), le MX décide à l'envoi. Une casse/espaces corrigeables sont normalisés.
func TestVeridianContactProviderClass_NonCanonicalIgnoredCanonicalNormalized(t *testing.T) {
	mk := func(s string) *Contact {
		return &Contact{Email: "a@b.fr", CustomString5: &NullableString{String: s, IsNull: false}}
	}
	assert.Equal(t, "", VeridianContactProviderClass(mk("godaddy")))
	assert.Equal(t, "", VeridianContactProviderClass(mk("Infomaniak !")))
	assert.Equal(t, ProviderClassInfomaniak, VeridianContactProviderClass(mk("  Infomaniak ")))
	assert.Equal(t, ProviderClassOVH, VeridianContactProviderClass(mk("OVH")))
}

type errResolver struct{ err error }

func (r *errResolver) LookupMXHosts(_ context.Context, _ string) ([]string, error) {
	return nil, r.err
}

// Un échec DNS transitoire ne fige PAS corporate_selfhost 7 jours : re-résolu après 5 min.
func TestMXClassifier_TransientFailureNotCachedLong(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	res := &flipResolver{err: errors.New("i/o timeout")}
	c := NewVeridianMXClassifier(res)
	c.now = func() time.Time { return now }

	assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyDomain(context.Background(), "cabinet-x.fr"))
	assert.Equal(t, int64(1), c.TransientFailures())

	// Le resolver se rétablit : 6 minutes plus tard le domaine est reclassé Google.
	res.err = nil
	res.hosts = []string{"aspmx.l.google.com"}
	now = now.Add(6 * time.Minute)
	assert.Equal(t, ProviderClassGoogle, c.ClassifyDomain(context.Background(), "cabinet-x.fr"))
}

// NXDOMAIN, lui, est définitif (TTL long) et ne compte pas comme échec transitoire.
func TestMXClassifier_NXDOMAINIsDefinitive(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	res := &flipResolver{err: &net.DNSError{Err: "no such host", IsNotFound: true}}
	c := NewVeridianMXClassifier(res)
	c.now = func() time.Time { return now }
	assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyDomain(context.Background(), "inexistant-zz.fr"))
	assert.Equal(t, int64(0), c.TransientFailures())
	res.err = nil
	res.hosts = []string{"aspmx.l.google.com"}
	now = now.Add(6 * time.Minute)
	assert.Equal(t, ProviderClassCorporateSelfhost, c.ClassifyDomain(context.Background(), "inexistant-zz.fr"), "toujours en cache (7 j)")
}

type flipResolver struct {
	err   error
	hosts []string
}

func (r *flipResolver) LookupMXHosts(_ context.Context, _ string) ([]string, error) {
	return r.hosts, r.err
}

// Table MX complète exportée : fichier testdata/veridian_mx_table.json, GÉNÉRÉ depuis
// veridianMXPatternTable (source unique du vocabulaire). ODH le lit/le copie (cf. fiche 69) ;
// ce test échoue si la table Go a bougé sans régénérer le fichier
// (UPDATE_MX_TABLE=1 go test ./internal/domain -run TestMXTableExport).
func TestMXTableExport(t *testing.T) {
	type row struct {
		Suffix string `json:"suffix"`
		Class  string `json:"class"`
	}
	rows := make([]row, 0, len(veridianMXPatternTable))
	for _, e := range veridianMXPatternTable {
		require.True(t, IsValidProviderClass(e.class), "classe non canonique dans la table MX: %s", e.class)
		rows = append(rows, row{Suffix: e.suffix, Class: e.class})
	}
	doc := map[string]interface{}{
		"_doc":    "Table MX -> classe d'envoi, ORDRE SIGNIFICATIF (premier suffixe qui matche gagne). Générée depuis internal/domain/veridian_provider_class_mx.go ; ne pas éditer à la main (UPDATE_MX_TABLE=1 go test ./internal/domain -run TestMXTableExport).",
		"classes": VeridianAllProviderClasses(),
		"rules":   rows,
	}
	want, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	want = append(want, '\n')
	const path = "testdata/veridian_mx_table.json"
	if os.Getenv("UPDATE_MX_TABLE") == "1" {
		require.NoError(t, os.WriteFile(path, want, 0o644))
		return
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err, "fichier absent : UPDATE_MX_TABLE=1 go test ./internal/domain -run TestMXTableExport")
	assert.Equal(t, string(want), string(got), "table MX modifiée sans régénérer testdata/veridian_mx_table.json (UPDATE_MX_TABLE=1)")

	// Contrôle de cohérence : chaque règle, appliquée à un hôte synthétique, retombe sur sa
	// classe, SAUF si une règle antérieure l'emporte légitimement (ordre) ; dans ce cas la
	// règle antérieure doit être plus spécifique ou une passerelle.
	var shadowed []string
	for i, r := range rows {
		got, ok := classifyMXHost("mx1" + r.Suffix)
		require.True(t, ok, r.Suffix)
		if got != r.Class {
			shadowed = append(shadowed, r.Suffix+"->"+got+" (index "+string(rune('0'+i%10))+")")
		}
	}
	sort.Strings(shadowed)
	assert.Empty(t, shadowed, "règles masquées par une règle précédente : %s", strings.Join(shadowed, ", "))
}
