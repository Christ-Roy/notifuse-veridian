# Cold mailing : throttle par provider destinataire + webhooks events → CRM Twenty

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse
> **Créé** : 2026-06-10 (par l'agent tunnel-de-vente)
> **Réf archi** : `../veridian-tunnel-de-vente/CLAUDE.md` §1.3 + ticket
> `../veridian-tunnel-de-vente/todo/2026-05-31-archi-tunnel-outbound.md`

## Contexte

Robert a acté (2026-06-10) le tunnel outbound. Exigence clé pour ne pas
cramer l'infra mail : **viser précisément les infras destinataires** —
débit d'envoi contrôlé PAR PROVIDER receveur (Gmail ≠ Microsoft ≠ OVH ≠
Orange…), chacun ayant ses seuils de tolérance.

## À faire

### 1. Audit terrain (avant tout dev)

- [ ] Capacités actuelles de Notifuse : throttle global ? par domaine
      destinataire ? scheduling d'une campagne étalée sur N jours ?
- [ ] Events disponibles (sent, delivered, bounce, click, open) +
      mécanisme webhook sortant existant.
- [ ] Tracking URL (clics) : réécriture de liens dispo ? domaine de
      tracking configurable (PAS le domaine principal) ?

### 2. Dev (selon résultat audit)

- [ ] **Throttle par provider destinataire** : classification du
      destinataire par MX (gmail→google, outlook/hotmail→microsoft, etc.)
      + quota/jour configurable par classe. La re-qualification locale
      sait déjà résoudre les MX (réutilisable).
- [ ] **Webhook events → Twenty** : pousser sent/delivered/click/bounce
      sur la timeline du prospect (Person, clé = email normalisé).
      Pattern obligatoire : webhook temps réel **+ cron de
      réconciliation** (cf TUNNEL-DE-VENTE.md §3.3 — jamais webhook seul).
- [ ] **Tracking ouvertures** : NE PAS activer par défaut. Décision
      data-driven plus tard (pixel open dégrade la délivrabilité).

### 3. Relai d'envoi

Le cold ne part PAS du domaine principal veridian.site. Options à
instruire avec le skill `postfix` : relai self-hosted + **domaine d'envoi
dédié** (ex: veridian-audit.fr) avec SPF/DKIM/DMARC propres + warm-up
progressif. Bloquant avant tout envoi de masse.

## Garde-fous

- Opt-out fonctionnel sur chaque mail de campagne (lien désinscription) —
  obligatoire dès qu'on sort du 1-to-1 manuel.
- B2B uniquement, base de leads à source licite documentée.
