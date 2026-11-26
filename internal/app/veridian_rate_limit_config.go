package app

import (
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/http/middleware"
)

// veridianRateLimitConfig : la config du rate-limit global (règle 11) avec les deux secrets dont il a besoin pour ne
// croire que ce qui est signé (26/11) : HUB_API_SECRET pour l'exemption des appels du Hub, la clé JWT pour le seau
// par identité. Branchée par une seule ligne d'app.go (fichier gelé).
func veridianRateLimitConfig(cfg *config.Config) middleware.VeridianAPIRateLimitConfig {
	c := middleware.LoadVeridianAPIRateLimitConfig()
	if cfg != nil {
		c.HubSecret = cfg.HubAPISecret
		c.JWTSecret = cfg.Security.JWTSecret
	}
	return c
}
