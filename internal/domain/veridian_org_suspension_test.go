package domain

import (
	"testing"
	"time"
)

func TestVeridianOrganisationSuspended(t *testing.T) {
	now := time.Now()
	cas := []struct {
		nom  string
		p    *VeridianPlan
		veut bool
	}{
		{"organisation suspendue", &VeridianPlan{Status: PlanStatusSuspended, SuspendedReason: "admin_action"}, true},
		{"facturation impayée", &VeridianPlan{Status: PlanStatusSuspended, SuspendedReason: "billing_past_due"}, false},
		{"suspendu sans motif", &VeridianPlan{Status: PlanStatusSuspended}, false},
		{"actif", &VeridianPlan{Status: PlanStatusActive, SuspendedReason: "admin_action"}, false},
		{"supprimé (son propre filtre)", &VeridianPlan{Status: PlanStatusSuspended, SuspendedReason: "admin_action", DeletedAt: &now}, false},
		{"absent", nil, false},
	}
	for _, c := range cas {
		if got := c.p.VeridianOrganisationSuspended(); got != c.veut {
			t.Errorf("%s : %v, attendu %v", c.nom, got, c.veut)
		}
	}
}
