# Cap journalier par couple (infra émettrice × classe destinataire) — warm-up multi-domaine

> **Sévérité** : 🔴 P1 (tier HAUT — envoi cold core)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-18
> **Demandeur** : agent tunnel-de-vente (pour Robert)

## Contexte business

Doctrine de warm-up actée par Robert 2026-06-18 (cf.
`veridian-tunnel-de-vente/docs/TUNNEL-DE-VENTE.md` §7.3bis) : **la seule limite
qui compte pour le warm-up = `1 infra émettrice (IP + domaine) → 1 classe de
provider destinataire = N/jour`**, N=1 au démarrage, monté à la main selon KPI.

L'unité de réputation = **IP + domaine d'envoi** = une `EmailProvider` (« infra
émettrice »). Les N adresses d'un même domaine partagent cette réputation
(round-robin sous le plafond commun de l'infra). Donc le plafond doit être keyé
**par infra émettrice**, pas globalement par workspace.

## Le problème (diagnostic terrain déjà fait)

Le cap journalier par classe (`veridian_provider_class_daily_cap`,
`internal/service/queue/veridian_daily_cap.go`) compte via :

```
internal/domain/message_history.go:227
  CountSentSinceForDomains(ctx, workspaceID, domains []string, exclude bool, since time.Time) (int, error)
internal/repository/message_history_postgre.go:1376
  → COUNT … WHERE classe(contact_email par domaines) AND sent_at >= minuit
```

**Aucun filtre sur le sender / l'infra émettrice.** Le compteur est donc
**mutualisé sur tout le workspace**, toutes infras d'envoi confondues.

Conséquence :
- **Aujourd'hui** : 1 seule infra cold (`agences-veridian.fr`) → le cap classe
  vaut de facto « 1 IP+domaine → 1 provider/jour ». ✅ Marche **par accident**.
- **Dès l'ajout d'une 2e infra** (2e domaine d'envoi — recommandé pour scaler le
  cold, cf. skill `postfix`) : les deux infras **partagent le compteur de
  classe** → « 1/jour **par infra** » est violé, elles se marchent dessus.

## Demande précise

Étendre le cap journalier **par classe** pour qu'il compte **par infra
émettrice** (couple sender-infra × classe). Le cap **par destinataire**
(`CountSentSinceForContact`) reste inchangé (déjà la bonne granularité).

### Ce qui aide énormément : la colonne existe déjà (V53)

`message_history.veridian_sender_email` (migration **V53**, peuplée à l'envoi
depuis `entry.Payload.FromAddress`, index partiel
`(veridian_sender_email, sent_at) WHERE NOT NULL`) est **déjà là**. Pas de
nouvelle migration nécessaire — juste un COUNT supplémentaire qui croise
`veridian_sender_email` (attribution infra) ET le filtre domaines (classe).

⚠️ Nuance « infra » vs « sender_email » : l'unité de réputation est l'infra
(IP+domaine), pas l'adresse exacte. Deux choix d'implémentation, à trancher par
l'agent Notifuse selon ce qui est le plus propre :
- **(A) par domaine du sender** : `lower(split_part(veridian_sender_email,'@',2))`
  = le domaine émetteur = l'infra réputationnelle. Le plus fidèle à la doctrine
  (les 3 adresses du domaine comptent ensemble). **Recommandé.**
- **(B) par adresse exacte** : réutilise tel quel l'index/COUNT V53
  (`CountSentSinceForSender`). Plus simple mais compte chaque adresse séparément
  → ne reflète pas « les 3 adresses partagent la réputation ». À éviter sauf si
  (A) coûte un index neuf jugé non rentable.

Si (A) → vérifier qu'un index sur `(lower(split_part(veridian_sender_email,'@',2)), sent_at)`
est utile (sinon le filtre `sent_at` indexé + volume cold faible suffit ;
décision perf au cas par cas, cf. note v49.go « COUNT live, pas d'agrégat »).

### Implémentation attendue (esquisse, l'agent tranche le détail)

1. **Repo** : nouvelle méthode
   `CountSentSinceForDomainsAndSenderDomain(ctx, workspaceID, domains, exclude,
   senderDomain, since)` (option A) — COUNT classe-par-domaines ET
   `lower(split_part(veridian_sender_email,'@',2)) = senderDomain`. Décorateur
   quota passthrough + mock régénéré.
2. **Gate** : `veridianDailyCapGate` / `veridianResolveDailyCaps`
   (`veridian_daily_cap.go`) — le cap classe résolu par infra utilise le nouveau
   COUNT, en dérivant `senderDomain` du sender de l'entrée
   (`entry.Payload.FromAddress`). `provider`/sender absent → fallback au COUNT
   workspace-global actuel (non-régression legacy). Best-effort strict inchangé
   (erreur COUNT = pass).
3. **Cascade config inchangée** : `broadcast → infra (EmailProvider) → workspace`.
   Le cap-classe posé AU NIVEAU INFRA (`EmailProvider.VeridianProviderClassDailyCap`)
   prend alors tout son sens : il plafonne CETTE infra vers la classe.
4. **Dégradation MX assumée** (déjà documentée) : `VeridianDomainsForClass`
   renvoie [] pour les classes MX → ce chemin ne les enforce pas ; le throttle
   minute par classe protège le hot path. Pas un régression, juste à re-noter.

## Tests exigés (tier 🔴 = E2E on-premise obligatoire avant promo prod)

- **Unitaire** : 2 infras (domaines émetteurs distincts) tapant la même classe
  → chacune plafonnée à son cap indépendamment (l'une au quota ne bloque pas
  l'autre) ; 1 infra au quota → skip-and-reschedule (contrat inchangé) ;
  non-régression : config absente = no-op ; legacy (sender vide) = fallback
  workspace-global.
- **E2E sink** (réutiliser `scripts/e2e/cold-garde-fous.sh` + extension
  `cold-simulate` `class_cap_decision`) : provisionner 2 intégrations SMTP→sink
  avec 2 domaines émetteurs, prouver que le compteur de classe est bien
  **séparé par infra** (mode `class_cap_decision` doit accepter un param
  sender/domaine, à étendre). 0 mail externe.

## Impact côté tunnel de vente

Bloquant pour le **multi-domaine d'envoi** (étape de scaling cold). Tant qu'on
reste à 1 infra (`agences-veridian.fr`), le comportement actuel est correct →
**pas un bloquant pour le pilote / la phase 0**. À livrer avant d'ajouter un 2e
domaine d'envoi.

## Réaligner le preset warm-up dans la foulée

Une fois le cap par infra câblé, réaligner `VERIDIAN_WARMUP_PRESET`
(`console/src/services/api/workspace.ts`) sur la doctrine §7.3bis :
- **retirer** `veridian_per_sender_daily_cap` (per-sender individuel = faux
  modèle réputationnel, cf. doctrine) ;
- cap classe destinataire = **1** (déjà le cas) posé **au niveau infra** pour
  qu'il plafonne par couple infra×classe ;
- garder `veridian_per_recipient_daily_cap: 1` + rates 0.5/min + jitter +
  fenêtre ouvrable.
