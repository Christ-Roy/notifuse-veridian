/// <reference types="vitest" />
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/__tests__/setup.tsx'],
    // Runner partagé (self-hosted) : sans plafond, vitest lance un worker par cœur et les tests
    // lourds (rendus antd en jsdom) se ralentissent de 3 à 6 fois sous charge, jusqu'à dépasser leur
    // timeout (28/10 : 6 tests > 30 s). Un plafond de moitié des cœurs en CI garde les durées stables ;
    // en local, tous les cœurs. Pas de timeout allongé : voir docs/claude/75.
    maxWorkers: process.env.CI ? '50%' : undefined,
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.{test,spec}.{ts,tsx}', 'src/vite-env.d.ts', 'src/__tests__/**/*']
    }
  },
  resolve: {
    alias: {
      '@': '/src'
    },
    mainFields: ['module', 'jsnext:main', 'jsnext', 'main']
  }
})
