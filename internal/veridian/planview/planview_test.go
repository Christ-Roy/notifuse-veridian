package planview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

type fakePlans struct {
	plan *domain.VeridianPlan
	err  error
}

func (f fakePlans) Get(context.Context, string) (*domain.VeridianPlan, error) { return f.plan, f.err }

// fakeAuth : membre du seul espace « alpha ».
type fakeAuth struct{}

func (fakeAuth) AuthenticateUserForWorkspace(ctx context.Context, ws string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	if ws != "alpha" {
		return ctx, nil, nil, errors.New("pas membre")
	}
	return ctx, &domain.User{}, &domain.UserWorkspace{}, nil
}

func get(p fakePlans, query string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	(&Handler{Plans: p, Auth: fakeAuth{}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/me/workspace-plan"+query, nil))
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestPlan_MembreLitSonPlan_SansQuotaNiCompteur(t *testing.T) {
	st, out := get(fakePlans{plan: &domain.VeridianPlan{WorkspaceID: "alpha", Plan: "enterprise", PlanSource: "lifetime_partner", Status: "active", MonthlyEmailQuota: 42, EmailsSentThisMonth: 7, SuspendedReason: "secret"}}, "?workspace_id=alpha")
	require.Equal(t, 200, st)
	assert.Equal(t, "enterprise", out["plan"])
	assert.Equal(t, "lifetime_partner", out["plan_source"])
	assert.Equal(t, "active", out["status"])
	assert.Len(t, out, 5, "ni quota, ni compteur, ni raison interne : %v", out)
}

func TestPlan_Refus(t *testing.T) {
	ok := fakePlans{plan: &domain.VeridianPlan{WorkspaceID: "beta", Plan: "pro"}}
	st, _ := get(ok, "")
	assert.Equal(t, 400, st)
	st, out := get(ok, "?workspace_id=beta")
	assert.Equal(t, 403, st, "le plan d'un autre espace ne se lit pas")
	assert.NotContains(t, out, "plan")
	st, _ = get(fakePlans{err: sql.ErrNoRows}, "?workspace_id=alpha")
	assert.Equal(t, 404, st)
	st, _ = get(fakePlans{err: errors.New("panne")}, "?workspace_id=alpha")
	assert.Equal(t, 500, st, "une panne n'est pas « pas de plan »")
}
