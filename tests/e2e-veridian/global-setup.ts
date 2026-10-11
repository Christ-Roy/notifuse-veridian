import { prodResidueSetup } from './prod-residue'

// Prod seulement (E2E_PROD_MODE=1) : photographie des tenants avant le run (cf. prod-residue.ts).
async function globalSetup() {
  await prodResidueSetup()
}

export default globalSetup
