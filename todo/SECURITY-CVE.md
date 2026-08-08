# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-08-08 04:26 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-08-08
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 9 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **0 RED** — fix prioritaire
- 🔴 **0 HIGH** — action recommandée cette semaine
- 🟡 **7 MEDIUM** — récap, pas urgent
- 🟢 **2 NOISE** — annexe collapse

✅ **Rien d'urgent.** Quelques items MEDIUM à voir quand t'as 5 min.


---

## 🟡 MEDIUM — 7 CVE en 3 groupes

### 1. `nanoid` — 3.3.16 → **5.1.6**

- **CVE** : `CVE-2026-67213` (HIGH/DoS)
- **Type** : DoS
- **Score max** : 15
- **Title** : nanoid (Nano ID) before 5.1.6 contains an infinite loop in the customA ...
- **Source** : `notification_center/package-lock.json`
- **Fix** : `pnpm up nanoid` (jusqu'à >= `5.1.6`)

### 2. `dompurify` — 3.4.0 → **3.4.13**

- **CVE** : `CVE-2026-49458` (MEDIUM/XSS), `CVE-2026-49459` (MEDIUM/XSS), `CVE-2026-49978` (MEDIUM/XSS), `CVE-2026-65898` (MEDIUM/XSS), `GHSA-55q2-fjhq-7xh7` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : dompurify: DOMPurify: Cross-site scripting due to improper sanitization of DOM nodes
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up dompurify` (jusqu'à >= `3.4.13`)

### 3. `echarts` — 5.6.0 → **6.1.0**

- **CVE** : `CVE-2026-45249` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : Apache ECharts has a cross-site scripting (XSS) vulnerability
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up echarts` (jusqu'à >= `6.1.0`)


---

## 🟢 NOISE filtré (2 CVE)

<details>
<summary>Liste complète (2 groupes — clique pour déplier)</summary>

| Package | Installed | Fix | CVE count | Max score |
|---|---|---|---|---|
| `dompurify` | 3.4.0 | 3.4.7 | 1 | 6 |
| `markdown-it` | 14.1.1 | 14.2.0 | 1 | 6 |

</details>


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
