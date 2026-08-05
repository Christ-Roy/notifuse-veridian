package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transientIMAPConsumer struct {
	calls int
	err   error
}

func (c *transientIMAPConsumer) Name() string { return "transient-test" }

func (c *transientIMAPConsumer) OnNewMessage(_ *VeridianIMAPMessage) error {
	c.calls++
	if c.calls == 1 {
		return c.err
	}
	return nil
}

func TestVeridianIMAPConsumer_ErrorIsObservableAndReplaySafe(t *testing.T) {
	sentinel := errors.New("temporary database outage")
	consumer := &transientIMAPConsumer{err: sentinel}
	var contract VeridianIMAPConsumer = consumer
	msg := &VeridianIMAPMessage{UID: 42, WorkspaceID: "ws-1"}

	require.ErrorIs(t, contract.OnNewMessage(msg), sentinel)
	require.NoError(t, contract.OnNewMessage(msg))
	assert.Equal(t, 2, consumer.calls, "an unacknowledged UID must be safe to replay")
}

const testIMAPPassphrase = "0123456789abcdef0123456789abcdef" // 32 bytes for AES

func TestIMAPSettings_GetFolder(t *testing.T) {
	tests := []struct {
		name     string
		settings *IMAPSettings
		want     string
	}{
		{"nil settings", nil, DefaultIMAPFolder},
		{"empty folder", &IMAPSettings{}, DefaultIMAPFolder},
		{"whitespace folder", &IMAPSettings{Folder: "   "}, DefaultIMAPFolder},
		{"explicit folder", &IMAPSettings{Folder: "Bounces"}, "Bounces"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.settings.GetFolder())
		})
	}
}

func TestIMAPSettings_GetPollingInterval(t *testing.T) {
	tests := []struct {
		name     string
		settings *IMAPSettings
		want     time.Duration
	}{
		{"nil settings", nil, DefaultIMAPPollingInterval},
		{"zero seconds", &IMAPSettings{PollingIntervalSeconds: 0}, DefaultIMAPPollingInterval},
		{"negative seconds", &IMAPSettings{PollingIntervalSeconds: -5}, DefaultIMAPPollingInterval},
		{"below min clamped", &IMAPSettings{PollingIntervalSeconds: 5}, minIMAPPollingInterval},
		{"exactly min", &IMAPSettings{PollingIntervalSeconds: 30}, 30 * time.Second},
		{"valid above min", &IMAPSettings{PollingIntervalSeconds: 120}, 120 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.settings.GetPollingInterval())
		})
	}
}

func TestIMAPSettings_Address(t *testing.T) {
	s := &IMAPSettings{Host: "imap.example.com", Port: 993}
	assert.Equal(t, "imap.example.com:993", s.Address())
}

func TestIMAPSettings_EncryptDecryptPassword_Roundtrip(t *testing.T) {
	s := &IMAPSettings{Password: "s3cr3t-app-password"}

	require.NoError(t, s.EncryptPassword(testIMAPPassphrase))
	assert.NotEmpty(t, s.EncryptedPassword)
	assert.NotEqual(t, "s3cr3t-app-password", s.EncryptedPassword, "encrypted must differ from plaintext")

	// Decrypt into a fresh struct (only the encrypted field is persisted).
	loaded := &IMAPSettings{EncryptedPassword: s.EncryptedPassword}
	require.NoError(t, loaded.DecryptPassword(testIMAPPassphrase))
	assert.Equal(t, "s3cr3t-app-password", loaded.Password)
}

func TestIMAPSettings_DecryptPassword_WrongPassphrase(t *testing.T) {
	s := &IMAPSettings{Password: "hunter2"}
	require.NoError(t, s.EncryptPassword(testIMAPPassphrase))

	loaded := &IMAPSettings{EncryptedPassword: s.EncryptedPassword}
	err := loaded.DecryptPassword("ffffffffffffffffffffffffffffffff")
	assert.Error(t, err)
}

