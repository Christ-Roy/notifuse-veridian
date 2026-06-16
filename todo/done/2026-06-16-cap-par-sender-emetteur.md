# Cap journalier par SENDER / adresse d'envoi (dimension émettrice)

> **Sévérité** : 🟢 P2 — lecture alternative de « 1/jour par provider », à confirmer avec Robert
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Type** : audit de cohérence — manque BACKEND identifié sous le preset warmup
> **Lié à** : `2026-06-16-preset-mode-warmup.md` (lecture B de la demande)

## Le trou

La demande Robert « 1 envoi par jour par provider » a deux lectures (cf. ticket
preset, section « Point clé ») :
- **Lecture A** (couverte aujourd'hui) : cap par CLASSE de provider DESTINATAIRE.
- **Lecture B** (NON couverte) : cap par ADRESSE D'ENVOI / SENDER / IP émetteur —
  c.-à-d. « chaque boîte expéditrice n'envoie que X mails/jour », le warmup IP
  classique où chaque sender monte son propre volume.

**Vérifié (lecture code)** : le daily-cap (`veridian_daily_cap.go`) est keyé
DESTINATAIRE uniquement — `CountSentSinceForContact` (par adresse reçue) et
`CountSentSinceForDomains` (par classe reçue). Il n'existe AUCUN COUNT par sender
émetteur (`message_history` n'est pas filtré par l'adresse FROM dans ces méthodes).
La sender-rotation (`veridian_sender_rotation.go`) RÉPARTIT sur les senders mais ne
PLAFONNE pas le volume par sender. Donc « X mails/jour par boîte émettrice » est
inexprimable en l'état.

## Ce qu'il faudrait (si Robert confirme la lecture B)

Un cap journalier keyé par sender émetteur, en plus du cap destinataire existant.

### Modèle

- Config sur `EmailProvider` : `VeridianPerSenderDailyCap int` (JSON blob, pas de
  migration) — max mails/jour par adresse d'envoi de cette infra.
- Source de vérité = `message_history` filtré par l'adresse FROM. ⚠️ Vérifier que
  `message_history` stocke bien le sender émetteur exploitable (colonne
  from/sender) — sinon il faut soit l'ajouter (migration), soit dériver. À auditer
  AVANT de chiffrer : `message_history` schema + ce que `Create`/`Upsert` y écrivent.
- Nouveau gate worker `veridianPerSenderCapGate` (jumeau de `veridianDailyCapGate`,
  même contrat skip-and-reschedule) OU extension du gate existant : COUNT par sender
  depuis minuit, plus-restrictif-gagne avec les caps destinataire/classe.

### Note de cohérence importante

En warmup, le cap par CLASSE destinataire (lecture A) + la sender-rotation
round-robin produisent DÉJÀ un effet proche : avec 3 senders en round-robin et un cap
de 1/jour/classe, chaque sender ne touche en pratique qu'une fraction du volume. La
lecture B n'est nécessaire QUE si Robert veut un plafond DUR par boîte émettrice
indépendant du destinataire (vrai warmup IP par boîte). **Ne pas implémenter avant
confirmation** — risque de sur-ingénierie d'une dimension que la rampe progressive
(ticket warmup-progressif) couvre peut-être suffisamment au niveau infra.

## Décision à trancher (Robert)

> **Question** : « 1 envoi/jour par provider » veut-il dire 1/jour vers chaque
> provider RECEVEUR (Gmail, Outlook…) [= déjà fait] OU 1/jour par BOÎTE ÉMETTRICE
> (chacune de tes 3 adresses agences-veridian.fr) [= ce ticket] ?
>
> Reco : lecture A (receveur) couvre le besoin réputation #1 et est déjà livrée. La
> lecture B (émetteur) n'est utile que pour un warmup IP par boîte ; la rampe
> progressive par infra la couvre probablement déjà. Confiance ~70 % que B n'est pas
> nécessaire à court terme.

## Fichiers concernés (si confirmé)

- `internal/domain/email_provider.go` — `VeridianPerSenderDailyCap` omitempty.
- `internal/repository/message_history_postgre.go` — `CountSentSinceForSender` (audit
  schema d'abord : colonne sender exploitable ?).
- `internal/domain/message_history.go` — méthode interface + décorateur quota + mock.
- `internal/service/queue/veridian_daily_cap.go` — gate par sender.
- Migration index `(sender, sent_at)` SI on count par sender (cf. V49, override
  `migrations-pending.txt` car runner en TX).

## Impact business

Optionnel. Ne pas faire sans GO Robert. Tracé ici pour ne pas perdre la lecture B de
la demande lors de l'exécution du preset.

## ✅ Résolu — 2026-06-17 (SHA ac38dbb0)

Lecture B IMPLÉMENTÉE (décision Robert explicite : le warmup = les DEUX
dimensions). **Audit confirmé : migration NÉCESSAIRE** — `message_history` ne
stockait aucune colonne sender exploitable (FROM en JSON `channel_options`,
non-queryable). Livré PROPREMENT (pas de dérivation) :

- **Migration V53** : colonne `message_history.veridian_sender_email VARCHAR(255)`
  (nullable, lowercase) + index partiel `(veridian_sender_email, sent_at)`.
  `config.VERSION` 52→53, fixture manager_test, migrations-pending.txt.
- **Repo** `CountSentSinceForSender` (index-only) + Create/Upsert écrivent le FROM.
- **Gate** `veridian_per_sender_cap.go:veridianPerSenderCapGate` (worker.go, après
  daily-cap, avant window). Cascade broadcast→infra→workspace. Best-effort.
- **Config** `EmailProvider`/`WorkspaceSettings`/`EmailQueuePayload`
  `VeridianPerSenderDailyCap` + metadata + allowlist.
- **UI** : champ par workspace + par infra + intégré au preset warmup.
- Tests colocalisés verts (Go + front). Diffs INLINE documentés CLAUDE.md.

Promo prod : E2E on-premise staging par le lead.
