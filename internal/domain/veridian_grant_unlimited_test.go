package domain

// === Veridian patch === Tests colocalises pour veridian_grant_unlimited.go.
// Les types n'ont pas de methodes exportees — ce fichier sert d'invariant
// explicite sur la structure des I/O et le contrat audit GDPR/compta.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrantUnlimitedInput_JSONRoundtrip(t *testing.T) {
	// Verifie que le JSON est exactement ce que le Hub envoie.
	raw := `{"tenant_id":"robertbrunon","reason":"internal","plan_source":"lifetime_partner"}`
	var in GrantUnlimitedInput
	require.NoError(t, json.Unmarshal([]byte(raw), &in))
	assert.Equal(t, "robertbrunon", in.TenantID)
	assert.Equal(t, "internal", in.Reason)
	assert.Equal(t, PlanSourceLifetimePartner, in.PlanSource)
}

func TestGrantUnlimitedInput_OmitemptyPlanSource(t *testing.T) {
	// Plan source optionnel : omitempty doit produire un JSON sans le champ
	// quand vide. Permet au Hub de ne pas envoyer le champ pour prendre le
	// defaut serveur (lifetime_partner).
	in := GrantUnlimitedInput{TenantID: "ws-1", Reason: "test"}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "plan_source", "omitempty devrait elider le champ")
}

func TestGrantUnlimitedResponse_AllFieldsExposed(t *testing.T) {
	// Tous les champs doivent etre serialises pour audit complet cote Hub.
	resp := GrantUnlimitedResponse{
		TenantID:     "ws-1",
		Plan:         "enterprise",
		PreviousPlan: "free",
		PlanSource:   PlanSourceLifetimePartner,
		Quota:        -1,
		GrantedAt:    time.Now().UTC(),
		Reason:       "audit_compensation",
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	// Verifie les 7 champs serialises pour audit GDPR/compta.
	s := string(b)
	for _, expectedField := range []string{
		`"tenant_id"`,
		`"plan"`,
		`"previous_plan"`,
		`"plan_source"`,
		`"quota"`,
		`"granted_at"`,
		`"reason"`,
	} {
		assert.Contains(t, s, expectedField, "audit field %s manque", expectedField)
	}
}

func TestGrantUnlimitedResponse_QuotaMinusOneIsUnlimited(t *testing.T) {
	// Quota=-1 est la valeur conventionnelle pour "illimite" (cf.
	// VeridianPlan.QuotaRemaining et IsBlocked dans veridian.go). Verifie
	// que la reponse persiste bien -1 et pas 0.
	resp := GrantUnlimitedResponse{Quota: -1}
	b, _ := json.Marshal(resp)
	assert.Contains(t, string(b), `"quota":-1`)
}
