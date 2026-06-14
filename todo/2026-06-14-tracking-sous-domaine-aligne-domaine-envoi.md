# [NOTIFUSE/INFRA] 🟡 P1 — Liens de tracking sur sous-domaine aligné au domaine d'envoi

> **Sévérité** : 🟡 P1 délivrabilité cold
> **Owner** : agent notifuse-veridian (+ infra/DNS + skill postfix)
> **Créé** : 2026-06-14 par Robert. Envoi cold depuis `agences-veridian.fr`.

## Le besoin (Robert)

Le cold est envoyé depuis le domaine **`agences-veridian.fr`** (relai Postfix). Les
liens de tracking (pixel ouverture + redirect clics) doivent être sur un **sous-domaine
DU MÊME domaine** (ex `track.agences-veridian.fr` ou `t.agences-veridian.fr`), PAS sur
`notifuse.app.veridian.site`. + réduire la taille du lien si possible.

**Pourquoi (délivrabilité)** : un lien de tracking sur un domaine DIFFÉRENT du From =
signal anti-spam (mismatch domaine, le filtre voit un lien vers un domaine tiers
inconnu), casse la cohérence perçue avec l'alignement DKIM/DMARC du domaine d'envoi.
Aligner tracking ↔ From = pratique standard cold (Lemlist/Instantly font ça : custom
tracking domain par domaine d'envoi).

## État du code (vérifié)

- Liens générés : `{apiEndpoint}/t/{token}` (pixel, `template_compilation.go:385`) et
  `{apiEndpoint}/r/{token}` (redirect clic, l.365). Tokens **chiffrés** (anti
  pixel-blocker) — déjà assez courts (`/t/<token>`).
- ⚠️ **`apiEndpoint` est GLOBAL** (config système `API_ENDPOINT` / system_settings
  `api_endpoint`, cf config.go:307/582/629). → TOUS les liens pointent vers le domaine
  Notifuse, identique pour tous les workspaces/envois. C'est LE point à changer.
- Endpoints servis : `/t/` (handleEncryptedOpen), `/r/` (handleEncryptedClick),
  `/opens` — `internal/http/email_handler.go:104-109`. Ils répondent quel que soit le
  Host → un sous-domaine custom qui route vers Notifuse marchera tel quel.

## Travail à faire

### Lot A — Tracking domain configurable (par workspace ou par infra d'envoi)
- Permettre un **custom tracking domain** override de l'apiEndpoint pour la génération
  des liens `/t/` et `/r/`. Granularité : idéalement **par infra d'envoi** (EmailProvider,
  cohérent avec R2 rates par infra) ou a minima par workspace. Un domaine d'envoi
  `agences-veridian.fr` → tracking `track.agences-veridian.fr`.
- Côté code : `GetTrackingURL` / la génération pixel+redirect doit lire ce custom domain
  au lieu de l'apiEndpoint global. Fichier veridian_* (ne pas patcher
  template_compilation upstream plus que nécessaire — mais le champ TrackingSettings y
  est, voir comment l'alimenter par workspace/infra). Best-effort : pas de custom domain
  → fallback apiEndpoint global (non-régression).

### Lot B — Infra DNS + reverse proxy (skill infra/postfix)
- DNS : `track.agences-veridian.fr` (CNAME ou A) → pointe vers l'infra qui sert Notifuse
  (Traefik prod). Cert Let's Encrypt pour ce sous-domaine.
- Traefik : router `track.agences-veridian.fr` → conteneur Notifuse (mêmes routes /t/ /r/).
  Le handler répond déjà indépendamment du Host, donc juste le routage à câbler.
- ⚠️ Le sous-domaine de tracking doit être **distinct du domaine de la boîte d'envoi**
  au niveau MX (tracking = HTTP only, pas de MX), mais SOUS le même domaine racine pour
  l'alignement perçu. Vérifier qu'on ne casse pas le SPF/DKIM/DMARC d'agences-veridian.fr.

### Lot C — Réduire la taille du lien (si possible)
- Les tokens chiffrés actuels (`/t/<token>`) sont déjà compacts. Voir si le token peut
  être raccourci (encodage plus dense) sans casser le déchiffrement. Gain marginal —
  à évaluer après A+B, pas prioritaire. Un domaine court (`t.agences-veridian.fr`) aide
  déjà plus que raccourcir le token.

## DoD
- [ ] Custom tracking domain configurable (par infra ou workspace)
- [ ] Liens /t/ et /r/ générés sur track.agences-veridian.fr quand configuré (fallback global sinon)
- [ ] DNS + Traefik + cert pour le sous-domaine de tracking
- [ ] Vérifié : pixel + clic fonctionnent via le sous-domaine (E2E sink), opened_at/click posés
- [ ] SPF/DKIM/DMARC d'agences-veridian.fr non impactés
- [ ] (opt) token raccourci si gain réel

## Liens
- Throttle/config par infra (granularité cible) : `todo/2026-06-14-classification-mx-table-patterns-option-A.md`, [[project_cold_outreach_rates_par_infra]]
- Cold = Postfix : [[reference_cold_envoi_postfix_pas_brevo]]
- Skill postfix (domaine d'envoi, DKIM) : `~/.claude/skills/postfix/SKILL.md`
