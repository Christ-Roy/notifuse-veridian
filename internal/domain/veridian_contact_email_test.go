package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cas réels relevés en prod le 22/11/2026 (VER-220) : 51 adresses du workspace greenbeamcraft
// (export Prospection du 30/10, extraction de page qui colle la fin d'un téléphone, un saut de
// ligne et « email: ») et 2 de robertbrunon (import CSV du 06/10, espace encodée en tête).
func TestVeridianCanonicalContactEmail_RealDefects(t *testing.T) {
	for in, want := range map[string]string{
		"06%0d%0aemail%3ahotel.des.lacs@netcourrier.com": "hotel.des.lacs@netcourrier.com",
		"33%0D%0Aemail%3Acontact@hotel-poste-63.com":     "contact@hotel-poste-63.com",
		"%20legi@legiavocats.eu":                         "legi@legiavocats.eu",
		"%20contact@groupe-vallier.com":                  "contact@groupe-vallier.com",
		"  Contact@Example.FR\u00a0":                     "contact@example.fr",
		"\u200bcontact@example.fr\xef\xbb\xbf":           "contact@example.fr",
		"mailto:contact@example.fr":                      "contact@example.fr",
		"E-mail : contact@example.fr":                    "contact@example.fr",
		"06 12 34 56 78\r\nemail:contact@example.fr":     "contact@example.fr",
		"contact@example.fr":                             "contact@example.fr",
		"prenom.nom+tag@sous.domaine.example.fr":         "prenom.nom+tag@sous.domaine.example.fr",
	} {
		got, err := VeridianCanonicalContactEmail(in)
		require.NoError(t, err, "%q", in)
		assert.Equal(t, want, got, "%q", in)
	}
}

func TestVeridianCanonicalContactEmail_Rejected(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"a b@example.fr",                   // espace interne : quelle adresse ?
		"a%20b@example.fr",                 // caractère encodé au milieu
		"100%25@example.fr",                // double encodage
		"un@example.fr\r\ndeux@example.fr", // deux adresses : on ne choisit pas
		"tel:0612345678",                   // pas d'adresse
		"contact@example.fr\x00",           // caractère de contrôle
		"contact@@example.fr",              // arobase double
		"contact@example",                  // domaine sans point
		"%E9t%E9@example.fr",               // encodage invalide en UTF-8
		"cont\u200bact@example.fr",         // invisible au milieu
	} {
		got, err := VeridianCanonicalContactEmail(in)
		assert.Error(t, err, "%q devait être refusée, rendue %q", in, got)
	}
}

// La porte : Contact.Validate (API, import CSV, synchro Prospection) normalise ou refuse.
func TestContactValidate_UsesCanonicalEmail(t *testing.T) {
	c := &Contact{Email: "06%0d%0aemail%3ahotel.des.lacs@netcourrier.com"}
	require.NoError(t, c.Validate())
	assert.Equal(t, "hotel.des.lacs@netcourrier.com", c.Email, "l'adresse rangée est l'adresse réelle")

	bad := &Contact{Email: "a%20b@example.fr"}
	assert.Error(t, bad.Validate(), "une adresse ambiguë n'entre pas")
}
