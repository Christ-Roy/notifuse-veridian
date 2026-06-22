# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-06-22 04:25 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-06-22
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 9 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **0 RED** — fix prioritaire
- 🔴 **0 HIGH** — action recommandée cette semaine
- 🟡 **5 MEDIUM** — récap, pas urgent
- 🟢 **4 NOISE** — annexe collapse

✅ **Rien d'urgent.** Quelques items MEDIUM à voir quand t'as 5 min.


---

## 🟡 MEDIUM — 5 CVE en 2 groupes

### 1. `vite` — 7.3.2 → **8.0.16**

- **CVE** : `CVE-2026-53571` (HIGH/Unclassified)
- **Type** : Unclassified
- **Score max** : 15
- **Title** : vite: `server.fs.deny` bypass on Windows alternate paths
- **Source** : `notification_center/package-lock.json`
- **Fix** : `pnpm up vite` (jusqu'à >= `8.0.16`)

### 2. `dompurify` — 3.4.0 → **3.4.11**

- **CVE** : `CVE-2026-49458` (MEDIUM/XSS), `CVE-2026-49459` (MEDIUM/XSS), `CVE-2026-49978` (MEDIUM/XSS), `GHSA-cmwh-pvxp-8882` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : DOMPurify: Cross-realm IN_PLACE sanitization leaves executable markup intact via realm-bound `instanceof` checks
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up dompurify` (jusqu'à >= `3.4.11`)


---

## 🟢 NOISE filtré (4 CVE)

<details>
<summary>Liste complète (4 groupes — clique pour déplier)</summary>

| Package | Installed | Fix | CVE count | Max score |
|---|---|---|---|---|
| `dompurify` | 3.4.0 | 3.4.7 | 1 | 6 |
| `js-yaml` | 4.1.1 | 4.2.0 | 1 | 6 |
| `markdown-it` | 14.1.1 | 14.2.0 | 1 | 6 |
| `vite` | 7.3.2 | 8.0.16 | 1 | 6 |

</details>


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
