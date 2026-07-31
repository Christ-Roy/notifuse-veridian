# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-07-31 04:25 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-07-31
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 42 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **1 RED** — fix prioritaire
- 🔴 **6 HIGH** — action recommandée cette semaine
- 🟡 **22 MEDIUM** — récap, pas urgent
- 🟢 **9 NOISE** — annexe collapse


---

## 🚨 RED — 1 CVE en 1 groupe

### 1. `seroval` — 1.5.0 → **1.5.3**

- **CVE** : `CVE-2026-59940` (CRITICAL/RCE)
- **Type** : RCE
- **Score max** : 150
- **Title** : seroval: `seroval.fromJSON()` Promise resolver type confusion invokes attacker-controlled methods during deserialization
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up seroval` (jusqu'à >= `1.5.3`)


---

## 🔴 HIGH — 6 CVE en 4 groupes

### 1. `golang.org/x/net` — v0.48.0 → **0.55.0**

- **CVE** : `CVE-2026-25681` (HIGH/RCE), `CVE-2026-39821` (HIGH/Priv esc), `CVE-2026-27136` (HIGH/XSS)
- **Type** : Priv esc, RCE, XSS
- **Score max** : 75
- **Title** : golang.org/x/net/html: golang.org/x/net/html: Arbitrary code execution via Cross-Site Scripting
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/net@0.55.0` + `go mod tidy`

### 2. `google.golang.org/grpc` — v1.79.3 → **1.82.1**

- **CVE** : `GHSA-hrxh-6v49-42gf` (HIGH/Auth bypass)
- **Type** : Auth bypass
- **Score max** : 45
- **Title** : gRPC-Go: xDS RBAC and HTTP/2 Vulnerabilities
- **Source** : `go.mod`
- **Fix** : `go get google.golang.org/grpc@1.82.1` + `go mod tidy`

### 3. `golang.org/x/crypto` — v0.46.0 → **0.52.0**

