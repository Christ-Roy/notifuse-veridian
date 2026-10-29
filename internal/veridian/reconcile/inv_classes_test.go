package reconcile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cas du 29/10 : 1 218 contacts classés freemail_fr alors que leur domaine est orange,
// laposte, free, sfr ou bouygues.
func fixtureFreemail1218() []classRow {
	return []classRow{
		{Domain: "orange.fr", Tag: "freemail_fr", Count: 700, Sample: []string{"a@orange.fr", "b@orange.fr"}},
		{Domain: "wanadoo.fr", Tag: "freemail_fr", Count: 100, Sample: []string{"c@wanadoo.fr"}},
		{Domain: "laposte.net", Tag: "freemail_fr", Count: 200},
		{Domain: "free.fr", Tag: "freemail_fr", Count: 120},
		{Domain: "sfr.fr", Tag: "freemail_fr", Count: 70},
		{Domain: "bbox.fr", Tag: "freemail_fr", Count: 28},
		// contacts sains
		{Domain: "orange.fr", Tag: "orange", Count: 1485},
		{Domain: "gmail.com", Tag: "google", Count: 4993},
	}
}

func TestJudgeClasses_Freemail1218(t *testing.T) {
	fs := judgeClasses(fixtureFreemail1218())
	total := 0
	byTo := map[string]Finding{}
	for _, f := range fs {
		total += int(f.Evidence["count"].(int))
		byTo[f.Evidence["to"].(string)] = f
	}
	assert.Equal(t, 1218, total, "les 1 218 contacts sont tous rattachés à un écart")
	require.Len(t, fs, 5, "un écart par fournisseur attendu")
	for _, to := range []string{"orange", "laposte", "free", "sfr_altice", "bouygues"} {
		f, ok := byTo[to]
		require.True(t, ok, to)
		assert.Equal(t, SeverityWarning, f.Severity)
		assert.Equal(t, "classe:freemail_fr->"+to, f.SubjectRef)
		require.NotNil(t, f.ProposedAction)
		assert.Equal(t, ActionReclassify, f.ProposedAction.Kind)
		assert.Equal(t, map[string]any{"from": "freemail_fr", "to": to}, f.ProposedAction.Params)
		assert.Contains(t, f.Explanation, "scindée le 22/10")
		assert.Contains(t, f.Title, "contacts classés")
	}
	assert.Equal(t, 800, byTo["orange"].Evidence["count"], "orange.fr + wanadoo.fr")
}

func TestJudgeClasses_CleanDataHasNoFinding(t *testing.T) {
	rows := []classRow{
		{Domain: "orange.fr", Tag: "orange", Count: 10},
		{Domain: "gmail.com", Tag: "google", Count: 10},
		{Domain: "hotmail.fr", Tag: "microsoft", Count: 10},
		{Domain: "gmx.fr", Tag: "other_hoster", Count: 9},      // résidu freemail_fr : non jugé
		{Domain: "dartybox.com", Tag: "freemail_fr", Count: 1}, // idem
	}
	assert.Empty(t, judgeClasses(rows))
}

func TestJudgeClasses_OtherMismatches(t *testing.T) {
	fs := judgeClasses([]classRow{
		{Domain: "gmail.com", Tag: "ovh", Count: 1, Sample: []string{"x@gmail.com"}},
		{Domain: "gmail.com", Tag: "", Count: 297},
	})
	require.Len(t, fs, 2)
	var ovh, empty Finding
	for _, f := range fs {
		if f.Evidence["from"] == "ovh" {
			ovh = f
		} else {
			empty = f
		}
	}
	assert.Equal(t, SeverityWarning, ovh.Severity)
	assert.Contains(t, ovh.Title, "1 contact classé")
	assert.Equal(t, SeverityInfo, empty.Severity, "non classé : le worker retrouve la classe")
	assert.Equal(t, "classe:vide->google", empty.SubjectRef)
	assert.Contains(t, empty.Title, "aucune classe")
	assert.Contains(t, empty.Title, "297")
}

func TestExpectedClassFor(t *testing.T) {
	assert.Equal(t, "orange", expectedClassFor("wanadoo.fr"))
	assert.Equal(t, "google", expectedClassFor("gmail.com"))
	assert.Equal(t, "", expectedClassFor("cabinet-dupont.fr"), "domaine propre : classé par son MX, pas jugé")
	assert.Equal(t, "", expectedClassFor("gmx.fr"))
}

func TestClassKnownDomains(t *testing.T) {
	ds := classKnownDomains()
	assert.Contains(t, ds, "orange.fr")
	assert.Contains(t, ds, "gmail.com")
	assert.NotContains(t, ds, "gmx.fr")
	assert.NotContains(t, ds, "dartybox.com")
	assert.True(t, strings.Compare(ds[0], ds[len(ds)-1]) < 0, "trié, déterministe")
}
