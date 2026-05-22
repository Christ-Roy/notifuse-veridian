/// <reference types="vitest" />
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { lingui } from '@lingui/vite-plugin'
import { fileURLToPath } from 'url'
import { dirname, resolve } from 'path'
import { readFileSync } from 'fs'
import path from 'path'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)

// https://vitejs.dev/config/
export default defineConfig({
  base: '/console/',
  build: {
    // === Veridian patch — perf-ui-baseline (ticket 2026-05-22) ===
    // Sans manualChunks, Vite met TOUTE la SPA dans un seul index.js de
    // ~6.3 MB. On découpe MAIS avec une règle de sûreté apprise à la dure :
    //
    // ⚠️ Tout ce qui appelle `React.createContext` au top-level (React,
    // TanStack, Ant Design, @ant-design/icons, fortawesome…) DOIT vivre
    // dans le MÊME chunk que React. Rollup ne garantit pas l'ordre
    // d'évaluation entre chunks frères : un chunk `tanstack` séparé peut
    // s'exécuter avant `react-vendor` → `Cannot read properties of
    // undefined (reading 'createContext')` au boot, app morte.
    // Donc : `react-vendor` = React + tout l'écosystème UI qui en dépend.
    //
    // Restent isolés (et lazy via React.lazy) les gros modules SANS
    // dépendance d'ordre sur React au top-level, confinés à un écran :
    //  - monaco : éditeur de code (~2 MB) — email builder / blog editor
    //  - charts : echarts (~1 MB) — écran Analytics
    //  - flow   : @xyflow/react — écran Automations
    //  - editor : prosemirror — éditeur de blog
    //  - vendor-utils : lodash, dayjs — utilitaires purs, pas de contexte React
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          // Gros modules confinés à un écran, lazy-loadés — pas de
          // dépendance d'ordre sur React.
          if (id.includes('monaco-editor') || id.includes('@monaco-editor')) return 'monaco'
          if (id.includes('echarts') || id.includes('zrender')) return 'charts'
          if (id.includes('@xyflow') || id.includes('reactflow')) return 'flow'
          if (id.includes('prosemirror')) return 'editor'
          // Utilitaires purs (pas de React.createContext top-level).
          if (id.includes('/lodash') || id.includes('/dayjs')) return 'vendor-utils'
          // React + TOUT l'écosystème UI qui appelle createContext au
          // top-level → un seul chunk atomique, ordre garanti.
          if (
            id.includes('/react/') ||
            id.includes('/react-dom/') ||
            id.includes('/scheduler/') ||
            id.includes('/react-is/') ||
            id.includes('@tanstack') ||
            id.includes('/antd/') ||
            id.includes('@ant-design') ||
            id.includes('@fortawesome') ||
            id.includes('rc-')
          ) {
            return 'react-vendor'
          }
          return undefined
        },
      },
    },
  },
  plugins: [
    react({
      babel: {
        plugins: ['@lingui/babel-plugin-lingui-macro'],
      },
    }),
    tailwindcss(),
    lingui(),
  ],
  server: {
    host: 'notifusedev.com',
    https: {
      key: readFileSync(resolve(__dirname, 'certificates/key.pem')),
      cert: readFileSync(resolve(__dirname, 'certificates/cert.pem'))
    },
    proxy: {
      '/config.js': {
        target: 'https://localapi.notifuse.com:4000',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path.replace(/^\/console/, '')
      },
      '/console/config.js': {
        target: 'https://localapi.notifuse.com:4000',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path.replace(/^\/console/, '')
      }
    }
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/__tests__/setup.tsx'],
    include: ['**/*.{test,spec}.{js,mjs,cjs,ts,mts,cts,jsx,tsx}'],
    coverage: {
      reporter: ['text', 'json', 'html'],
      exclude: ['node_modules/', 'src/__tests__/setup.tsx']
    }
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src')
    },
    extensions: ['.js', '.jsx', '.ts', '.tsx', '.json']
  },
  optimizeDeps: {
    include: ['@fortawesome/react-fontawesome', '@fortawesome/fontawesome-svg-core']
  }
})
