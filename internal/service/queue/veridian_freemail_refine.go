package queue

import "github.com/Notifuse/notifuse/internal/domain"

// veridianRefineLegacyFreemail ré-éclate la classe historique freemail_fr en FAI
// (orange, sfr_altice, free, laposte, bouygues). Veridian 22/10/2026, fiche 69.
//
// Pourquoi ici : la classe d'un envoi est le tag du contact (custom_string_5) copié
// dans le payload de l'entrée à l'enqueue, puis figé. Les contacts déjà étiquetés
// freemail_fr par ODH, et les entrées déjà en file, ne sont jamais re-résolus ailleurs.
// Cette fonction rend la scission transparente : AUCUN contact ni entrée à migrer.
//
//  1. suffixe de l'adresse (orange.fr, free.fr...) : zéro I/O ;
//  2. sinon le MX réel via le classifieur du worker (cache 7 jours, timeout 2 s) :
//     un domaine professionnel hébergé chez Orange ou SFR est rattaché à son FAI ;
//  3. sinon freemail_fr, qui reste une classe valide, bridée par ses propres défauts.
//
// Seul un résultat de la famille freemail est retenu : un tag freemail_fr sur un
// domaine dont le MX est aujourd'hui Google ne bascule pas de lui-même sur google.
func (w *EmailQueueWorker) veridianRefineLegacyFreemail(entry *domain.EmailQueueEntry) string {
	if c := domain.ClassifyProviderClass(entry.ContactEmail); domain.VeridianParentClass(c) == domain.ProviderClassFreemailFR {
		return c
	}
	if w.providerMXClassifier != nil {
		if c := w.providerMXClassifier.ClassifyEmail(w.ctx, entry.ContactEmail); domain.VeridianParentClass(c) == domain.ProviderClassFreemailFR {
			return c
		}
	}
	return domain.ProviderClassFreemailFR
}
