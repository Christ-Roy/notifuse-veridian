package relaycert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/relaysettings"
	"github.com/Notifuse/notifuse/pkg/crypto"
)

// settingKey porte le bundle chiffré dans les réglages système (même mécanisme que les autres secrets).
const settingKey = "encrypted_veridian_relay_bundle"

// SettingsStore persiste le bundle dans les réglages système, chiffré par SECRET_KEY (AES-GCM, hex),
// et publie l'état lisible (échéance, série, dernier renouvellement, dernière erreur) en clair.
type SettingsStore struct {
	repo       domain.SettingRepository
	passphrase string
}

// NewSettingsStore construit le stockage. passphrase = SECRET_KEY de la configuration.
func NewSettingsStore(repo domain.SettingRepository, passphrase string) (*SettingsStore, error) {
	if repo == nil {
		return nil, errors.New("relaycert: dépôt de réglages requis")
	}
	if passphrase == "" {
		return nil, errors.New("relaycert: SECRET_KEY requis pour chiffrer le certificat")
	}
	return &SettingsStore{repo: repo, passphrase: passphrase}, nil
}

// Load rend le bundle déchiffré, ou (nil, nil) si rien n'est stocké.
func (s *SettingsStore) Load(ctx context.Context) (*Bundle, error) {
	setting, err := s.repo.Get(ctx, settingKey)
	if err != nil || setting == nil || setting.Value == "" {
		return nil, nil
	}
	plain, err := crypto.DecryptFromHexString(setting.Value, s.passphrase)
	if err != nil {
		return nil, errors.New("relaycert: déchiffrement du certificat du relais impossible (SECRET_KEY)")
	}
	var b Bundle
	if err := json.Unmarshal([]byte(plain), &b); err != nil {
		return nil, fmt.Errorf("bundle du relais illisible: %w", err)
	}
	return &b, nil
}

// Save chiffre le bundle et l'écrit. La clé privée n'est jamais stockée en clair.
func (s *SettingsStore) Save(ctx context.Context, b *Bundle) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	enc, err := crypto.EncryptString(string(raw), s.passphrase)
	if err != nil {
		return fmt.Errorf("chiffrement du certificat du relais: %w", err)
	}
	return s.repo.Set(ctx, settingKey, enc)
}

// SaveState écrit l'état lisible dans les réglages (lecture seule côté API). Vide = effacé.
func (s *SettingsStore) SaveState(ctx context.Context, st State) error {
	values := map[string]string{
		relaysettings.KeyCertNotAfter:    timeValue(st.NotAfter),
		relaysettings.KeyCertSerial:      st.Serial,
		relaysettings.KeyCertLastRenewed: timeValue(st.LastRenewed),
		relaysettings.KeyCertLastError:   st.LastError,
	}
	for key, value := range values {
		if err := s.repo.Set(ctx, key, value); err != nil {
			return fmt.Errorf("écriture de %s: %w", key, err)
		}
	}
	return nil
}

func timeValue(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
