# Rampe de débit cold — workspace `coldtunnel`

> Comment on augmente le volume d'envoi cold **sans cramer les domaines**, cran par
> cran, avec un critère de passage mesurable à chaque cran. Ce document est la
> vérité de la rampe : ce qui est actif, ce qui est le prochain cran, et ce qui
> autorise à le franchir. Il se met à jour à chaque cran franchi.
>
> Dernière mise à jour : **2026-08-24**.

---

## 1. D'où vient le débit

Le plafond quotidien n'est **pas** un réglage unique : c'est le produit de la
configuration de chaque **infra d'envoi** (une intégration SMTP + une boîte
expéditrice) déclarée sur le workspace.

Le paramètre qui porte le plafond par infra est `veridian_profile_daily_cap`
(5 aujourd'hui). Le débit total ≈ `nombre d'infras actives × profile_daily_cap`.
S'y ajoutent des gates qui ne se contournent pas et qu'on ne désarme jamais :

| Gate | Rôle |
|---|---|
| `veridian_sending_window` | jours et heures ouvrables (lun–ven, 10 h–16 h, Europe/Paris) |
| `veridian_per_recipient_daily_cap` | 1 mail par jour et par adresse (anti-harcèlement) |
| `veridian_provider_class_daily_cap` | plafond par classe de messagerie destinataire |
| `veridian_provider_class_rates` | débit par minute et par classe |
| `veridian_excluded_provider_classes` | classes qu'on ne démarche pas du tout |
| `veridian_anti_hash_enabled` | pas deux fois le même contenu dans la fenêtre (168 h) |
| `veridian_jitter_pct` | 35 % de dispersion sur les délais (pas de cadence machine) |

**Toujours vérifier une hypothèse de débit par un DRY-RUN avant d'y toucher :**
`notifuse --env staging verify coldtunnel` interroge les prédicats réels des
gates, sans ouvrir un seul SMTP.

---

## 2. État au 2026-08-24

### Infras ACTIVES

| Infra | Expéditeur | Domaine | Plafond | Réception des réponses |
|---|---|---|---|---|
| `nord-propre-1` | robert.brunon@messagerie-nord-776.fr | messagerie-nord-776.fr | 5 / jour ouvré | boîte relevée par l'intégration IMAP « Nord DSN » |
| `relai-nord-2` | r.brunon@messagerie-nord-776.fr | messagerie-nord-776.fr | 5 / jour ouvré | réception prouvée (routage Cloudflare vers la boîte Veridian) ; **pas** relevée par Notifuse |

Débit courant : **10 / jour ouvré**. Les deux infras portent une configuration
cold **strictement identique** (fenêtre, plafonds par classe, débits, exclusions,
plafond profil 5, jitter 0,35, anti-hash 168 h) — seule l'adresse d'envoi diffère.

### Infras déclarées mais NON actives

| Infra | Expéditeur | Utilisable ? |
|---|---|---|
| `relai-nord-3` | contact@messagerie-nord-776.fr | **Oui**, aux conditions du § 5 |
| `relai-agence-2` | r.brunon@**agence**-veridian.fr | 🔴 **NON — interdit** |
| `relai-agence-3` | contact@**agence**-veridian.fr | 🔴 **NON — interdit** |

> 🔴 **`agence-veridian.fr` au SINGULIER n'a aucune réception exploitable.** Six
> mails en sont partis le 2026-08-11 : toute réponse d'un prospect tombe dans un
> trou noir. Le domaine d'envoi légitime est `agences-veridian.fr` au **pluriel**,
> et ses trois boîtes servent déjà la jambe A (workspace `robertbrunon`) — les
> réutiliser ici casserait la comptabilité des plafonds par expéditeur, qui est
> tenue **par workspace**. Ces deux infras restent donc hors rampe tant qu'on ne
> leur a pas donné des boîtes à elles, sur un domaine qui reçoit.

**Conséquence sur le plafond atteignable** : la rampe plafonne à **15 / jour**
(3 infras sur le domaine nord), pas 25. Aller au-delà demande un **nouveau
domaine d'envoi** chauffé depuis zéro — c'est un chantier, pas un cran.

---

## 3. La rampe

Un cran tous les **3 jours ouvrés**, et seulement si les critères du § 4 sont
tenus. Jamais deux crans le même jour, jamais de saut direct au plafond : c'est
la montée brutale qui grille un domaine, pas le volume en lui-même.

| Cran | Infras actives | Débit | Date de franchissement | Critère vérifié |
|---|---|---|---|---|
| 0 | `nord-propre-1` | 5 / j | (état antérieur) | — |
| **1** | + `relai-nord-2` | **10 / j** | 2026-08-24 | 2 bounces durs sur 261 envois (0,77 %), 0 plainte — mesurés à la main (§ 4 bis) |
| 2 | + `relai-nord-3` | 15 / j | ≥ 2026-08-27 | **bloqué** tant que la remontée des bounces n'est pas réparée (§ 4 bis) |
| 3 | nouveau domaine | > 15 / j | non planifié | demande un warm-up de domaine complet |

---

## 4. Critères de passage au cran suivant

Tous doivent être vrais, **mesurés**, sur la fenêtre écoulée depuis le cran
précédent. Un critère qu'on ne peut pas mesurer compte comme **non tenu** : une
absence de trace n'est pas une absence de problème.

1. **Bounces < 2 %** des envois de la période, et **aucun bounce dur** (5.x.x)
   sur une adresse valide.
2. **Zéro plainte** (`complained_at`).
3. **Réponses relevées** : la boîte de chaque expéditeur actif est effectivement
   polée par une intégration IMAP. Une boîte dont on n'a jamais vu arriver un
   message est réputée **non fonctionnelle** jusqu'à preuve du contraire.
4. **Aucune inscription en liste noire** du domaine ni de l'IP d'envoi.
5. **Les gates n'ont rien bloqué anormalement** : un `would_be_capped` massif au
   DRY-RUN signale que le cran précédent n'était déjà pas absorbé.

Mesure des trois premiers, sur la base du workspace (lecture seule) :

```sql
SELECT count(*)                       AS envois,
       count(bounced_at)              AS bounces,
       count(complained_at)           AS plaintes,
       round(100.0*count(bounced_at)/nullif(count(*),0), 2) AS taux_bounce_pct
FROM message_history
WHERE sent_at > now() - interval '3 days';
```

**Si un critère n'est pas tenu : on ne monte pas, on redescend d'un cran.**

---

## 4 bis. 🔴 La remontée des bounces est aveugle — à réparer avant le cran 2

État mesuré le 2026-08-24, et il faut le lire deux fois avant de monter le débit :

- `message_history` du workspace `coldtunnel` : **261 envois, `bounced_at` = 0**.
- La boîte de retour du relai nord contient pourtant **2 avis de non-remise durs
  bien réels** (13/08, `550 5.1.1 Recipient address` pour `contact@spinnaker-one.fr`
  et `contact@proxiferm.com`).

Autrement dit : **le compteur de bounces de Notifuse ne peut pas devenir non nul.**
C'est le pire visage d'un indicateur — il est au vert parce qu'il n'a pas de
données, pas parce que tout va bien. Le critère nº 1 du § 4 (« bounces < 2 % »)
est donc, en l'état, **non mesurable depuis l'outil** ; le chiffre de 0,77 %
ci-dessus a été relevé à la main dans la boîte de retour, pas produit par Notifuse.

Conséquences :

1. Le cran 1 (10 / jour) est franchi sur une mesure manuelle, assumée comme telle.
2. **Le cran 2 reste fermé** tant que la boucle de bounce ne remonte pas dans
   `message_history` — doubler encore le volume avec un détecteur muet, c'est
   exactement la faute qu'on cherche à éviter.
3. À réparer : l'intégration IMAP « Nord DSN » relève `robert.brunon@`, mais les
   avis de non-remise de `relai-nord-2` arriveront dans la boîte de `r.brunon@`.
   Il faut donc et réparer la boucle existante, et la brancher sur chaque boîte
   expéditrice active.

> Un dispositif de sécurité qu'on n'a jamais vu se déclencher doit être considéré
> comme non fonctionnel jusqu'à preuve du contraire.

---

## 5. Comment franchir un cran

1. **Prouver la réception de la boîte expéditrice** avant tout.

   > ⚠️ **Pas de mail de test fabriqué depuis un domaine d'envoi Veridian**
   > (décision Robert, 2026-08-24 : on ne fabrique plus de trafic sur ces
   > domaines, la réputation prime). La preuve se fait en LECTURE :
   > - la règle de routage Cloudflare Email Routing de l'adresse existe et est
   >   activée, et sa destination est une boîte Veridian réellement relevée ;
   > - ou un message déjà arrivé sur cette adresse est retrouvé dans la boîte de
   >   destination.
   >
   > Si aucune de ces deux preuves n'est disponible : **on n'active pas**. C'est
   > exactement l'erreur `agence-veridian.fr`, et elle coûte des réponses de
   > prospects perdues.

   Recette de lecture (ne fabrique aucun trafic) :

   ```bash
   # la destination du routage de l'adresse (token Cloudflare avec le droit
   # Email Routing en lecture) :
   curl -s -H "Authorization: Bearer $CF_TOKEN" \
     "https://api.cloudflare.com/client/v4/zones/$ZONE/email/routing/rules" \
     | jq -r '.result[] | "\(.enabled)\t\(.matchers[0].value)\t→ \(.actions[0].value[0])"'
   # et, côté relai, ce qui est effectivement arrivé dans la boîte :
   ssh prod "docker exec \$(docker ps --format '{{.Names}}' | grep '^dms-') \
     sh -c 'find /var/mail/messagerie-nord-776.fr/<boite> -type f \
       \\( -path \"*cur*\" -o -path \"*new*\" \\) | wc -l'"
   ```

2. **Porter la configuration cold** de `nord-propre-1` sur la nouvelle infra
   (fenêtre d'envoi, plafonds par classe, débits, exclusions, plafond profil,
   jitter, anti-hash). Une infra sans config cold n'a **aucun** garde-fou.

   ```bash
   notifuse integrations:cold coldtunnel --id <nouvelle_infra> \
     --rates '<rates de nord-propre-1>' \
     --daily-cap '<caps de nord-propre-1>' \
     --per-recipient-cap 1 \
     --exclude google,microsoft,yahoo_aol,freemail_fr,apple_icloud,ionos,security_gateway
   ```

3. **Brancher le relevé des réponses** : ajouter l'intégration IMAP de la
   nouvelle boîte, sans quoi ni les bounces ni les réponses ne sortent le
   contact de la séquence.

4. **DRY-RUN** : `notifuse --env staging verify coldtunnel` — aucun mail envoyé.

5. **Mettre à jour le tableau du § 3** (date + critère vérifié) et commiter.

---

## 6. Ce qu'on ne fait jamais

- Passer de 5 à 25 d'un coup parce que « les infras sont prêtes ».
- Activer une infra dont la boîte expéditrice ne reçoit pas.
- Réutiliser dans un workspace une boîte déjà utilisée par un autre : les
  plafonds par expéditeur sont comptés **par workspace**, la protection saute
  sans que rien ne l'indique.
- Désarmer un gate pour « débloquer » un envoi.
- Conclure « ça va » depuis l'absence d'alerte. On cite la mesure ou on dit
  **INDÉTERMINÉ**.
