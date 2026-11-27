// VER-265 (27/11/2026) : l'éditeur de code (Monaco) est servi par la console elle-même, plus par jsdelivr.
// Avant, @monaco-editor/react le chargeait depuis cdn.jsdelivr.net, qu'il fallait donc autoriser dans script-src. Or
// jsdelivr sert n'importe quel paquet npm : une balise script injectée pouvait y charger son code, la CSP la laissait passer.
// Le dossier monaco-editor/min/vs (même version que celle du CDN, 0.55.1) est copié dans public/monaco/vs par vite.config.ts
// (dev et build). Les composants importent Editor D'ICI : le chargeur est pointé sur la copie locale avant le premier
// éditeur, et seulement dans les pages qui en ont un (aucun poids au démarrage de la console).
import Editor, { loader } from '@monaco-editor/react'

export const VERIDIAN_MONACO_VS = `${import.meta.env.BASE_URL}monaco/vs`

export function installVeridianMonaco(): void {
  loader.config({ paths: { vs: VERIDIAN_MONACO_VS } })
}

installVeridianMonaco()

export { Editor }
export default Editor
export type { OnMount, BeforeMount } from '@monaco-editor/react'
