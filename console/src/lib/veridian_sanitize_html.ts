import DOMPurify from 'dompurify'

/**
 * Assainit un fragment HTML avant de le monter dans le DOM de la console
 * (dangerouslySetInnerHTML). Le contenu d'un modèle vient de l'API, d'un import
 * ou d'un autre membre : il ne doit jamais pouvoir exécuter un script dans
 * l'origine de la console (jeton en localStorage). Défaut S2/GHSA-pff3 de v42.
 *
 * Conserve la mise en forme d'un e-mail (balises de texte, style en ligne,
 * liens), retire scripts, gestionnaires d'événements, URL javascript: et
 * éléments actifs (iframe, object, embed, form, base, meta).
 */
const FORBID_TAGS = ['script', 'iframe', 'object', 'embed', 'form', 'base', 'meta', 'link', 'style']

export function veridianSanitizeHtml(html: string | null | undefined): string {
  if (!html) return ''
  return DOMPurify.sanitize(html, {
    FORBID_TAGS,
    FORBID_ATTR: ['srcdoc', 'formaction'],
    ALLOW_DATA_ATTR: false
  })
}
