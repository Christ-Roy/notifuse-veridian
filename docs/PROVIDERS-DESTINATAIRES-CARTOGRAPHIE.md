# Cartographie des providers destinataires — cold outreach Veridian

> **But** : savoir À COUP SÛR, pour toute base de leads, la VRAIE distribution des
> providers de réception (par MX réel, pas par suffixe de domaine), pour throttler
> le cold outreach sans cramer la réputation. Document de référence + accès data.
> Créé 2026-06-14. Source de vérité sur la composition des destinataires.

## TL;DR — pourquoi ce doc existe

Le throttle cold outreach Notifuse limite le débit **par classe de provider
destinataire**. Mais classer par **suffixe de domaine** (gmail.com→google, tout le
reste→corporate) est FAUX pour ~70% des leads B2B : un `@cabinet-dupont.fr` peut
être hébergé chez Google Workspace, Microsoft 365, OVH, etc. Envoyer vite vers ces
"corporate" = taper Google/Microsoft à plein régime sans le savoir = **réputation
grillée**. La vérité = le **MX réel** du domaine.

## Les sources de data (où sont les leads)

| Source | Accès | Volume | Colonnes email | MX déjà résolu ? |
|---|---|---|---|---|
| **Prospection PROD** (actuelle) | `ssh prod-pub` → `docker exec code-prospection-saas-db-1 psql -U postgres -d prospection` | table `entreprises` ~996k (286k avec email) | `best_email_normalized`, `best_email_type` | ❌ non — classification par suffixe seulement |
| **Scrape LOCAL** (= FUTURE PROD data) | `docker exec odh-scrape-db psql -U scraper -d scrape` (port local 5434) | table `extracted_v2` **7,8 M leads** | `email_principal`, `domain`, `web_provider` | ⚠️ partiel — voir ci-dessous |
| **email_verification** (LOCAL, dans scrape) | même DB `scrape` | **48k** emails vérifiés | `email`, `domain`, `mx_provider`, `mx_host`, `catch_all`, `smtp_code`, `result` | ✅ **OUI — pipeline MX + SMTP déjà en place** |
| `veridian-datahub` (local, port 5432) | `docker exec veridian-datahub psql -U veridian -d datahub` | VIDE (future prod, pas encore peuplée) | — | — |

➡️ **Le pipeline de vérification email existe DÉJÀ** dans `odh-scrape-db.email_verification`
(mx_provider, mx_host, catch_all, smtp result). NE PAS le réinventer — l'exploiter
et le scaler sur les 7,8M de `extracted_v2`. C'est l'asset clé pour la future prod.

## La distribution RÉELLE (par MX) — mesurée 2026-06-14

