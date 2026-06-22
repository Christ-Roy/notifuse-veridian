import * as crypto from 'crypto'

// globalTeardown Playwright — filet de sécurité anti-accumulation de bases de test.
//
// Problème (cf. todo/2026-06-20-e2e-sature-staging-provisioning-pic.md) : la suite
// (298 tests) provisionne des centaines de workspaces de test. La plupart des specs
// wipent en afterAll, MAIS un test crashé saute son afterAll → le tenant + sa base
// restent. Cumulé sur des runs, le pool DB staging sature (348/350) et les tests de
// provisioning concurrent (chaos-provisioning, anti-regression) flakent (500/404).
//
// Ce teardown appelle le GC orphelines staging-only EN FIN DE RUN, quoi qu'il arrive,
// pour garantir 0 résidu. Best-effort : un échec du GC ne fait PAS échouer le run
// (le résultat des tests prime ; le GC est de l'hygiène). Staging-only par
// construction (l'endpoint renvoie 503 hors staging → no-op silencieux en prod).
async function globalTeardown() {
  const NOTIFUSE_URL = process.env.NOTIFUSE_URL
  const HUB_API_SECRET = process.env.HUB_API_SECRET
  if (!NOTIFUSE_URL || !HUB_API_SECRET) {
    console.log('[global-teardown] NOTIFUSE_URL/HUB_API_SECRET absents → skip GC')
    return
  }
  try {
    const body = JSON.stringify({ dry_run: false })
    const ts = Date.now().toString()
    const signature = crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.${body}`).digest('hex')
    const r = await fetch(`${NOTIFUSE_URL}/api/veridian/admin/gc-orphan-workspace-dbs`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'x-veridian-app': 'notifuse',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': ts,
      },
      body,
    })
    if (r.status === 503) {
      console.log('[global-teardown] GC = 503 (prod/staging-only) → skip, normal')
      return
    }
    if (!r.ok) {
      console.log(`[global-teardown] GC HTTP ${r.status} → best-effort, on ignore`)
      return
    }
    const j: any = await r.json().catch(() => ({}))
    console.log(`[global-teardown] GC orphelines : ${j.total_orphans ?? '?'} vues, ${(j.dropped?.length) ?? '?'} droppées`)
  } catch (e) {
    console.log(`[global-teardown] GC best-effort échoué (ignoré) : ${(e as Error).message}`)
  }
}

export default globalTeardown
