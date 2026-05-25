# 🔒 Veille CVE automatique — notifuse-veridian

> **Généré par** : `veridian-infra/.github/workflows/cron-trivy.yml`
> **Dernier run** : 2026-05-25 04:25 UTC
> **Run URL** : local-cron@mail.mybigserveur.local:2026-05-25
> **Image scannée** : `ghcr.io/christ-roy/notifuse-veridian:latest`
> **CVE bruts détectés** : 20 (avant filtrage)
> **Scoring** : `veridian-infra/ci/trivy-scoring.yml`

## TL;DR

- 🚨 **0 RED** — fix prioritaire
- 🔴 **0 HIGH** — action recommandée cette semaine
- 🟡 **1 MEDIUM** — récap, pas urgent
- 🟢 **9 NOISE** — annexe collapse

✅ **Rien d'urgent.** Quelques items MEDIUM à voir quand t'as 5 min.


---

## 🟡 MEDIUM — 1 CVE en 1 groupe

### 1. `libpq` + `postgresql17-client` — 17.9-r0 → **17.10-r0** *(base image OS)*

- **CVE** : `CVE-2026-6638` (HIGH/SQL injection)
- **Type** : SQL injection
- **Score max** : 22.5
- **Title** : SQL injection in PostgreSQL logical replication ALTER SUBSCRIPTION ... ...
- **Source** : `ghcr.io/christ-roy/notifuse-veridian:latest (alpine 3.21.7)`
- **Fix** : rebuild image avec base image patchée — `libpq` >= `17.10-r0`


---

## 🟢 NOISE filtré (9 CVE)

<details>
<summary>Liste complète (1 groupe — clique pour déplier)</summary>

| Package | Installed | Fix | CVE count | Max score |
|---|---|---|---|---|
| `libpq` | 17.9-r0 | 17.10-r0 | 9 | 9.0 |

</details>


---

## Comment réagir

1. **Tu fixes** → bump la dep / la base image, push sur `staging`. Le prochain tick (24h) confirme.
2. **Tu acks le risque** → ajoute un override dans [`veridian-infra/ci/trivy-overrides.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-overrides.yml) avec date d'expiration + raison.
3. **Tu ignores** → ne fais rien, le tick recréera ce fichier demain à l'identique.

> Tu peux **supprimer ce fichier librement**. Il sera recréé au prochain tick s'il reste des items à signaler. C'est l'idempotence qui garantit qu'on ne perd rien.

*Pour ajuster les règles : [`veridian-infra/ci/trivy-scoring.yml`](https://github.com/Christ-Roy/veridian-infra/blob/main/ci/trivy-scoring.yml). Ping infra-agent.*