### Sur prospection PROD (286k leads avec email, résolution MX large via mxmap)
| Provider réel | % | Note |
|---|---|---|
| 🟦 google | 25,6% | Gmail public + Workspace |
| 🟠 **ovh** | **17,4%** | nébuleuse FR majeure, PAS dans les classes actuelles |
| 🟧 microsoft | 17,3% | (app n'en voyait que 3,3% par suffixe → **5× sous-estimé**) |
| corporate (vrai self-host) | 17,0% | ⚠️ hétérogène, voir danger ci-dessous |
| freemail_fr | 9,2% | |
| ionos | 5,4% | nébuleuse FR |
| gandi 2,0% · infomaniak 1,8% · yahoo_aol 1,0% · amen/zoho/o2switch/scaleway <1% | | |

**Delta démasquage** : l'app croit 69,5% "corporate" → réel 46,9%. **64 553 leads
(22,5%) "corporate" tapent en fait Google/Microsoft/OVH.**

### Sur scrape LOCAL (email_verification, 48k déjà vérifiés — confirme la prospection)
google 25,5% · ovh 17,1% · microsoft 13,1% · ionos 6,4% · orange 3,6% · infomaniak
2,5% · hostinger 2,1% · gandi 1,6% · yahoo 1,1% · puis anti-spam (mailinblack,
vadesecure, hornetsecurity, proofpoint...) · icloud · proton · zoho.
Qualité : **valid 47% · unknown 25% · invalid 20% · catch_all 5% · no_mx 4%**.

## ⚠️ Le danger : "corporate self-hosted" n'est PAS une zone safe pour le mass-mailing

Derrière les vrais "corporate self-host", on trouve (pondéré leads) :
- **Passerelles anti-spam pro** : `mailinblack.com`, `vadesecure.com`,
  `hornetsecurity.com`, `proofpoint`, `mimecast`, `sophos.com`, `retarus.com`,
  `trendmicro.eu`, `securemail.pro`, `security-mail.net`. → **conçues pour bloquer
  le cold outreach. Envoyer vite = blacklist + réputation grillée.** Débit
  ultra-conservateur OU exclusion du cold.
- **iCloud/Apple** (`icloud.com`) : gros provider à règles strictes, mal rangé en corporate.
- **ProtonMail, Zoho, mail.com** : providers à part entière.
- **Vrais micro-serveurs PME** (`mail.<domaine>`) : fragiles, graylisting facile.

➡️ Conclusion : la classe "corporate" actuelle = sac fourre-tout **à débit prudent
par défaut** (l'inverse de l'app aujourd'hui où corporate = débit rapide). NON, on
ne peut pas envoyer en masse aux self-hosted comme si c'était neutre.

## Classes finales — IMPLÉMENTÉES (Lot 4, 2026-06-14)

11 classes canoniques, throttlées séparément vu la distribution réelle
(`domain.VeridianAllProviderClasses()` = source de vérité Go) :

| Classe | Origine | Patterns MX (suffixe host, case-insensitive) |
|---|---|---|
| `google` | Gmail public + Workspace | `.google.com`, `.googlemail.com`, `.psmtp.com` |
| `microsoft` | Outlook + M365 | `.protection.outlook.com`, `.outlook.com`, `.office365.com` |
| `yahoo_aol` | Yahoo/AOL | `.yahoodns.net` |
| `freemail_fr` | FAI FR (host MX) | `.orange.fr`, `.sfr.fr`, `.free.fr`, `.laposte.net`, `.bbox.fr` |
| `corporate` | suffixe inconnu AVANT MX (rétrocompat) | — (jamais retourné par le MX, gardé pour les configs/tags existants) |
| `ovh` | nébuleuse FR ~17% | `.ovh.net`, `.ovh.com` |
| `ionos` | IONOS / 1&1 ~6% | `.ionos.*`, `.kundenserver.de`, `.1and1.com` |
| `apple_icloud` | iCloud/Apple | `.mail.icloud.com`, `.icloud.com`, `.apple.com` |
| `security_gateway` | anti-spam pro → débit ultra-prudent / quasi-exclusion cold | `.vadesecure.com`, `.mailinblack.com`, `.proofpoint.com`/`.pphosted.com`, `.mimecast.com`, `.hornetsecurity.com`, `.messagelabs.com`, `.cudasvc.com` (Barracuda), `.sophos.com`, `.retarus.com`, `.trendmicro.*`, … |
| `other_hoster` | hébergeurs propres | `.infomaniak.*`, `.gandi.net`, `.hostinger.*`, `.zoho.*`, `.proton*`, `.online.net`/`.scaleway.com`, `.titan.email`, … |
| `corporate_selfhost` | vrai self-host / MX inconnu (fallback prudent) | aucun pattern reconnu / échec lookup / NXDOMAIN |

Table de patterns versionnée : `veridianMXPatternTable` dans
`internal/domain/veridian_provider_class_mx.go`. **Gateways anti-spam testées EN
PREMIER** (elles frontent un MX d'entreprise). Pour étendre : ajouter le suffixe
host **OBSERVÉ en data** (pas deviné).

## Le script réutilisable (pour CHAQUE future DB)

**`veridian-prospection/scripts/mx_provider_map.py`** (+ `.README.md`) — résout les
MX d'une base de leads, classe par provider réel, sort un rapport distribution + un
CSV `domaine,nb_leads,mx,classe_reelle`. Resolver fiable (1.1.1.1/8.8.8.8, PAS le
local 127.0.0.1 instable), cache MX (`mx_cache.json`), traîne échantillonnée.

### Relancer sur une nouvelle DB
```bash
cd ~/Bureau/veridian-platform/veridian-prospection/scripts/
python3 mx_provider_map.py   # voir .README.md pour les paramètres DB/table/colonne
# sortie : mx_provider_map_data/prospection_report_<date>.txt + _classified_<date>.csv
```

### Pour la future prod (7,8M leads) — méthode recommandée
1. **Exploiter `email_verification` existant** (mx_provider déjà résolu) plutôt que
   relancer 7,8M lookups. Scaler ce pipeline sur tout `extracted_v2`.
2. Mesurer la distribution réelle via une simple agrégation SQL sur `mx_provider`
   (pas de DNS à refaire pour les déjà-vérifiés).
3. Pré-remplir la classe à l'import Notifuse (tag `custom_string_5`) depuis le CSV
   → classification exacte d'emblée, zéro lookup à l'envoi.

## Lien avec le code Notifuse
- Classification par suffixe (pure, hot path) : `internal/domain/veridian_provider_class.go`
  (`ClassifyProviderClass`).
- **Classification par MX RÉEL (LIVRÉE Lot 4)** : `internal/domain/veridian_provider_class_mx.go`
  (`VeridianMXClassifier` : suffixe connu sans lookup → MX caché → fallback
  `corporate_selfhost`, resolver DI 8.8.8.8/1.1.1.1, cache in-memory TTL 7j,
  best-effort timeout 2s). Câblé dans le worker via `veridianClassifyRecipient`
  (tag amont `custom_string_5` prime, sinon MX), utilisé par les deux gates
  throttle minute + daily cap.
- Le throttle/cap par classe est étanche (rate limiter keyé `integrationID|classe`) —
  le fix porte sur la CLASSIFICATION d'entrée, pas sur le throttle.
- Pré-remplissage massif à venir : tag `custom_string_5` posé à l'import par
  Prospection (ticket Prospection séparé) → zéro lookup à l'envoi pour les 7,8M.
