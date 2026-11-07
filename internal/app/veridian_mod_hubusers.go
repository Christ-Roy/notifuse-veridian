package app

// Capacité « comptes vus par le Hub » (CONTRAT-HUB §3.8, fiche 80) : GET /api/hub/users et
// POST /api/hub/users/sync. L'import vide suffit, le module s'enregistre à l'init.
import _ "github.com/Notifuse/notifuse/internal/veridian/hubusers"
