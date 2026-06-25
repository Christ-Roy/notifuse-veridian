# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-06-25 04:27 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-06-25
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 29 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **0 RED** — fix prioritaire
- 🔴 **6 HIGH** — action recommandée cette semaine
- 🟡 **14 MEDIUM** — récap, pas urgent
- 🟢 **9 NOISE** — annexe collapse


---

## 🔴 HIGH — 6 CVE en 2 groupes

### 1. `golang.org/x/crypto` — v0.46.0 → **0.52.0**

- **CVE** : `CVE-2026-46595` (HIGH/Auth bypass)
- **Type** : Auth bypass
- **Score max** : 45
- **Title** : golang.org/x/crypto/ssh: golang.org/x/crypto/ssh: Authorization bypass due to skipped source-address validation
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/crypto@0.52.0` + `go mod tidy`

### 2. `golang.org/x/net` — v0.48.0 → **0.55.0**

- **CVE** : `CVE-2026-39821` (HIGH/Priv esc), `CVE-2026-25681` (HIGH/XSS), `CVE-2026-27136` (HIGH/XSS), `CVE-2026-42502` (HIGH/XSS), `CVE-2026-42506` (HIGH/XSS)
- **Type** : Priv esc, XSS
- **Score max** : 45
- **Title** : golang.org/x/net/idna: golang: golang.org/x/net/idna: Privilege escalation via incorrect Punycode label processing
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/net@0.55.0` + `go mod tidy`


---

## 🟡 MEDIUM — 14 CVE en 4 groupes

### 1. `vite` — 7.3.2 → **8.0.16**

- **CVE** : `CVE-2026-53571` (HIGH/Unclassified)
- **Type** : Unclassified
- **Score max** : 15
- **Title** : vite: `server.fs.deny` bypass on Windows alternate paths
- **Source** : `notification_center/package-lock.json`
- **Fix** : `pnpm up vite` (jusqu'à >= `8.0.16`)

### 2. `golang.org/x/crypto` — v0.46.0 → **0.52.0**

- **CVE** : `CVE-2026-39827` (HIGH/Unclassified), `CVE-2026-39828` (HIGH/Unclassified), `CVE-2026-39829` (HIGH/DoS), `CVE-2026-39830` (HIGH/DoS), `CVE-2026-39835` (HIGH/Unclassified), `CVE-2026-42508` (HIGH/Unclassified), `CVE-2026-46597` (HIGH/Unclassified)
- **Type** : DoS, Unclassified
- **Score max** : 15
- **Title** : An authenticated SSH client that repeatedly opened channels which were ...
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/crypto@0.52.0` + `go mod tidy`

### 3. `golang.org/x/net` — v0.48.0 → **0.55.0**

- **CVE** : `CVE-2026-25680` (HIGH/DoS), `CVE-2026-33814` (HIGH/DoS)
- **Type** : DoS
- **Score max** : 15
- **Title** : Parsing arbitrary HTML can consume excessive CPU time, possibly leadin ...
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/net@0.55.0` + `go mod tidy`

### 4. `dompurify` — 3.4.0 → **3.4.11**

- **CVE** : `CVE-2026-49458` (MEDIUM/XSS), `CVE-2026-49459` (MEDIUM/XSS), `CVE-2026-49978` (MEDIUM/XSS), `GHSA-cmwh-pvxp-8882` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : DOMPurify: Cross-realm IN_PLACE sanitization leaves executable markup intact via realm-bound `instanceof` checks
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up dompurify` (jusqu'à >= `3.4.11`)


---

## 🟢 NOISE filtré (9 CVE)

<details>
<summary>Liste complète (5 groupes — clique pour déplier)</summary>

| Package | Installed | Fix | CVE count | Max score |
|---|---|---|---|---|
| `dompurify` | 3.4.0 | 3.4.7 | 1 | 6 |
| `js-yaml` | 4.1.1 | 4.2.0 | 1 | 6 |
| `markdown-it` | 14.1.1 | 14.2.0 | 1 | 6 |
| `vite` | 7.3.2 | 8.0.16 | 1 | 6 |
| `golang.org/x/crypto` | v0.46.0 | 0.52.0 | 5 | 6 |

</details>


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
