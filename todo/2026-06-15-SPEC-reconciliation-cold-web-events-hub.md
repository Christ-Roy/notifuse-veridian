# [SPEC CROSS-APP] 🟡 Réconciliation cold↔web : events comportementaux → scoring prospect (dans le HUB)

> **Type** : SPEC large cross-app (Notifuse + Analytics + Hub + CRM). PAS du code immédiat.
> **Owner spec** : à router (Hub = porteur du réconciliateur ; Notifuse/Analytics = émetteurs)
> **Créé** : 2026-06-15 par Robert + lead Notifuse.
> **Décision Robert** : le réconciliateur vit **DANS LE HUB** (pas un micro-service de plus —
> `veridian-tunnel-de-vente` n'était qu'une ébauche locale de prototypage, pas une app destinée
> à vivre). Le Hub a une session/BDD qui fait la jointure + le scoring.

## Le besoin (Robert)
Corréler les events cold (Notifuse) avec les visites web (Analytics) pour AUGMENTER la confiance
dans l'identification d'un prospect. Ex : si le prospect a cliqué le lien cold (Notifuse) ET
qu'Analytics voit la page /audit hit dans le même laps de temps → confiance accrue → score
d'engagement plus élevé → prioriser ce prospect dans le CRM.

## Archi cible (tranchée)

```
Notifuse  ──émet events comportementaux (avec vid)──┐
                                                     ├─→  HUB (réconciliateur + BDD)
Analytics ──émet events web (avec vid)──────────────┘     jointure par vid → scoring → CRM Twenty
```

- **Réconciliateur = dans le Hub** (sa propre BDD). Le Hub a déjà `Tenant`/`TenantApp`/`CrmTenant`
  (mapping client→apps) + l'infra de scoring/réconciliation billing. On y ajoute la réconciliation
  COMPORTEMENTALE (distincte de la réconciliation billing existante `lib/sync/reconcile.ts`).
- **Notifuse + Analytics = ÉMETTEURS purs** : ils ne réconcilient rien, ne s'appellent JAMAIS
  entre eux (règle d'or : pas d'app→app direct, tout via le Hub).
- **Corrélation par ID DÉTERMINISTE (vid), pas temporelle floue** : la fenêtre temporelle est un
  fallback faible (2 prospects qui cliquent en même temps se mélangent). Le vid propagé partout
  rend la jointure certaine.

## ⚠️ LE VRAI PRÉREQUIS (ce qui rend ce ticket "large") : état tenant/workspace synchronisé

Robert (juste) : *"les app web n'ont pas forcément l'état des workspace et tenant synchronisé
entre eux"*. Pour que le Hub sache que workspace Notifuse X = tenant Analytics Y = client C, il
faut une synchro tenant cross-app FIABLE. État réel vérifié 2026-06-15 :

- Infra sync 3 niveaux **codée côté Hub** (`CONTRAT-HUB.md:3164+`) : discovery pull (N1) +
  webhook push (N2) + cron reconcile (N3, dry-run).
- **Côté NOTIFUSE, le socle est DÉJÀ LIVRÉ** (vérifié) :
  - ✅ N1 discovery : `GET/POST /api/users/by-email` routé + HMAC (veridian_discovery_handler.go).
  - ✅ N2 webhook push : émet déjà `tenant.provisioned/member_added/plan_changed/suspended/
    api_key_rotated/frozen...` via `VeridianWebhookEmitter` (câblé dans les services).
  - ⚠️ La doc Hub dit "0/4 apps livrent discovery" → soit périmée pour Notifuse, soit ce sont
    Analytics/CMS/Prospection qui manquent. À RÉCONCILIER : vérifier l'état RÉEL des 4 apps.
- ❌ **Analytics** : c'est là que ça coince probablement (provisioning via legacy, pas de sync
  fiable — cf giga-ticket décommission bridge). Le mapping tenant Notifuse↔Analytics n'est pas
  garanti tant qu'Analytics n'est pas branché proprement sur le Hub.

→ **Sans cette synchro tenant fiable (surtout côté Analytics), aucune réconciliation prospect
n'est possible.** C'est l'ÉTAGE 1, prérequis absolu. Lié aux 20 orphelins prod vus le 2026-06-15.

## Les 2 étages du chantier

### ÉTAGE 1 — SOCLE : synchro tenant/workspace cross-app fiable (prérequis)
- [ ] Auditer l'état RÉEL des 4 apps sur la sync 3 niveaux (Notifuse semble OK, vérifier les autres).
- [ ] Brancher Analytics proprement sur le Hub (dépend du décommission bridge legacy — ticket lié).
- [ ] Le Hub a une vue cohérente : 1 client Veridian → {workspace Notifuse, tenant Analytics, CRM}.

### ÉTAGE 2 — FEATURE : réconciliation events comportementaux (dans le Hub, après étage 1)
- [ ] **Identité prospect partagée (vid)** : un ID prospect unique propagé cross-produit. Source =
      Hub (il a déjà le modèle identité cross-app `hub_user_id`). À étendre aux PROSPECTS (pas que users).
- [ ] **Notifuse** : (a) propager le `vid` dans les liens de tracking `/t/` `/r/` (pour qu'Analytics
      le capte au hit de page) ; (b) émettre les events COMPORTEMENTAUX (`email.clicked/opened/replied`
      avec le vid) vers le Hub — RÉUTILISER le `VeridianWebhookEmitter` existant (les events tenant
      passent déjà par là ; ajouter les events comportementaux taggés vid). ❌ vid ABSENT du contact
      aujourd'hui (vérifié) = à ajouter.
- [ ] **Analytics** : capter le `vid` à l'arrivée sur la page + émettre `page.hit{vid}` vers le Hub.
- [ ] **Hub** : backend de réconciliation (BDD) qui joint par vid + score d'engagement + écrit CRM Twenty.

## Pour MAINTENANT : RIEN à coder
Le tunnel cold livré (envoi + bounce + reply + séquences) tourne SANS ça. La réconciliation
cold↔web est une feature de SCORING qui vient APRÈS, quand il y aura du volume + besoin de
prioriser les prospects chauds. Cette spec grave l'archi pour ne pas partir dans le mauvais sens.

## Tickets liés
- Décommission bridge Analytics legacy (prérequis pour brancher Analytics sur le Hub proprement).
- Ticket MIROIR à créer côté **veridian-hub/todo/** : le réconciliateur lui-même + identité prospect.
- 20 orphelins prod (symptôme de la synchro tenant bancale).
