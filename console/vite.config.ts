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
    // ~6.3 MB. On découpe en chunks stables et cacheables séparément :
    //  - react-vendor : socle React, change quasi jamais → cache long
    //  - antd        : Ant Design v5, le plus gros morceau de la lib UI
    //  - tanstack    : query + router
    //  - monaco      : éditeur de code (~2 MB) — confiné à l'email builder
    //                  et au blog editor, n'a rien à faire dans le chunk
    //                  initial du dashboard
    //  - charts      : echarts (~1 MB) — utilisé seulement par l'écran
    //                  Analytics
    //  - flow        : @xyflow/react — utilisé seulement par Automations
    // Ces libs étant isolées, un changement du code applicatif n'invalide
    // plus le cache navigateur des chunks vendor.
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('monaco-editor') || id.includes('@monaco-editor')) return 'monaco'
          if (id.includes('echarts') || id.includes('zrender')) return 'charts'
          if (id.includes('@xyflow') || id.includes('reactflow')) return 'flow'
          // prosemirror : l'éditeur riche du blog — gros et confiné.
          if (id.includes('prosemirror')) return 'editor'
          if (id.includes('/antd/') || id.includes('@ant-design')) return 'antd'
          if (id.includes('@tanstack')) return 'tanstack'
          // fortawesome : 4 packs d'icônes, sortis dans leur propre chunk.
          if (id.includes('@fortawesome')) return 'icons'
          // utilitaires courants : lodash, dayjs — chunk vendor-utils stable.
          if (id.includes('/lodash') || id.includes('/dayjs')) return 'vendor-utils'
          if (
            id.includes('/react/') ||
            id.includes('/react-dom/') ||
            id.includes('/scheduler/')
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
