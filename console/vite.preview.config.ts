// === Veridian patch — config Vite dédiée à `vite preview` pour la suite E2E ===
//
// Pourquoi un fichier séparé : `vite.config.ts` définit `server.https`
// avec des certs self-signed locaux. Vite hérite cette config pour
// `vite preview` aussi → le healthcheck `webServer.url` de Playwright
// échoue silencieusement sur « Self-signed certificate detected ».
//
// Cette config minimale ne déclare PAS `server.https` → `vite preview`
// sert dist/ en HTTP plain. Le build (rollupOptions, manualChunks,
// plugins, base /console/) reste celui de `vite.config.ts` car la
// commande `npm run build` lit ce fichier-là. Ce config-ci est lu
// uniquement quand on lance `vite preview --config vite.preview.config.ts`.
//
// Usage :
//   npm run build
//   npx vite preview --config vite.preview.config.ts --port 4173 --host 127.0.0.1

import { defineConfig } from 'vite'

export default defineConfig({
  base: '/console/',
  // Pas de `server.https` ici — preview reste en HTTP plain.
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true
  }
})
