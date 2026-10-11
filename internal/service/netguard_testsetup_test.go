package service

import (
	"net"

	"github.com/Notifuse/notifuse/pkg/netguard"
)

// Les tests historiques de l'envoi SMTP parlent à un faux serveur sur
// 127.0.0.1 (port éphémère). Le seam de la garde réseau les y autorise
// explicitement ; les tests de refus (smtp_netguard_test.go) remettent la
// liste de production le temps de leur exécution.
func init() {
	smtpDialAllowlist = func() netguard.Allowlist {
		return netguard.Allowlist{{IP: net.ParseIP("127.0.0.1"), Port: 0}}
	}
}
