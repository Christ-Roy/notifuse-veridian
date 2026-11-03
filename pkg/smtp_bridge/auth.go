package smtp_bridge

import "errors"

// AuthHandler is a function type that authenticates SMTP credentials
// It receives the username (workspace_id) and password (api_key)
// Returns the workspace_id if authentication succeeds, or an error if it fails
type AuthHandler func(username, password string) (workspaceID string, err error)

// ErrTemporaryAuth marque un échec d'authentification TEMPORAIRE (limite de débit) : il est
// refusé en 454 4.7.0 et seul ce cas autorise un nouvel essai côté client. Tout autre échec
// d'identifiants est refusé en 535 5.7.8 (permanent).
var ErrTemporaryAuth = errors.New("temporary authentication failure")
