package testpg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve_OwnVariableThenCommon(t *testing.T) {
	t.Setenv("L1_TEST_PG_DSN", "")
	t.Setenv(Common, "")
	dsn, from := Resolve("L1_TEST_PG_DSN")
	assert.Empty(t, dsn)
	assert.Empty(t, from)

	t.Setenv(Common, "postgres://commune")
	dsn, from = Resolve("L1_TEST_PG_DSN")
	assert.Equal(t, "postgres://commune", dsn, "la variable commune sert de dernier recours")
	assert.Equal(t, Common, from)

	t.Setenv("L1_TEST_PG_DSN", "postgres://propre")
	dsn, from = Resolve("L1_TEST_PG_DSN")
	assert.Equal(t, "postgres://propre", dsn, "la variable propre au module passe d'abord")
	assert.Equal(t, "L1_TEST_PG_DSN", from)
}

// fakeTB enregistre Fatal et Skip sans arrêter le test qui l'appelle.
type fakeTB struct {
	testing.TB
	fatal, skip bool
}

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Fatalf(string, ...any) { f.fatal = true }
func (f *fakeTB) Skipf(string, ...any)  { f.skip = true }

func TestDSN_MissingSkipsLocallyFailsWhenRequired(t *testing.T) {
	t.Setenv(Common, "")
	t.Setenv(Require, "")
	f := &fakeTB{TB: t}
	DSN(f)
	assert.True(t, f.skip)
	assert.False(t, f.fatal)

	t.Setenv(Require, "1")
	f = &fakeTB{TB: t}
	DSN(f)
	assert.True(t, f.fatal, "en CI, une base absente est un échec, jamais un test ignoré")

	t.Setenv(Common, "postgres://x")
	f = &fakeTB{TB: t}
	assert.Equal(t, "postgres://x", DSN(f))
	assert.False(t, f.fatal || f.skip)
}
