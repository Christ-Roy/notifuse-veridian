# [NOTIFUSE] 🔴 P0 RÉPUTATION — classification provider par MX réel (nébuleuses Google/M365)

> **Sévérité** : 🔴 P0 — risque réputation email à grande échelle
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-14 par Robert (intuition juste, vérifiée par le lead sur la DB prospection prod)

## Le problème (vérifié, chiffré)

Le throttle/cap par classe de provider destinataire est **étanche** entre classes
(rate limiter keyé `integrationID|classe`, compteurs indépendants — OK). MAIS la
**classification est aveugle** : elle dérive la classe du **suffixe du domaine**
via une table statique (`veridian_provider_class.go` `veridianProviderDomainTable`),
et **tout domaine custom inconnu → `corporate`**.

**Aucun MX lookup** n'existe — ni dans Notifuse, ni dans Prospection (vérifié par
grep). Le commentaire du code dit "résolution MX faite EN AMONT" → mais personne
ne la fait. `custom_string_5` n'est jamais rempli avec une classe résolue par MX.

### Mesure sur la DB prospection prod (entreprises, 2026-06-14)
- 286k leads avec email. Répartition par suffixe :
  - **corporate (domaine custom) : 199 305 = ~70% de la base**
  - google 49 528 · freemail_fr 25 268 · microsoft 9 328 · yahoo_aol 2 876
- Échantillon MX des domaines "corporate" fréquents → une grande part est en
  réalité hébergée Google/M365 : `local.fr`→MS, `agence-horizonplus.fr`→MS,
  `refpro.fr`→MS, `medimmoconso.fr`→MS, `udevweb.co`→Google, `hrz.fr`→Google,
  `treatwell.fr`→Google (7/12 résolus = Google ou Microsoft).

### Conséquence réputation (le vrai danger)
Ces boîtes pro à domaine custom hébergées chez Google Workspace / Microsoft 365
sont classées `corporate` → reçoivent le débit `corporate` (rapide, pixel ON) →
**tapent physiquement les serveurs Google/Microsoft sans throttle ni protection**
→ on crame la réputation Google/Microsoft à grande échelle sans le voir. Le cap
"1/jour vers Google" ne protège RIEN tant que ces domaines ne sont pas reconnus
comme Google.

## Le fix (design)

Ajouter une **résolution MX** qui classe un domaine custom selon où il atterrit
VRAIMENT, au lieu de le jeter en `corporate` :
- MX contenant `google.com` / `googlemail.com` / `aspmx.l.google.com` → `google`
- MX contenant `protection.outlook.com` / `outlook.com` / `.mail.protection.outlook.com` → `microsoft`
- MX yahoodns → `yahoo_aol`
- sinon → `corporate` (vrai self-hosted / autre)

### Où câbler (à trancher par l'agent après reco — 2 options)
1. **À la lecture, côté Notifuse**, dans `ClassifyProviderClass` : si suffixe inconnu,
   faire un MX lookup AVEC CACHE (les MX d'un domaine changent rarement — cache
   persistant en DB ou in-memory TTL long). ⚠️ ne JAMAIS bloquer l'envoi sur un
   lookup lent : best-effort, timeout court, fallback corporate si échec.
2. **En amont, côté Prospection** (enrichissement) : résoudre le MX au moment du
   scrape/enrichissement et stocker la vraie classe (→ remplir `custom_string_5`
   à l'import dans Notifuse). C'est ce que le code Notifuse ASSUME déjà.
   ➜ probablement le PLUS PROPRE (lookup fait une fois à l'enrichissement, pas à
   chaque envoi), mais touche le repo Prospection (autre agent). À coordonner.

Reco lead : **les deux en défense en profondeur** — Notifuse fait un MX lookup
caché en fallback (pour ne jamais être aveugle même si l'amont oublie), Prospection
enrichit `custom_string_5` à l'import (cache global, performant). Commencer par le
fallback Notifuse (dans notre périmètre, protège immédiatement), ticket Prospection
en parallèle.

### ⚠️ Pièges
- **Resolver DNS** : le resolver local (`127.0.0.1#53`) est instable (vu en test :
  "communications error"). Utiliser un resolver fiable (1.1.1.1 / 8.8.8.8) avec
  timeout court + retry. Cache obligatoire (ne pas refaire 199k lookups à chaque envoi).
- **Best-effort strict** : un lookup MX qui échoue/timeout → fallback `corporate`,
  JAMAIS bloquer l'envoi (même contrat que le reste du throttle Veridian).
- **Cache** : ~des dizaines de milliers de domaines distincts → table de cache
  `domain → classe` (TTL semaines/mois), pas un lookup par mail.
- Étanchéité throttle déjà OK (ne pas y toucher) : le fix porte SEULEMENT sur la
  classification d'entrée.

## DoD
- [ ] MX lookup câblé (resolver fiable, cache, best-effort, timeout)
- [ ] Domaine custom Google/M365 → classé google/microsoft (pas corporate)
- [ ] Tests : domaine MX Google → google, MX outlook → microsoft, échec lookup → corporate (fallback), cache hit
- [ ] Mesure avant/après sur un échantillon : combien de "corporate" reclassés google/microsoft
- [ ] Non-régression : suffixes connus (gmail, etc.) → inchangés, zéro lookup (table statique prime)
- [ ] Ticket Prospection ouvert pour l'enrichissement amont (custom_string_5)
