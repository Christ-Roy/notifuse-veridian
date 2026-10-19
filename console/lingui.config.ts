import { defineConfig } from "@lingui/cli"

export default defineConfig({
  sourceLocale: "en",
  locales: ["en", "fr", "es", "de", "ca", "pt-BR", "ja", "it"],
  // Sans numeros de ligne : un decalage de code ne doit pas rendre les .po « obsoletes »
  // (check-console-build.sh echouait a chaque commit qui deplacait un t`...`).
  format: "po",
  formatOptions: { lineNumbers: false },
  catalogs: [
    {
      path: "src/i18n/locales/{locale}",
      include: ["src"],
    },
  ],
})
