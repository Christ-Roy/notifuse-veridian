# Pricing — source de vérité

> **Source unique cross-app** : `../veridian-hub/docs/PRICING-VERIDIAN.md`.
> Lire ce doc avant tout travail pricing/trial/paywall/feature gate.

**Philosophie figée par Robert 2026-05-21** : générosité maximale, **tout
illimité partout y compris Free** (emails, contacts, OAuth BYO, automation,
seats, custom domains, A/B testing, historique). L'app ne doit **jamais** être
défigurée par des limites visibles ou murs béton.

**Seules différenciations** :
- Free → durée 15j visibles (révélée à J+2 après 5 mails envoyés) puis paywall
- Business 99€ vs Pro 29€ → white-label custom (footer client custom)

**Flow trial** : signup silencieux → 5 mails déclenchent timer 2j serveur
invisible → J+2 bandeau trial 15j → ajout CB = cadeau **inconditionnel** de
30j → débit auto Pro à expiration si CB présente, sinon paywall lecture seule.
Détails complets : doc Hub.

### Interdits côté code

- Mur béton `402 Payment Required` sur une feature
- Compteur visible "il vous reste X mails / contacts / domaines"
- Menu grisé "🔒 Pro", pop-up "passez Pro pour faire ça"
- Branding obligatoire qui dégrade les emails du client
- Toute limite enforced sur contacts / OAuth / seats / automation / historique / custom domains / A/B
- Affichage du timer trial **avant J+2** (le timer 2j post-5mails reste invisible UI)

### Acceptable côté code

- Bandeau trial visible uniquement en phase 4+ (J+2)
- Compte à rebours pendant les 15j (puis 30j si CB)
- Lien Upgrade, paywall lecture seule à expiration
- White-label custom = différenciation Business+ uniquement

### État côté Notifuse (post-pivot 2026-05-21)

- **V37 lots 4b/4c/4d et 5** : tous annulés (aucun enforcement de dimensions)
- **Lot 4a A/B feature gate** : reverté (A/B gratuit pour tous, `featureGatedPaths` vide)
- **DefaultPlanLimits** : tout à `-1` / `true` sauf `FeatureWhiteLabel` (Business+ uniquement)

### Compteurs invisibles (télémétrie interne)

- `emails_sent_lifetime` : signal d'activité
- `activity_threshold_reached_at` (post-5e mail) : timestamp serveur consommé par Hub
- **Jamais exposés UI client** tant que phase 3 (J+2) n'est pas atteinte

### Tickets actifs reliés

- `todo/2026-05-21-trial-eligible-signal.md` (signal 5 mails Notifuse→Hub)
- `todo/2026-05-21-paywall-degraded-mode-soft-deleted.md` (UX dégradée)
- `todo/2026-05-20-pricing-plans-implementation.md` (V37)
- Hub : `2026-05-21-trial-state-machine.md`, `2026-05-21-stripe-webhook-orchestrator.md`

---

