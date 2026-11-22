// Package testpg dit où trouver la base PostgreSQL jetable des tests Veridian, et ce qu'il faut
// faire sans elle. Sur un poste : le test est ignoré. En CI (VERIDIAN_TEST_REQUIRE_PG=1) : il
// échoue. Avant le 22/11/2026, chaque module lisait sa propre variable (L1_TEST_PG_DSN,
// VERIDIAN_POLICY_TEST_DSN...) et la CI n'en posait aucune : les tests PostgreSQL de la boîte
// unifiée et de la politique d'envoi étaient ignorés partout, en silence.
package testpg

import (
	"os"
	"testing"
)

// Common : la variable commune, lue en dernier recours par tous les modules.
const Common = "VERIDIAN_TEST_POSTGRES_DSN"

// Require : posée à 1, une base absente fait échouer le test au lieu de l'ignorer.
const Require = "VERIDIAN_TEST_REQUIRE_PG"

// Resolve rend la première variable posée parmi names puis Common, et son nom ("" si aucune).
func Resolve(names ...string) (dsn, from string) {
	for _, n := range append(append([]string{}, names...), Common) {
		if v := os.Getenv(n); v != "" {
			return v, n
		}
	}
	return "", ""
}

// DSN rend la base jetable, sinon ignore le test (poste) ou le fait échouer (CI).
func DSN(t testing.TB, names ...string) string {
	t.Helper()
	dsn, _ := Resolve(names...)
	if dsn != "" {
		return dsn
	}
	all := append(append([]string{}, names...), Common)
	if os.Getenv(Require) == "1" {
		t.Fatalf("base PostgreSQL de test requise (%s=1) mais aucune de %v n'est posée", Require, all)
	}
	t.Skipf("aucune de %v n'est posée : test PostgreSQL non joué", all)
	return ""
}
