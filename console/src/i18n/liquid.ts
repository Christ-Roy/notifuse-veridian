// Exemples Liquid affiches dans des messages traduits.
// Les accolades doubles ne doivent JAMAIS apparaitre litteralement dans un t`...` :
// en ICU, `{{ x }}` est une syntaxe invalide (le message est supprime du catalogue et
// l'interface affiche son hash, ex. `X4YDvc`) ou, pire, rend `{}`. On les passe donc
// comme valeurs de placeholders nommes : t`... ${liquidUrl}` (audit UI 19/10/2026, D2).
// Garde-fou : src/i18n/messages-render.test.ts.
export const liquidUrl = '{{ url }}'
export const liquidVariable = '{{ variable }}'
export const liquidFontUrl = '{{ font_url }}'
export const liquidFirstName = '{{ contact.first_name }}'
export const liquidName = '{{ name }}'
export const liquidCompany = '{{ company }}'
