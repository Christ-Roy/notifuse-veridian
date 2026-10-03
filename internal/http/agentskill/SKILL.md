---
name: notifuse
description: >
  Piloter une campagne d'emailing (transactionnel ou cold outreach B2B) sur
  ce workspace Notifuse via le CLI `notifuse`. Couvre le premier message,
  les plafonds par fournisseur destinataire, la chauffe progressive,
  l'import en masse, les séquences automatiques et le diagnostic d'un débit
  d'envoi qui semble bas ou bloqué. TRIGGER : "envoie une campagne", "importe
  ces contacts", "configure une séquence de relance", "pourquoi mes mails ne
  partent pas", "monte le volume".
---

# Emailing en masse avec Notifuse

Ce skill t'a été distribué avec une clé API scopée à UN workspace (voir
`AGENTS.md` dans ce même dossier pour où elle vit et comment l'utiliser).
Il porte le savoir-faire opérationnel : comment écrire, envoyer, monter en
volume et diagnostiquer sans cramer la délivrabilité du domaine d'envoi.

## Principe directeur

La délivrabilité se gagne lentement et se perd vite. Un fournisseur
destinataire (Gmail, Outlook/Microsoft 365, une boîte professionnelle
auto-hébergée, etc.) juge un domaine d'envoi sur son HISTORIQUE : volume,
régularité, taux de plainte, taux de réponse, taux de rebond. Le CLI expose
des plafonds et une fenêtre d'envoi précisément pour que cet historique reste
propre. Les contourner n'accélère rien : ça fait tomber le domaine en
liste noire, ce qui est bien plus lent à réparer qu'à construire.

## 1. Le premier message : texte brut, court, une question

Pour un premier contact (cold outreach), le meilleur taux de réponse vient
d'un message qui ressemble à un email humain écrit à la main, pas à une
newsletter :

- **texte brut** (`text/plain`), jamais de HTML tant que le destinataire n'a
  pas répondu une première fois ;
- 60 à 140 mots, une seule idée, une seule question à la fin ;
- objet de 3 à 60 caractères, littéral, sans emoji ni ponctuation agressive ;
- pas de préheader (`<mj-preview>`/`subject_preview`) recopiant l'objet ou
  le nom du gabarit : ça s'affiche dans la boîte du destinataire, pas dans
  ton outil de gestion. Soit on le laisse vide (la boîte affiche alors le
  début du corps, comme un mail écrit à la main), soit on y met une vraie
  accroche ;
- aucun lien de tracking, aucun pixel, pas de faux `Re:`/`Fwd:` ;
- un moyen simple et honnête de dire non, respecté immédiatement.

```bash
notifuse templates:push <workspace> \
  --plain-text-file premier-message.txt \
  --name cold-j0 --id cold-j0 \
  --subject "Une question sur <sujet>"
```

Avant d'envoyer au-delà d'un test, relis le texte comme si tu le recevais :
une seule idée, une question claire, zéro jargon marketing.

## 2. Plafonds par fournisseur destinataire et chauffe progressive

Chaque fournisseur de messagerie (Gmail, Microsoft 365/Outlook, un hébergeur
mail générique type OVH, une boîte auto-hébergée, etc.) a sa propre
tolérance au volume entrant depuis un domaine inconnu. Un domaine d'envoi
neuf doit monter en charge progressivement ("chauffe" / warm-up), jamais
démarrer à pleine capacité.

Règles par défaut raisonnables pour un domaine en chauffe (à adapter avec
`notifuse config` une fois l'historique établi) :

| Palier | Plafond / classe de fournisseur / jour | Débit |
|---|---|---|
| Semaine 1 (nouveau domaine) | 1 | très lent, étalé sur la journée |
| Semaine 2-3 | 10-30 | étalé, jamais en rafale |
| Mois 2+, historique propre | 100+ | selon réponses/plaintes observées |

Un même destinataire ne doit JAMAIS recevoir deux messages le même jour,
quel que soit le volume global autorisé — ce plafond-là ne monte pas avec
la chauffe, il reste bas en permanence.

```bash
# Voir/poser les plafonds et la fenêtre d'envoi au niveau workspace
notifuse settings:get <workspace>
notifuse settings:set <workspace> veridian_provider_class_daily_cap '{"google":10,"microsoft":10,"other":10}'
notifuse settings:set <workspace> veridian_per_recipient_daily_cap 1
notifuse settings:set <workspace> veridian_sending_window '{"days":[1,2,3,4,5],"start_hour":8,"end_hour":19,"timezone":"Europe/Paris"}'

# Mêmes plafonds, mais rattachés à UNE intégration d'envoi précise (plusieurs
# domaines/expéditeurs peuvent avoir des plafonds différents)
notifuse integrations:cold <workspace> --id <integration_id> \
  --rates '{"google":0.5,"microsoft":0.5}' \
  --daily-cap '{"google":10}' \
  --per-recipient-cap 1 \
  --exclude microsoft
```

La fenêtre d'envoi (jours ouvrés, heures humaines, dans le fuseau du
destinataire) et un léger "jitter" temporel (ne pas envoyer en rafale
exacte) font partie de la même logique anti-spam que les plafonds.