- **CVE** : `CVE-2026-46595` (HIGH/Auth bypass)
- **Type** : Auth bypass
- **Score max** : 45
- **Title** : golang.org/x/crypto/ssh: golang.org/x/crypto/ssh: Authorization bypass due to skipped source-address validation
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/crypto@0.52.0` + `go mod tidy`

### 4. `postcss` — 8.5.14 → **8.5.18**

- **CVE** : `GHSA-r28c-9q8g-f849` (HIGH/Data leak)
- **Type** : Data leak
- **Score max** : 30
- **Title** : PostCSS: Path Traversal in Previous Source Map Auto-Loading (sourceMappingURL) leads to Arbitrary .map File Disclosure
- **Source** : `notification_center/package-lock.json`
- **Fix** : `pnpm up postcss` (jusqu'à >= `8.5.18`)


---

## 🟡 MEDIUM — 22 CVE en 9 groupes

### 1. `js-yaml` — 4.1.1 → **4.3.0**

- **CVE** : `CVE-2026-59869` (HIGH/DoS)
- **Type** : DoS
- **Score max** : 15
- **Title** : js-yaml: js-yaml: Denial of Service via crafted YAML documents
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up js-yaml` (jusqu'à >= `4.3.0`)

### 2. `linkify-it` — 5.0.0 → **5.0.2**

- **CVE** : `CVE-2026-48801` (HIGH/DoS), `CVE-2026-59887` (HIGH/Unclassified)
- **Type** : DoS, Unclassified
- **Score max** : 15
- **Title** : linkify-it: linkify-it: Denial of Service via algorithmic complexity vulnerability
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up linkify-it` (jusqu'à >= `5.0.2`)

### 3. `liquidjs` — 10.27.0 → **10.27.1**

- **CVE** : `CVE-2026-55575` (HIGH/Unclassified)
- **Type** : Unclassified
- **Score max** : 15
- **Title** : LiquidJS: `pop` filter bypasses `memoryLimit` accounting that its array-filter siblings enforce
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up liquidjs` (jusqu'à >= `10.27.1`)

### 4. `golang.org/x/text` — v0.37.0 → **0.39.0**

- **CVE** : `CVE-2026-56852` (HIGH/DoS)
- **Type** : DoS
- **Score max** : 15
- **Title** : A norm.Iter can enter an infinite loop when handling input containing  ...
- **Source** : `go.mod`
- **Fix** : `go get golang.org/x/text@0.39.0` + `go mod tidy`

### 5. `vite` — 7.3.2 → **8.0.16**

- **CVE** : `CVE-2026-53571` (HIGH/Unclassified)
- **Type** : Unclassified
- **Score max** : 15
- **Title** : vite: `server.fs.deny` bypass on Windows alternate paths
- **Source** : `notification_center/package-lock.json`
- **Fix** : `pnpm up vite` (jusqu'à >= `8.0.16`)

### 6. `golang.org/x/crypto` — v0.46.0 → **0.52.0**

- **CVE** : `CVE-2026-39828` (HIGH/Unclassified), `CVE-2026-39829` (HIGH/DoS), `CVE-2026-39830` (HIGH/DoS), `CVE-2026-39831` (HIGH/Unclassified), `CVE-2026-39832` (HIGH/Unclassified), `CVE-2026-39835` (HIGH/DoS), `CVE-2026-42508` (HIGH/Unclassified), `CVE-2026-46597` (HIGH/DoS)
- **Type** : DoS, Unclassified
- **Score max** : 15
- **Title** : golang.org/x/crypto/ssh: golang.org/x/crypto/ssh: Unauthorized command execution via discarded SSH permissions
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/crypto@0.52.0` + `go mod tidy`

### 7. `golang.org/x/net` — v0.48.0 → **0.55.0**

- **CVE** : `CVE-2026-33814` (HIGH/DoS), `CVE-2026-42502` (MEDIUM/XSS), `CVE-2026-42506` (MEDIUM/XSS)
- **Type** : DoS, XSS
- **Score max** : 15
- **Title** : net/http/internal/http2: golang: golang.org/x/net: Go HTTP/2: Denial of Service via malformed SETTINGS_MAX_FRAME_SIZE frame
- **Source** : `telemetry/go.mod`
- **Fix** : `go get golang.org/x/net@0.55.0` + `go mod tidy`

### 8. `dompurify` — 3.4.0 → **3.4.11**

- **CVE** : `CVE-2026-49458` (MEDIUM/XSS), `CVE-2026-49459` (MEDIUM/XSS), `CVE-2026-49978` (MEDIUM/XSS), `CVE-2026-65898` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : dompurify: DOMPurify: Cross-site scripting due to improper sanitization of DOM nodes
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up dompurify` (jusqu'à >= `3.4.11`)

### 9. `echarts` — 5.6.0 → **6.1.0**

- **CVE** : `CVE-2026-45249` (MEDIUM/XSS)
- **Type** : XSS
- **Score max** : 12
- **Title** : Apache ECharts has a cross-site scripting (XSS) vulnerability
- **Source** : `console/package-lock.json`
- **Fix** : `pnpm up echarts` (jusqu'à >= `6.1.0`)


---

## 🟢 NOISE filtré (9 CVE)

<details>
<summary>Liste complète (6 groupes — clique pour déplier)</summary>

| Package | Installed | Fix | CVE count | Max score |
|---|---|---|---|---|
| `dompurify` | 3.4.0 | 3.4.7 | 1 | 6 |
| `js-yaml` | 4.1.1 | 4.2.0 | 1 | 6 |
| `markdown-it` | 14.1.1 | 14.2.0 | 1 | 6 |
| `vite` | 7.3.2 | 8.0.16 | 1 | 6 |
| `golang.org/x/crypto` | v0.46.0 | 0.52.0 | 4 | 6 |
| `golang.org/x/net` | v0.48.0 | 0.55.0 | 1 | 6 |

</details>


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
