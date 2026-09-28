# Audit fork ↔ upstream Notifuse — 2026-09-28

> **Demandeur** : Robert Brunon, suite au constat du 28/09 sur un envoi réel `coldtunnel`
> (Message-ID `coldtunnel_...@agence-veridian.fr` en clair, Date en UTC) et au soupçon que
> le fork a été « défiguré » pour porter un bricolage de prospection externe au lieu d'une
> prospection native dans l'app.
> **Méthode** : lecture seule. `ssh bastion`, dépôt `~/Bureau/veridian-platform/notifuse-veridian`,
> remote `upstream` = `github.com/Notifuse/notifuse` (ajouté + fetché ce jour). Branche qui
> déploie : `veridian` (était périmée localement de 5 commits — resynchronisée sur
> `origin/veridian` avant l'audit, aucun code touché). Aucune config, aucun workspace Notifuse
> modifié. Périmètre secondaire lu en survol (lecture seule) : les scripts externes dans
> `~/Bureau/veridian-platform/acquisition-emailing/{batch,monitoring}` pour vérifier le
> soupçon de bricolage — pas d'audit complet de ce second dépôt.

---

## 1. Point de divergence

| Mesure | Valeur |
|---|---|
| Merge-base fork ↔ `upstream/main` | `d8f31f81` — 2026-05-31, juste avant la release **v32.2** |
| Version upstream au moment du fork | v32.2 (2026-05-31) |
| Version upstream actuelle | **v41.0** (2026-09-17) — `upstream/main` == tag `v41.0` exactement, rien d'untagué après |
| Commits propres au fork depuis le merge-base | **869** |
| Commits upstream non intégrés depuis le merge-base | **106** (9 releases : v32.3 → v41.0) |
| Dernier commit fork (`veridian`) | `06a776f5` — 2026-09-28 11:30 (le fix Message-ID de ce matin) |
| Dernier commit upstream | `7516397c` — 2026-09-17 (v41 — audit logs) |
| Diff cumulé fork vs merge-base | 950 fichiers, +168 423 / −25 331 lignes |
| Fichiers `internal/` | 428 touchés — dont **324 fichiers neufs préfixés `veridian_`** (convention systématique, code fork toujours isolé du code upstream, jamais mélangé dans le même fichier sauf modif ponctuelle documentée) |

**Lecture** : le fork n'est pas un dépôt qui a divergé au hasard. C'est un travail organisé
(convention de nommage stricte, ~140 fiches `todo/done/*.md` par lot, tests systématiques) —
mais 4 mois sans rebase sur un projet qui livre une release tous les 4-5 jours en moyenne
laissent un retard réel : audit logs (v41), licence BSL + 5 capacités payantes (v40), Zapier
(v39), Web Analytics natif (v38), **stop-on-reply automations natif** (v33) sont tous
manqués. Le point le plus important pour ce dossier : **le fork a reconstruit à la main,
mi-juin 2026, un stop-on-reply IMAP maison — la veille du jour où upstream livrait sa propre
version native (v33.0, 16/06)**. Détail en §3.

---

## 2. Inventaire des modifications du fork, par fonctionnalité

Regroupé par grappe fonctionnelle (fichiers `veridian_*` + fichiers upstream modifiés en place),
pas par commit.

### 2.1 Moteur cold outreach natif (gates anti-cramage, classes de provider, séquences)
**~27 600 lignes** — `internal/service/queue/veridian_{daily_cap,per_sender_cap,sending_window_gate,
content_hash_gate,provider_rate_limiter,provider_throttle,jitter,prefilter,excluded_class_gate}.go`,
`internal/domain/veridian_{provider_class,provider_class_mx,excluded_classes,sending_window,
daily_quota,content_hash,open_pixel}.go`, `internal/service/broadcast/veridian_{content_dedup,
pixel_resolver,sender_rotation,email_profile_rotation}.go`, `pkg/veridian_spintax/*`,
`pkg/veridian_deliverability/*`, console `veridian_cold_outreach_settings.tsx` +
`veridian_sending_profiles_ui.tsx` + `veridian_broadcast_rates_info.tsx`.

**But** : débits et plafonds par classe de destinataire (google/microsoft/yahoo_aol/freemail_fr/
corporate/…), exclusion de classe, fenêtre d'envoi, plafond/jour par destinataire ET par
expéditeur (warm-up IP), anti-doublon de contenu (spintax + hash), pixel de tracking
désactivable par classe, rotation d'expéditeurs. **Tout ceci est un ajout pur** : le moteur
upstream ne throttle que par intégration émettrice (`EmailProvider.RateLimitPerMinute`), sans
notion de classe destinataire ni de rotation. Chaque gate a sa doc de contrat en tête de
fichier, son test, et son ticket `todo/done/*`. **Config par workspace** (settings +
`broadcast.metadata`), **pas** codée en dur pour `coldtunnel` — vérifié : aucun `if
workspaceID == "coldtunnel"` dans le code non-test, seulement dans des fixtures de test.
Écran dédié dans la console (`SettingsSidebar` → Cold outreach) : c'est déjà pilotable en UI,
pour n'importe quel workspace.

**VERDICT : GARDER.** C'est exactement la brique qui manque à l'upstream pour du cold
propre, elle est saine (pas de hack workspace-spécifique), testée, et utilisable pour
`robertbrunon` sans rien changer au code.

### 2.2 Stop-on-reply / bounce IMAP maison
**Fichiers** : `internal/service/veridian_{reply_service,reply_consumer,bounce_consumer,
followup_prioritizer,cold_exit}.go`, `internal/service/queue/veridian_imap_{client,poller}.go`,
`internal/domain/veridian_{contact_reply,reply_detection,bounce_type,reply_stats}.go`,
`pkg/veridian_ndr/parser.go` (parsing NDR/bounce RFC 3464), `internal/http/
veridian_cold_simulate_handler.go` (endpoint de test E2E staging-only, auth HMAC, 503 hors
staging).

**But** : poller une boîte IMAP de retour (Lark), détecter réponse/bounce, sortir le contact
de l'automation en cours (exit-on-reply), sans dépendre d'un provider spécifique (Mailgun/SES).

**Constat upstream (v33.0, 16/06/2026)** : upstream a livré exactement cette fonctionnalité —
stop-on-reply natif, exit_on_reply sur les automations, matching strict par Message-ID/
In-Reply-To/References — **la veille du dernier commit du fork sur le sujet** (le fork a
livré son propre stop-on-reply le 15/06, upstream le 16/06). Différence structurante :
**le natif upstream ingère les réponses par WEBHOOK provider (Mailgun d'abord, puis SES)**,
pas par poll IMAP. Notre infra d'envoi est du SMTP relay générique (agences-veridian.fr),
pas Mailgun/SES : le webhook natif ne s'applique pas tel quel à notre setup actuel.

**VERDICT : À TRANCHER.** Deux chemins : (a) garder le poller IMAP maison (fonctionne avec
n'importe quel SMTP, déjà testé et en prod) ; (b) migrer l'infra d'envoi vers Mailgun/SES
pour hériter du stop-on-reply natif upstream (webhook, plus robuste, maintenu par eux) et
supprimer ~15 fichiers maison. **Reco : garder (a) pour l'instant** — changer de provider
d'envoi est un chantier lourd et un risque de délivrabilité en pleine rampe de chauffe ;
mais noter (b) comme cible si on migre un jour vers Mailgun/SES pour d'autres raisons.

### 2.3 Empreinte des en-têtes (fix du jour, 28/09)
**Fichiers** : `internal/domain/veridian_message_id.go` (nouveau), `internal/service/
smtp_service.go` (modifié), `internal/service/veridian_send_message_id.go`,
`internal/repository/message_history_postgre.go`. 2 commits (`a55cd9d9`, `06a776f5`),
389 lignes, 8 fichiers, **tests complets** (12 cas domain + 2 repository + 3 service bout-en-
bout, y compris un test qui prouve l'offset Europe/Paris contre `time.Now()`).

**But** : le Message-ID RFC822 sortant exposait `coldtunnel_<uuid>@...` (nom du workspace en
clair — aucun Thunderbird/Apple Mail ne fait ça, un vrai tell anti-spam) ; corrigé en UUID nu,
avec reconstruction du préfixe au moment du lookup de réponse (non-régressif, aucune migration
de données). Le `Date:` partait en UTC (heure du conteneur) au lieu de l'heure d'une boîte
française ; corrigé en `Europe/Paris` explicite, avec repli sur l'heure serveur si tzdata
absent.

**Comparaison upstream** : upstream ne pose que `X-Message-ID` (un header custom, jamais le
Message-ID RFC822 lui-même) et ne touche jamais au `Date:` (UTC par défaut de go-mail). **Le
fork est ici objectivement plus soigné que l'upstream** sur ce point précis.

**VERDICT : GARDER.** Fix générique (n'importe quel workspace SMTP en bénéficie), bien testé,
non lié à un hack coldtunnel — le nom `coldtunnel` n'apparaît que dans les données d'exemple
du commit (envoi réel observé), pas dans la logique.

### 2.4 Magic code / rate limit — la preuve du bricolage externe
**Fichier** : `internal/service/user_service.go`, fonction `GenerateMagicCodeForVeridian`
(ligne ~470), log `"veridian magic code rate limit exceeded"` (ligne 493).

C'est le endpoint que le **Hub** appelle en HMAC pour générer un magic-link self-contained
(auto-login cross-app) — fonctionnalité saine, documentée, utilisée par toute la plateforme.
**Mais** le rate limiter qu'il traverse est le même bucket `"signin"` que le login normal :
s'il est frappé en boucle sur l'identité `coldtunnel`, c'est qu'**un script externe
(hors app) redemande un token d'auth en boucle** au lieu de réutiliser une session — cohérent
avec `refill_notifuse_queue.py` / `send_followups.py` qui invoquent le CLI `notifuse` (donc
une authentification) à chaque exécution planifiée, sans gestion de session persistante côté
script.

**VERDICT** : le code Go n'est **pas** en cause (GARDER — c'est le mécanisme HMAC/Hub
standard). Le bruit dans les logs est un symptôme du bricolage externe (§4), pas un bug fork.

### 2.5 Provisioning Hub / HMAC / webhooks plateforme
**~24 500 lignes** — `internal/http/middleware/veridian_hmac.go`, `internal/service/
veridian_webhook_emitter.go`, `internal/http/veridian_{hub_discovery,autologin,sso}_handler.go`,
tout le cluster paywall/plan/billing/freeze (`veridian_paywall_*`, `veridian_plan_*`,
`veridian_billing_contract.go`, `veridian_freeze*`, `veridian_grant_unlimited.go`),
provisioning/cleanup (`veridian_provision_lock.go`, `veridian_workspace_drop.go`,
`veridian_orphan_db_gc.go`, `veridian_test_tenants_cleanup.go`, `veridian_idempotency*`).

**But** : c'est le standard SaaS multi-tenant Veridian (`docs/saas-standards.md §6.1/§7` dans
le monorepo) — signature HMAC-SHA256 sur les endpoints `/api/tenants/*`, émission d'events
vers le Hub, paywall/plan/freeze pilotés par le Hub, nettoyage des workspaces de test/orphelins.
Utilisé par **toutes** les apps Veridian (pas spécifique à Notifuse ni à coldtunnel), aucun
code n'y référence `coldtunnel` en dehors des tests.

**VERDICT : GARDER.** C'est l'intégration Hub multi-tenant, elle est saine et hors sujet
prospection — ne pas la toucher en nettoyant le cold.

### 2.6 Branding / OAuth / UI console Veridian
**Fichiers** : `console/src/components/veridian_{logo,brand_footer,brand_header_link,
oauth_buttons,welcome_toast,soft_delete_banner,paywall_modal}.tsx` + services API
(`veridian_402_interceptor.ts`, `veridian_plan.ts`, `veridian_hub_discovery.ts`).

**But** : habillage de marque (logo, footer, header), boutons OAuth Hub, gestion du 402
paywall côté front, toasts. Cosmétique/UX de la plateforme, sans rapport avec le cold.

**VERDICT : GARDER** (à faible enjeu — ne bloque rien, ne coûte rien à garder).

### 2.7 Durcissement sécurité (rate limit API, headers, idempotency)
`internal/http/middleware/veridian_{rate_limit,security_headers,idempotency}.go` +
tests. Rate limiting générique par IP/identité sur toute l'API, headers de sécurité,
idempotency-key sur les mutations. Standard, sain, sans lien coldtunnel.

**VERDICT : GARDER.**

### 2.8 IaC coldtunnel (`iac/coldtunnel/workspace.yaml`, `scripts/iac/*`)
**Fichier clé** : `iac/coldtunnel/workspace.yaml` — manifeste déclaratif idempotent
(intégrations SMTP, boîte retour IMAP, débits par classe, **séquence J0/J+3/J+7 avec
templates et automation native** commentée « Exit-on-reply/exit-on-bounce sont AUTOMATIQUES :
le gate ColdReplyChecker sort le contact »). Appliqué via `scripts/iac/notifuse-iac.sh`
(plan/apply idempotent contre l'API, secrets jamais en clair).

C'est en réalité **le bon design that Robert demande** — un workspace piloté par manifeste
versionné, avec automations natives (nodes email+delay), pas du bricolage. Le problème n'est
pas cet outil IaC : c'est que (a) le workspace visé est `coldtunnel` (enterprise, isolé de
`robertbrunon`) et (b) les templates `cold-step1/2/3` du manifeste sont restés à l'état de
squelette (`body_mjml: ""`) — **le vrai contenu et le vrai déclenchement sont passés par les
scripts Python** (§4) plutôt que par ce manifeste IaC + les automations natives qu'il décrit.

**VERDICT : GARDER l'outil (notifuse-iac.sh, le pattern manifeste), RETRANCHER la cible**
(re-pointer sur `robertbrunon`, ou supprimer une fois la bascule faite — cf. plan §6).

### 2.9 Code mort / tickets non finis liés au cold
`todo/2026-06-15-SPEC-reconciliation-cold-web-events-hub.md` (spec jamais implémentée —
réconciliation events web/Hub pour le cold) et une poignée de champs `body_mjml: ""` dans le
manifeste IaC ci-dessus.

**VERDICT : RETIRER** (ou clore explicitement) — un ticket spec ouvert depuis 3+ mois sans
suite est du bruit, pas un risque, mais il pollue `todo/` et laisse croire qu'un chantier est
en cours.

---

## 3. Fonctionnalités upstream à importer — top 5 (+ reste)

| # | Fonctionnalité upstream | Version | Utile pour prospection | Difficulté d'import |
|---|---|---|---|---|
| 1 | **Segments** (conditions timeline, goals négatifs, clics par lien/broadcast) | déjà en base (pré-fork) + v37/v36 | Oui — cible une audience sans script Python | **Faible** : c'est du code upstream jamais touché par le fork dans son cœur ; le retard (106 commits) porte surtout des fixes/durcissements dessus. Un `git merge upstream/main` classique en bénéficierait sans conflit majeur (segments non modifiés par le fork). |
| 2 | **Stop-on-reply natif (webhook Mailgun/SES)** | v33.0 | Oui, si on migre l'infra d'envoi | **Lourd** : suppose de changer de provider d'envoi (SMTP relay → Mailgun/SES) ; sinon inutilisable tel quel. Le fork a sa propre version IMAP (§2.2), qui reste la bonne option tant qu'on est sur SMTP générique. |
| 3 | **Rate limiting par intégration (déjà natif) + audit logs (v41)** | base + v41 | Oui pour la traçabilité (qui a créé/modifié quel broadcast/template, utile pour distinguer script externe vs action manuelle dans robertbrunon) | **Moyenne** : nouvelle table système + migration v41, mais isolée (aucun fichier touché par le fork dans ce chemin). Cherry-pick plausible après un rebase propre du socle `internal/migrations`. |
| 4 | **Contacts : import CSV, `contacts.upsert`/`lists.subscribe` avec retour enrichi, dédup JSON custom fields** | v39/v38 (deux petits fixes) | Oui — fiabilise l'import ODH | **Faible** : correctifs ciblés, aucun fichier touché côté fork. |
| 5 | **Web Analytics natif (Staminads rebuild), attribution, identify() lié au contact** | v38.0 | Utile en aval (suivi de conversion des prospects sur le site après clic) mais hors périmètre strict prospection | **Lourde** (feature massive, migration dédiée) — à ne considérer qu'après le chantier prospection, si jamais. |

**Autres à surveiller sans urgence** : rotation d'expéditeurs (**aucun équivalent natif
upstream** — la version fork §2.1 reste la seule, donc rien à importer ici, juste à garder),
retry des recipients échoués (v40, petit et utile), audit-log retention (v41), licence BSL 5
capacités payantes (v40 — **à lire avant tout merge**, ça change le modèle de licence des
versions ≥ v40 : self-host reste gratuit sauf 4e workspace / permissions restreintes / SES
tenant / traduction de template / SSO — sans impact prospection mais impact processus de merge
à anticiper).

**Difficulté d'import globale** : un vrai rebase de `veridian` sur `upstream/main` (869 vs 106
commits, 4 mois d'écart) est un chantier en soi — trop risqué en bloc. La voie réaliste est le
**cherry-pick ciblé** des correctifs isolés (imports/contacts, audit logs, retry) qui ne
touchent aucun fichier `veridian_*`, fonctionnalité par fonctionnalité, testé à chaque fois.

---

## 4. Pour faire tourner la prospection 100% native dans `robertbrunon`

| Capacité nécessaire | Statut |
|---|---|
| Séquence J0 → J+4 → J+10 avec délais | **Natif fork à garder** — automations upstream (nodes email + delay, préexistants au fork) suffisent ; le manifeste IaC `coldtunnel` en donne déjà le squelette exact (nodes `n_step1/wait1/step2/wait2/step3`) |
| Arrêt sur réponse | **Natif fork à garder** — `veridian_reply_service` + IMAP poller + `exit_on_reply` (§2.2). Fonctionne déjà, juste jamais branché sur `robertbrunon` |
| Rotation de 2 relais SMTP avec plafonds de chauffe | **Natif fork à garder** — `veridian_sender_rotation.go` + `veridian_email_profile_rotation.go` + `veridian_per_sender_cap.go` (warm-up par expéditeur). Aucun équivalent upstream, mais déjà écrit, testé, piloté par settings workspace |
| Un segment par cible | **Natif upstream** — segments existent depuis avant le fork, jamais modifiés par lui. Zéro travail : (re)configurer les conditions par cible dans l'UI |
| Import CSV des contacts ODH | **Natif upstream** — `contacts.import` existe nativement. Le script `import_audience_csv.py` (12,8 Ko, bricolage) réimplémente ce que l'endpoint natif fait déjà ; à remplacer par un simple appel API/CLI vers l'endpoint natif, pointé sur `robertbrunon` |
| Dédup contre l'historique d'envoi | **Natif fork à garder** — `veridian_daily_cap.go` (cap par destinataire, lit `message_history`, survit aux redémarrages) empêche déjà le recontact. Le script `build_clean_notifuse_queue.py` (15,8 Ko) refait une partie de ce travail côté Python — à vérifier lequel des deux fait réellement foi aujourd'hui (probablement le script, puisque le natif n'est câblé que sur `coldtunnel`) |
| Débits/plafonds par classe de destinataire | **Natif fork à garder** — §2.1, déjà en settings + UI |
| Rate limiting/plafond par provider d'envoi | **Natif upstream** (`EmailProvider.RateLimitPerMinute`) + **natif fork** (couche classe destinataire par-dessus) |

**Conclusion §4** : il **manque zéro brique technique**. Tout ce dont la prospection a besoin
existe déjà, natif, testé, dans le fork ou l'upstream sous-jacent. Ce qui manque est
uniquement **l'application** : provisionner ces réglages sur `robertbrunon` au lieu de
`coldtunnel`, remplir les templates MJML (aujourd'hui `body_mjml: ""` dans le manifeste IaC),
et activer les automations natives au lieu de laisser les scripts Python faire le travail à
côté.

---

## 5. CI sur runners GitHub hébergés (interdit, dépôt privé, Loi 7)

Convention déjà en place ailleurs dans ce même dépôt pour le label auto-hébergé :
`runs-on: [self-hosted, linux, x64]` (utilisé par `deploy-staging`, `e2e-headful-staging`,
`e2e-headful-prod`, `notifuse-console-e2e.yml`). Tout le reste ci-dessous doit basculer sur ce
même label.

| Fichier | Job | Runner actuel | Équivalent auto-hébergé |
|---|---|---|---|
| `.github/workflows/claude-code-review.yml` | `claude-review` | `ubuntu-latest` | `[self-hosted, linux, x64]` (workflow marqué DISABLED — vérifier s'il est réellement inactif avant de le migrer ou le supprimer) |
| `.github/workflows/claude.yml` | `claude` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/docker-manual.yml` | `build` (matrix amd64/arm64) | `ubuntu-latest` / `ubuntu-24.04-arm` | `[self-hosted, linux, x64]` (+ un runner arm64 si le parc en a un, sinon garder le build arm64 croisé via buildx sur x64) |
| `.github/workflows/docker-manual.yml` | `merge` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/docker-release.yml` | `build` (matrix amd64/arm64) | `ubuntu-latest` / `ubuntu-24.04-arm` | idem |
| `.github/workflows/docker-release.yml` | `merge` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/go.yml` | `unit-tests` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/go.yml` | `integration-tests` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/synthetic-monitoring.yml` | `smoke` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/synthetic-monitoring.yml` | `alert` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/tests-pending-debt-tracker.yml` | `report` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/unfreeze-after-revert.yml` | `unfreeze` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `test-mapping` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `console-build` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `console-unit` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `e2e-console-prod-smoke` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `compose-validate` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `migration-safety` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `workflow-lint` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `cve-scan` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `test-go` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `govulncheck-summary` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `trivy-fs` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `gitleaks` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `build` (image GitHub-hosted, commentaire l'assume déjà) | `ubuntu-latest` | `[self-hosted, linux, x64]` — nécessite un runner avec Docker/Buildx propre (pas de socket Docker partagé, cf. doctrine gitops-ci) |
| `.github/workflows/veridian-ci.yml` | `notify-promotion-needed` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `deploy-prod` | `ubuntu-latest` | `[self-hosted, linux, x64]` |
| `.github/workflows/veridian-ci.yml` | `rollback` | `ubuntu-latest` | `[self-hosted, linux, x64]` |

**Déjà conformes** (à ne pas toucher) : `deploy-staging`, `e2e-headful-staging`,
`e2e-headful-prod` (`veridian-ci.yml`), et le job unique de `notifuse-console-e2e.yml`.

**27 jobs sur ubuntu-latest/ubuntu-24.04-arm à basculer.** C'est le plus gros chantier CI du
dépôt — hors scope prospection mais c'est une exposition Loi 7 active sur un dépôt privé
Veridian, à traiter par le skill `gitops-ci` dès que possible (le skill sait déjà câbler le
label `[self-hosted, linux, x64]`, cf. les jobs qui l'utilisent déjà ici).

---

## 6. Plan recommandé, étape par étape

Chaque étape est réversible et mesurable indépendamment. Part de l'existant réel
(265 mails en file dans `coldtunnel`, 8/jour) vers 100% natif dans `robertbrunon`.

### Étape 0 — Ne rien casser tout de suite (0 effort, déjà fait)
Le fix Message-ID/Date de ce matin (§2.3) est bon et déjà sur `veridian`. Rien à faire.

### Étape 1 — Vider la file `coldtunnel` en cours sans rien changer de structurel
**Effort : 0 (déjà en cours).** Laisser tourner les 265 mails à 8/jour (~33 jours) le temps de
préparer la bascule. Ne pas relancer de nouveau batch Python pendant qu'on construit la
version native en parallèle sur `robertbrunon` — sinon on aura deux pipelines concurrents.

### Étape 2 — Provisionner le cold natif sur `robertbrunon`
**Effort : 0,5 à 1 jour.** Adapter `iac/coldtunnel/workspace.yaml` → un manifeste
`iac/robertbrunon/cold.yaml` pointé sur le workspace `robertbrunon` (id existant), débits/caps
par classe repris du preset actuel (déjà réglés prudents), rotation des 2 relais SMTP déclarée
comme 2 intégrations `sending_integrations`, plafond de chauffe posé via `veridian_per_sender_cap`.
Appliqué via `scripts/iac/notifuse-iac.sh plan/apply` (déjà l'outil, déjà testé sur
`coldtunnel`). **Testable** : `plan` en dry-run affiche le diff avant tout appel API réel.

### Étape 3 — Écrire les 3 templates MJML (J0/J+4/J+10) et le segment cible
**Effort : 0,5 jour** (skill `notifuse-templates`, charte Veridian) + définir le segment (une
condition simple : ex. liste importée + pas encore contacté). Remplace le
`body_mjml: ""` du manifeste.

### Étape 4 — Import CSV natif + dédup native, en parallèle du script Python (double-run de contrôle)
**Effort : 0,5 jour.** Importer un petit lot ODH (50-100 contacts) via `contacts.import` natif
dans `robertbrunon`, laisser le daily-cap gate faire la dédup, comparer le résultat à ce
qu'aurait fait `build_clean_notifuse_queue.py` sur le même lot. **Mesure** : même liste finale
= le natif est prêt à remplacer le script.

### Étape 5 — Activer la séquence native (email→delay→email→delay→email) avec exit-on-reply
**Effort : 0,5 jour.** Nodes automation standard + `exit_on_reply` (poller IMAP déjà branché
sur la boîte Lark, juste à ajouter `robertbrunon` comme second consommateur — ou basculer la
boîte de `coldtunnel` vers `robertbrunon` si une seule boîte de retour existe). **Testable**
sans envoyer de vrai mail : `veridian_cold_simulate_handler.go` (§2.2) injecte une fausse
réponse et vérifie que le contact sort de l'automation — exactement fait pour ça.

### Étape 6 — Pilote croisé : 20 contacts en parallèle, natif `robertbrunon` vs script `coldtunnel`
**Effort : 1 jour + observation sur 10 jours** (le temps d'un cycle J0→J+10). Compare taux
d'ouverture, taux de réponse, aucune anomalie de délivrabilité (SPF/DKIM déjà en place,
inchangés). **Critère de bascule** : le natif produit un comportement identique ou meilleur.

### Étape 7 — Bascule complète : tout nouveau contact part par le natif `robertbrunon`
**Effort : 0 (interrupteur)** une fois l'étape 6 validée. `refill_notifuse_queue.py` et
`send_followups.py` ne sont plus **lancés** (pas supprimés tout de suite — garder en secours
1-2 semaines).

### Étape 8 — Geler puis retirer `coldtunnel` et les scripts
**Effort : 0,5 jour.** Une fois la file `coldtunnel` vidée (fin de l'étape 1, en parallèle) et
2 semaines sans incident sur le natif : passer `coldtunnel` en workspace suspendu (Hub), puis
supprimé. Retirer `batch/refill_notifuse_queue.py`, `batch/send_followups.py`,
`batch/build_clean_notifuse_queue.py`, `batch/import_audience_csv.py` du dépôt
`acquisition-emailing` (ou les archiver, doctrine §9 : on n'efface pas sans raison, on
archive daté). Retirer `iac/coldtunnel/` du fork une fois `iac/robertbrunon/` opérationnel.

### Étape 9 — Nettoyage fork ↔ upstream
**Effort : 2-3 jours, étalé.** Cherry-pick des correctifs upstream isolés qui ne touchent
aucun fichier `veridian_*` (imports contacts, audit logs, retry recipients échoués — §3),
un par un, testés à chaque fois. Ne PAS tenter un rebase global (869 commits fork, trop de
divergence pour un seul chantier) — le faire par petites vagues thématiques, jamais en bloc.

### Étape 10 — CI sur runners auto-hébergés
**Effort : 1-2 jours**, via skill `gitops-ci` — les 27 jobs du §5, migration mécanique vers
`[self-hosted, linux, x64]`, en commençant par les jobs `veridian-ci.yml` qui gardent déjà le
pattern à côté (`deploy-staging` etc.) comme modèle. Indépendant du reste du plan, peut se
faire en parallèle des étapes 2-9.

---

## Annexe — ce qui n'a PAS été audité en détail (hors scope de cette mission)

- Le dépôt `acquisition-emailing` dans son ensemble (seulement `batch/` et `monitoring/`
  survolés pour confirmer le mécanisme de bricolage — pas de revue ligne à ligne, pas de
  revue de `bridge/`, `tunnel-e2e/`, `infra-mail/`).
- Le contenu réel des 265 mails en file (aucune lecture de données de workspace).
- Les 106 commits upstream un par un (lus via CHANGELOG.md, pas via diff de code).
