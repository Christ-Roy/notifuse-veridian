package app

// Capacité « politique d'envoi et stock » (lot L3) : l'import vide suffit, le module s'enregistre
// dans le registre de la plateforme à l'init (platform.Register) et ses migrations dans le registre
// des migrations Veridian (V66).
import _ "github.com/Notifuse/notifuse/internal/veridian/policy"
