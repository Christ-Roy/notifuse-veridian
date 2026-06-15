package domain

// Veridian fork (cold outbound) — SUPPRESSION du lien/header unsubscribe en
// contexte tunnel cold.
//
// Décision Robert 2026-06-14 (ticket todo/2026-06-14-cold-no-unsubscribe.md) :
// le cold outreach Veridian assume AUCUN lien/header unsubscribe sur les envois
// cold (façon Lemlist/Instantly). Le cold B2B se présente comme du 1-to-1
// personnel ; un footer "se désabonner" + header RFC-8058 `List-Unsubscribe` =
// signal "mailing de masse" qui tue la délivrabilité et le côté personnel.
// Risque légal B2B assumé en connaissance de cause.
//
// Mécanique (sans patcher le compilateur upstream) : le broadcast ne remplit
// `EmailOptions.ListUnsubscribeURL` que si `data["oneclick_unsubscribe_url"]`
// est non-vide, et le header RFC-8058 n'est posé par les providers (SES/Mailgun/
// SparkPost) que si `ListUnsubscribeURL` est non-vide. Il suffit donc, EN
// CONTEXTE TUNNEL, de NE PAS propager cette URL → ni header, ni footer.
//
// NON-RÉGRESSION stricte : hors contexte tunnel, ce prédicat retourne false →
// les broadcasts marketing classiques GARDENT leur unsubscribe à l'identique.

// VeridianSuppressUnsubscribe indique s'il faut supprimer le lien/header
// unsubscribe pour cet envoi. Vrai UNIQUEMENT en contexte tunnel cold, détecté
// par le MÊME prédicat centralisé que la rotation des senders et le resolver
// pixel (VeridianIsColdContext) : un signal suffit — tag contact custom_string_5,
// OU config cold posée sur le broadcast (rates/caps/window/pixel dans metadata),
// OU config cold au niveau workspace (settings). Cohérence garantie avec le
// reste du tunnel : pixel, throttle et unsubscribe partagent la même définition
// de "contexte cold".
//
// workspace peut être nil (chemin sans fallback workspace) → la détection se
// fait alors sur le contact + le broadcast seuls, comme VeridianIsColdContext.
func VeridianSuppressUnsubscribe(contact *Contact, broadcast *Broadcast, workspace *Workspace) bool {
	return VeridianIsColdContext(contact, broadcast, workspace)
}
