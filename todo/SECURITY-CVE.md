# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-08-14 04:26 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-08-14
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 2 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **0 RED** — fix prioritaire
- 🔴 **1 HIGH** — action recommandée cette semaine
- 🟡 **1 MEDIUM** — récap, pas urgent
- 🟢 **0 NOISE** — annexe collapse


---

## 🔴 HIGH — 1 CVE en 1 groupe

### 1. `stdlib` — v1.25.12 → **1.27.0-rc.3**

- **CVE** : `CVE-2026-39821` (HIGH/Priv esc)
- **Type** : Priv esc
- **Score max** : 45
- **Title** : golang.org/x/net/idna: golang: net/http: golang.org/x/net/idna: Privilege escalation via incorrect Punycode label processing
- **Source** : `app/server`
- **Fix** : `pnpm up stdlib` (jusqu'à >= `1.27.0-rc.3`)


---

## 🟡 MEDIUM — 1 CVE en 1 groupe

### 1. `stdlib` — v1.25.12 → **1.27.0-rc.3**

- **CVE** : `CVE-2026-46600` (HIGH/DoS)
- **Type** : DoS
- **Score max** : 15
- **Title** : golang.org/x/net/dns/dnsmessage: golang.org/x/net/dns/dnsmessage: Denial of Service via invalid DNS record parsing
- **Source** : `app/server`
- **Fix** : `pnpm up stdlib` (jusqu'à >= `1.27.0-rc.3`)


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