func TestIMAPSettings_Validate(t *testing.T) {
	tests := []struct {
		name      string
		settings  *IMAPSettings
		wantErr   bool
		errSubstr string
		// checkEncrypted asserts the password got encrypted in place.
		checkEncrypted bool
	}{
		{
			name:      "missing host",
			settings:  &IMAPSettings{Port: 993, Username: "u", Password: "p"},
			wantErr:   true,
			errSubstr: "host is required",
		},
		{
			name:      "whitespace host",
			settings:  &IMAPSettings{Host: "  ", Port: 993, Username: "u", Password: "p"},
			wantErr:   true,
			errSubstr: "host is required",
		},
		{
			name:      "invalid port zero",
			settings:  &IMAPSettings{Host: "h", Port: 0, Username: "u", Password: "p"},
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "invalid port too high",
			settings:  &IMAPSettings{Host: "h", Port: 70000, Username: "u", Password: "p"},
			wantErr:   true,
			errSubstr: "invalid port",
		},
		{
			name:      "missing username",
			settings:  &IMAPSettings{Host: "h", Port: 993, Password: "p"},
			wantErr:   true,
			errSubstr: "username is required",
		},
		{
			name:      "missing password (no encrypted either)",
			settings:  &IMAPSettings{Host: "h", Port: 993, Username: "u"},
			wantErr:   true,
			errSubstr: "password is required",
		},
		{
			name:           "valid with plaintext password encrypts it",
			settings:       &IMAPSettings{Host: "imap.example.com", Port: 993, Username: "u", Password: "p", UseTLS: true},
			wantErr:        false,
			checkEncrypted: true,
		},
		{
			name: "valid edit without password (encrypted already present)",
			settings: &IMAPSettings{
				Host: "imap.example.com", Port: 993, Username: "u",
				EncryptedPassword: mustEncrypt(t, "existing"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.settings.Validate(testIMAPPassphrase)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			if tt.checkEncrypted {
				assert.NotEmpty(t, tt.settings.EncryptedPassword)
				// roundtrip confirms it's the right ciphertext
				loaded := &IMAPSettings{EncryptedPassword: tt.settings.EncryptedPassword}
				require.NoError(t, loaded.DecryptPassword(testIMAPPassphrase))
				assert.Equal(t, "p", loaded.Password)
			}
		})
	}
}

// TestIMAPSettings_MarshalJSON_MasksPlaintextPassword garantit que la
// sérialisation de persistance masque le clair et conserve le ciphertext. La
// couche HTTP workspace retire ensuite aussi le ciphertext avant réponse API.
func TestIMAPSettings_MarshalJSON_MasksPlaintextPassword(t *testing.T) {
	const plaintext = "super-secret-imap-pw"
	s := &IMAPSettings{
		Host:              "imap.larksuite.com",
		Port:              993,
		Username:          "robert.brunon@veridian.site",
		Password:          plaintext,                 // champ runtime (déchiffré)
		EncryptedPassword: mustEncrypt(t, plaintext), // ciphertext persisté
		UseTLS:            true,
	}

	t.Run("marshal direct", func(t *testing.T) {
		raw, err := json.Marshal(s)
		require.NoError(t, err)
		js := string(raw)
		assert.NotContains(t, js, plaintext, "plaintext IMAP password must never appear in JSON")
		assert.NotContains(t, js, `"password"`, "the cleartext password field must be omitted")
		// Le ciphertext DOIT rester ici pour le round-trip DB. Ce test ne couvre
		// pas la réponse API, qui passe par veridianRedactWorkspaceForAPI.
		assert.Contains(t, js, `"encrypted_password"`, "encrypted password must be preserved for DB persistence")
		assert.Contains(t, js, s.EncryptedPassword)
		// Champs non secrets toujours là.
		assert.Contains(t, js, "imap.larksuite.com")
		assert.Contains(t, js, "robert.brunon@veridian.site")
		assert.NotContains(t, js, `"has_password"`, "derived API marker must be omitted from stored settings by default")
	})

	t.Run("derived readiness marker carries no secret", func(t *testing.T) {
		apiView := *s
		apiView.HasPassword = true
		apiView.Password = ""
		apiView.EncryptedPassword = ""
		raw, err := json.Marshal(&apiView)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"has_password":true`)
		assert.NotContains(t, string(raw), plaintext)
		assert.NotContains(t, string(raw), `"encrypted_password"`)
	})

	t.Run("marshal via pointer in containing struct", func(t *testing.T) {
		// Sérialisé comme dans le blob Integration de persistance.
		integ := Integration{ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP, IMAPSettings: s}
		raw, err := json.Marshal(integ)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), plaintext, "leak through containing struct")
	})

	t.Run("round-trip Encrypt->Marshal does not leak", func(t *testing.T) {
		fresh := &IMAPSettings{Host: "h", Port: 993, Username: "u", Password: "another-secret"}
		require.NoError(t, fresh.EncryptPassword(testIMAPPassphrase))
		raw, err := json.Marshal(fresh)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "another-secret")
		assert.NotContains(t, string(raw), `"password"`)
		assert.Contains(t, string(raw), `"encrypted_password"`)
	})
}

// TestIMAPSettings_UnmarshalJSON_StillReadsIncomingPassword garantit que le
// MarshalJSON custom n'a PAS cassé le décodage entrant : un POST
// create/update intégration IMAP avec un `password` clair doit toujours être lu
// (puis chiffré + persisté par Validate/BeforeSave).
func TestIMAPSettings_UnmarshalJSON_StillReadsIncomingPassword(t *testing.T) {
	incoming := `{"host":"imap.example.com","port":993,"username":"u","password":"incoming-pw","use_tls":true}`
	var s IMAPSettings
	require.NoError(t, json.Unmarshal([]byte(incoming), &s))
	assert.Equal(t, "incoming-pw", s.Password, "incoming plaintext password must still be decodable")

	// Et il se chiffre normalement en aval (chaîne complète create intégration).
	require.NoError(t, s.Validate(testIMAPPassphrase))
	assert.NotEmpty(t, s.EncryptedPassword)
	loaded := &IMAPSettings{EncryptedPassword: s.EncryptedPassword}
	require.NoError(t, loaded.DecryptPassword(testIMAPPassphrase))
	assert.Equal(t, "incoming-pw", loaded.Password)
}

// TestIMAPSettings_Value_DBBlobHasNoPlaintext vérifie que le blob persisté en DB
// (Integration.Value -> json.Marshal) ne contient pas le password en clair —
// BeforeSave ne vide pas le clair IMAP, c'est le MarshalJSON qui le protège.
func TestIMAPSettings_Value_DBBlobHasNoPlaintext(t *testing.T) {
	integ := Integration{
		ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP,
		IMAPSettings: &IMAPSettings{
			Host: "imap.example.com", Port: 993, Username: "u",
			Password: "db-plaintext-pw", EncryptedPassword: mustEncrypt(t, "db-plaintext-pw"),
		},
	}
	v, err := integ.Value()
	require.NoError(t, err)
	blob, ok := v.([]byte)
	require.True(t, ok)
	assert.False(t, strings.Contains(string(blob), "db-plaintext-pw"), "plaintext password must not be written to the DB blob")
	assert.Contains(t, string(blob), `"encrypted_password"`)
}

func mustEncrypt(t *testing.T, plain string) string {
	t.Helper()
	enc, err := crypto.EncryptString(plain, testIMAPPassphrase)
	require.NoError(t, err)
	return enc
}

// --- Integration switch wiring (BeforeSave / AfterLoad / Validate) ---

func TestIntegration_IMAP_Validate(t *testing.T) {
	t.Run("missing imap settings", func(t *testing.T) {
		integ := &Integration{ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP}
		err := integ.Validate(testIMAPPassphrase)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "imap settings are required")
	})

	t.Run("invalid imap settings bubbles up", func(t *testing.T) {
		integ := &Integration{
			ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP,
			IMAPSettings: &IMAPSettings{Port: 993, Username: "u", Password: "p"}, // no host
		}
		err := integ.Validate(testIMAPPassphrase)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid imap settings")
	})

	t.Run("valid imap integration encrypts password", func(t *testing.T) {
		integ := &Integration{
			ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP,
			IMAPSettings: &IMAPSettings{Host: "imap.example.com", Port: 993, Username: "u", Password: "p", UseTLS: true},
		}
		require.NoError(t, integ.Validate(testIMAPPassphrase))
		assert.NotEmpty(t, integ.IMAPSettings.EncryptedPassword)
	})
}

func TestIntegration_IMAP_BeforeSaveAfterLoad_Roundtrip(t *testing.T) {
	integ := &Integration{
		ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP,
		IMAPSettings: &IMAPSettings{
			Host: "imap.example.com", Port: 993, Username: "u",
			Password: "plaintext-pass", UseTLS: true,
		},
	}

	// BeforeSave encrypts password and (we then clear plaintext to simulate DB).
	require.NoError(t, integ.BeforeSave(testIMAPPassphrase))
	require.NotEmpty(t, integ.IMAPSettings.EncryptedPassword)
	integ.IMAPSettings.Password = "" // simulate: plaintext not persisted

	// AfterLoad decrypts it back.
	require.NoError(t, integ.AfterLoad(testIMAPPassphrase))
	assert.Equal(t, "plaintext-pass", integ.IMAPSettings.Password)
}

func TestIntegration_IMAP_BeforeSave_NoPasswordNoop(t *testing.T) {
	// Editing without a new password (encrypted already set) must not re-encrypt
	// or error.
	enc := mustEncrypt(t, "existing")
	integ := &Integration{
		ID: "i1", Name: "Bounce box", Type: IntegrationTypeIMAP,
		IMAPSettings: &IMAPSettings{
			Host: "h", Port: 993, Username: "u", EncryptedPassword: enc,
		},
	}
	require.NoError(t, integ.BeforeSave(testIMAPPassphrase))
	assert.Equal(t, enc, integ.IMAPSettings.EncryptedPassword, "encrypted password must be untouched when no plaintext provided")
}

func TestIntegration_IMAP_AfterLoad_NilSettingsSafe(t *testing.T) {
	integ := &Integration{ID: "i1", Name: "x", Type: IntegrationTypeIMAP}
	assert.NoError(t, integ.AfterLoad(testIMAPPassphrase))
	assert.NoError(t, integ.BeforeSave(testIMAPPassphrase))
}
