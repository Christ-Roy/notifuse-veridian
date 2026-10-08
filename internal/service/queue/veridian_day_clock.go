package queue

import "time"

// veridianNow est l'horloge des portes de plafond journalier (jour de compte).
// Variable de package pour que les tests fixent l'instant, notamment autour de
// minuit a Paris et des changements d'heure (lot 4, 08/10/2026).
var veridianNow = time.Now
