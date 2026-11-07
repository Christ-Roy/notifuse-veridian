package hubusers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecret = "hub-secret-test"
	hubID      = "11111111-2222-3333-4444-555555555555"
)

type fakeStore struct {
	humans     []Account
	found      *Account
	findCalls  []string
	updated    []string
	gotName    *string
	gotEmail   *string
	updateErr  error
	revoked    []string
	revokedErr error
}

func (f *fakeStore) ListHumans(context.Context) ([]Account, error) { return f.humans, nil }
func (f *fakeStore) FindByHubUserID(_ context.Context, id string) (*Account, error) {
	f.findCalls = append(f.findCalls, id)
	if f.found == nil || f.found.HubUserID != id {
		return nil, ErrNotFound
	}
	return f.found, nil
}
func (f *fakeStore) UpdateIdentity(_ context.Context, userID string, name, email *string) error {
	f.updated = append(f.updated, userID)
	f.gotName, f.gotEmail = name, email
	return f.updateErr
}
func (f *fakeStore) Revoke(_ context.Context, userID string) error {
	f.revoked = append(f.revoked, userID)
	return f.revokedErr
}

func mux(f *fakeStore) *http.ServeMux {
	m := http.NewServeMux()
	Mount(m, &Handler{Store: f}, testSecret)
	return m
}

func signed(method, path, body string, signature string) *http.Request {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	if signature == "" {
		mac := hmac.New(sha256.New, []byte(testSecret))
		mac.Write([]byte(ts + "." + body))
		signature = hex.EncodeToString(mac.Sum(nil))
	}
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Hub-Signature", signature)
	return r
}

func do(m *http.ServeMux, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, r)
	return rec
}

func TestRoutes_RefuseSansSignatureValide(t *testing.T) {
	f := &fakeStore{found: &Account{ID: "u1", HubUserID: hubID}}
	m := mux(f)

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/hub/users", ""},
		{"POST", "/api/hub/users/sync", `{"hub_user_id":"` + hubID + `","deleted":true}`},
	} {
		// aucune signature
		rec := do(m, httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body)))
		assert.Equal(t, http.StatusUnauthorized, rec.Code, tc.path)
		// mauvaise signature
		rec = do(m, signed(tc.method, tc.path, tc.body, "deadbeef"))
		assert.Equal(t, http.StatusUnauthorized, rec.Code, tc.path)
	}
	assert.Empty(t, f.findCalls, "rien n'est lu sans signature")
	assert.Empty(t, f.revoked, "aucune coupure sans signature")
}

func TestList_HumainsSeulement_SansIdLocal(t *testing.T) {
	f := &fakeStore{humans: []Account{
		{ID: "internal-1", HubUserID: hubID, Email: "a@x.test", Name: "Alice"},
		{ID: "internal-2", Email: "b@x.test"},
	}}
	rec := do(mux(f), signed("GET", "/api/hub/users", "", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "internal-", "users.id n'est jamais exposé")

	var out struct {
		Users []struct {
			HubUserID *string `json:"hub_user_id"`
			Email     string  `json:"email"`
			Name      string  `json:"name"`
		} `json:"users"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Users, 2)
	require.NotNil(t, out.Users[0].HubUserID)
	assert.Equal(t, hubID, *out.Users[0].HubUserID)
	assert.Nil(t, out.Users[1].HubUserID, "un compte non relié sort avec hub_user_id null")
}

func TestSync_CompteInconnu_404UserNotInApp(t *testing.T) {
	f := &fakeStore{}
	rec := do(mux(f), signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","name":"X"}`, ""))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"user_not_in_app"}`, rec.Body.String())
	assert.Empty(t, f.updated)
}

func TestSync_RetrouveParHubUserIDSeulement_JamaisParEmail(t *testing.T) {
	// Le compte local porte l'adresse du body mais un AUTRE hub_user_id : rien ne bouge.
	other := "99999999-2222-3333-4444-555555555555"
	f := &fakeStore{found: &Account{ID: "u1", HubUserID: other, Email: "victime@x.test"}}
	rec := do(mux(f), signed("POST", "/api/hub/users/sync",
		`{"hub_user_id":"`+hubID+`","email":"victime@x.test","deleted":true}`, ""))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, []string{hubID}, f.findCalls)
	assert.Empty(t, f.updated)
	assert.Empty(t, f.revoked, "un email identique ne suffit pas à couper un compte")
}

func TestSync_NAppliqueQueLesChampsPresents(t *testing.T) {
	f := &fakeStore{found: &Account{ID: "u1", HubUserID: hubID}}
	m := mux(f)

	rec := do(m, signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","name":"  Alice Martin "}`, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, f.gotName)
	assert.Equal(t, "Alice Martin", *f.gotName)
	assert.Nil(t, f.gotEmail, "email absent = email intact")

	rec = do(m, signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","email":" Nouveau@X.test "}`, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, f.gotEmail)
	assert.Equal(t, "nouveau@x.test", *f.gotEmail)
	assert.Nil(t, f.gotName, "name absent = name intact")
	assert.Empty(t, f.revoked, "sans deleted, aucune coupure")
}