## 3. Import en masse (CSV, listes, segments)

```bash
notifuse contacts:import --file contacts.csv --list-id <list_id>
notifuse lists:create --name "Prospects Q4" --id prospects-q4
notifuse lists:stats --list-id prospects-q4
notifuse segments:create <workspace> --data @segment.json   # filtre dynamique
notifuse segments:contacts --id <segment_id>
```

Avant un import en masse :
- déduplique par email (et par domaine/entreprise si le cold B2B s'y prête) ;
- exclus les adresses de rôle (`contact@`, `info@`, `support@`...) pour du
  cold outreach : elles répondent rarement et ne sont jamais la bonne
  personne ;
- vérifie qu'aucun contact importé n'est déjà désabonné, en bounce dur, ou
  déjà engagé dans une séquence concurrente.

## 4. Séquences automatiques (J0 / J+4 / J+10)

Une séquence de relance qui ne s'arrête pas d'elle-même sur une réponse est
un bug, pas une fonctionnalité.

```bash
notifuse automations:create <workspace> --data @sequence.json
notifuse automations:activate <workspace> --id <automation_id>
notifuse automations:enroll <workspace> --id <automation_id> --segment-id <segment_id>

# Un contact répond ou ne doit plus être relancé : le sortir immédiatement
notifuse automations:exitContact <workspace> --id <automation_id> --contact-email <email>

# Repartir de zéro pour un contact (reset du parcours)
notifuse automations:resetContact <workspace> --id <automation_id> --contact-email <email>

# Diagnostiquer où un lot de contacts est bloqué dans le parcours
notifuse automations:nodeExecutions <workspace> --id <automation_id>
```

Structure recommandée pour une séquence de prospection :
1. **J0** : premier message (règle du §1).
2. **J+4** : relance courte, angle différent (pas un copier-coller avec
   "Re:" devant), qui demande toujours une décision simple.
3. **J+10** : dernière relance, ton direct ("je referme le sujet si pas de
   nouvelle"), puis sortie automatique de la séquence quoi qu'il arrive.
4. **Sortie immédiate** sur réponse (positive ou négative), désabonnement,
   ou bounce dur — jamais d'relance après un de ces trois signaux.

## 5. Oser le volume quand le vivier de leads est grand

Quand il y a beaucoup de leads qualifiés à travailler, la bonne réaction
n'est pas la prudence excessive : c'est d'augmenter la CAPACITÉ d'envoi
plutôt que de pousser un seul canal au-delà de son plafond sûr.

- **Plusieurs expéditeurs/domaines en parallèle** plutôt qu'un seul domaine
  poussé au-delà de sa chauffe : chaque domaine garde son propre historique
  de réputation et peut monter à son propre rythme.
- **Des plafonds plus élevés sont légitimes** une fois l'historique du
  domaine établi (taux de plainte bas, taux de réponse correct, pas de
  blocage fournisseur) — ne pas rester arbitrairement prudent par défaut
  quand les signaux sont bons.
- **On mesure en envoyant** : chaque palier de montée en charge doit être
  suivi d'un contrôle des stats (§6) avant le palier suivant, pas d'une
  intuition. Monter, mesurer, ajuster — jamais "monter et espérer".

```bash
# Ajouter un deuxième expéditeur/intégration et lui donner ses propres plafonds
notifuse integrations:create-smtp <workspace> --name relay-2 --host <host> --port <port> \
  --user <user> --password <pass> --from-email <expediteur2@domaine> --rate 30
notifuse integrations:cold <workspace> --id <nouvelle_integration_id> \
  --rates '{"google":1}' --daily-cap '{"google":30}'
```

## 6. Lire les stats et diagnostiquer un débit faible

🔴 **Avant toute autre investigation sur un débit qui semble bas ou
bloqué**, lance :

```bash
notifuse config <workspace>
```

Cette commande affiche en une fois les plafonds EFFECTIFS (après cascade
intégration > workspace, avec la valeur de chauffe du jour), la fenêtre
d'envoi active, l'état des files d'attente, et les intégrations configurées
avec leurs secrets masqués. Un plafond bas posé il y a longtemps à un
niveau qu'on ne regarde plus (ex. au niveau workspace alors qu'on pensait
n'avoir réglé que l'intégration) est la cause la plus fréquente d'un débit
qui semble "bloqué sans raison" — cette commande le rend visible en une
fois, sans avoir à deviner où regarder.

Autres leviers de diagnostic :

```bash
notifuse messages:list <workspace> --param limit=50       # historique des envois récents
notifuse lists:stats --list-id <id>                        # taux d'ouverture/clic/désabonnement d'une liste
notifuse webhooks:deliveries <workspace> --id <webhook_id>  # évènements delivery/bounce/complaint livrés
```

Un débit bas a presque toujours une cause visible dans `notifuse config` :
un plafond trop bas resté de la phase de chauffe, une fenêtre d'envoi trop
étroite, une exclusion de classe de fournisseur oubliée, ou une intégration
dont le cap par-sender masque le cap par-classe qu'on croyait actif.
