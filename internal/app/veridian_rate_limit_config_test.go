package app

import (
	"testing"

	"github.com/Notifuse/notifuse/config"
)

// Sans ces deux secrets, l'exemption HMAC et le seau par identité retombent à rien : le rate-limit croirait de nouveau
// un en-tête ou un jeton non vérifiés, ou n'exempterait plus le Hub.
func TestVeridianRateLimitConfig_PorteLesSecrets(t *testing.T) {
	cfg := &config.Config{HubAPISecret: "hub"}
	cfg.Security.JWTSecret = []byte("jwt")
	c := veridianRateLimitConfig(cfg)
	if c.HubSecret != "hub" || string(c.JWTSecret) != "jwt" {
		t.Fatalf("secrets non transmis : hub=%q jwt=%q", c.HubSecret, c.JWTSecret)
	}
	if c := veridianRateLimitConfig(nil); c.HubSecret != "" || c.JWTSecret != nil {
		t.Fatal("config absente : aucun secret")
	}
}