func TestSync_EmailDejaPris_409_EtEmailInvalide_400(t *testing.T) {
	f := &fakeStore{found: &Account{ID: "u1", HubUserID: hubID}, updateErr: ErrEmailTaken}
	m := mux(f)
	rec := do(m, signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","email":"pris@x.test"}`, ""))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error":"email_taken"}`, rec.Body.String())

	f.updated = nil
	rec = do(m, signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","email":"pas-un-email"}`, ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, f.updated)
}

func TestSync_HubUserIDInvalide_400(t *testing.T) {
	f := &fakeStore{}
	rec := do(mux(f), signed("POST", "/api/hub/users/sync", `{"hub_user_id":"pas-un-uuid","deleted":true}`, ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, f.findCalls)
}

func TestSync_Deleted_CoupeLeCompteSansLeSupprimer(t *testing.T) {
	f := &fakeStore{found: &Account{ID: "u1", HubUserID: hubID}}
	rec := do(mux(f), signed("POST", "/api/hub/users/sync", `{"hub_user_id":"`+hubID+`","deleted":true}`, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"u1"}, f.revoked)
	assert.Empty(t, f.updated, "deleted seul ne touche ni nom ni email")
}

func TestSQLStore_ListHumans_FiltreLesHumains(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`FROM users WHERE type = 'user'`).
		WillReturnRows(sqlmock.NewRows([]string{"hub_user_id", "email", "name"}).
			AddRow(hubID, "a@x.test", "A").AddRow(nil, "b@x.test", ""))
	got, err := (&SQLStore{DB: db}).ListHumans(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, hubID, got[0].HubUserID)
	assert.Equal(t, "", got[1].HubUserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLStore_Revoke_SessionsPuisMarqueur_EnTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM user_sessions WHERE user_id = \$1`).WithArgs("u1").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`UPDATE users SET hub_deleted_at = NOW\(\)`).WithArgs("u1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, (&SQLStore{DB: db}).Revoke(context.Background(), "u1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLStore_UpdateIdentity_EmailPrisDevientErrEmailTaken(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`UPDATE users SET`).WillReturnError(assert.AnError)
	err = (&SQLStore{DB: db}).UpdateIdentity(context.Background(), "u1", nil, nil)
	assert.ErrorIs(t, err, assert.AnError)

	mock.ExpectExec(`UPDATE users SET`).WillReturnError(errString(`pq: duplicate key value violates unique constraint "users_email_key"`))
	e := "a@x.test"
	err = (&SQLStore{DB: db}).UpdateIdentity(context.Background(), "u1", nil, &e)
	assert.ErrorIs(t, err, ErrEmailTaken)
}

type errString string

func (e errString) Error() string { return string(e) }
