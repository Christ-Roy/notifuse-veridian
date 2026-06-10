package domain

import "testing"

func TestVeridianIsPrivateSMTPHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		// Tailscale CGNAT 100.64/10 (cas relai agences-veridian.fr)
		{"tailscale cgnat", "100.92.215.42", true},
		{"tailscale low bound", "100.64.0.1", true},
		{"tailscale high bound", "100.127.255.254", true},
		{"100.x hors cgnat (public)", "100.128.0.1", false},
		{"100.63 hors cgnat (public)", "100.63.255.255", false},

		// RFC1918
		{"rfc1918 10", "10.0.0.5", true},
		{"rfc1918 172.16", "172.16.0.1", true},
		{"rfc1918 192.168", "192.168.1.10", true},

		// loopback
		{"loopback", "127.0.0.1", true},

		// public IP → refusé
		{"public gmail mx", "142.250.150.27", false},
		{"public cloudflare", "1.1.1.1", false},

		// hostname interne non résolvable (nom de service Docker) → privé
		{"docker service name", "smtp-sink", true},
		{"compose internal", "mail-relay", true},

		// vide → pas privé (le garde-fou doit échouer plus tôt sur host vide)
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := veridianIsPrivateSMTPHost(tt.host); got != tt.want {
				t.Errorf("veridianIsPrivateSMTPHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestSMTPSettings_Validate_SkipTLSVerifyGuard(t *testing.T) {
	t.Run("skip autorisé vers host privé Tailscale", func(t *testing.T) {
		s := &SMTPSettings{Host: "100.92.215.42", Port: 587, UseTLS: true, SkipTLSVerify: true}
		if err := s.Validate(""); err != nil {
			t.Fatalf("attendu OK pour host privé, got: %v", err)
		}
	})

	t.Run("skip autorisé vers nom de service interne", func(t *testing.T) {
		s := &SMTPSettings{Host: "mail-relay", Port: 587, UseTLS: true, SkipTLSVerify: true}
		if err := s.Validate(""); err != nil {
			t.Fatalf("attendu OK pour service interne, got: %v", err)
		}
	})

	t.Run("skip REFUSÉ vers host public", func(t *testing.T) {
		s := &SMTPSettings{Host: "smtp.gmail.com", Port: 587, UseTLS: true, SkipTLSVerify: true}
		if err := s.Validate(""); err == nil {
			t.Fatal("attendu erreur : skip_tls_verify interdit vers host public")
		}
	})

	t.Run("sans skip, host public reste valide (non-régression)", func(t *testing.T) {
		s := &SMTPSettings{Host: "smtp.gmail.com", Port: 587, UseTLS: true, SkipTLSVerify: false}
		if err := s.Validate(""); err != nil {
			t.Fatalf("attendu OK sans skip, got: %v", err)
		}
	})
}
