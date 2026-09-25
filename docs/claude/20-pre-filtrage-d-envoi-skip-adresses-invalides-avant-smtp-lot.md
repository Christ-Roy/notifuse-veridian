# Pré-filtrage d'envoi — skip adresses invalides AVANT SMTP (Lot 7, 2026-06-14)

Quatrième gate du worker (après circuit breaker → throttle minute → daily cap).
BUT : ne PAS taper le serveur SMTP pour une adresse qu'on SAIT déjà morte —
chaque envoi vers une adresse invalide est un bounce probable qui grille la
réputation IP et gaspille du quota. Spec : ticket
`todo/2026-06-14-bounce-loop-postfix-suppression-cold.md` (lot B « pré-filtrage »).

- **3 conditions DURABLES filtrées** (une adresse invalide ne redevient jamais
  valide) : (1) **syntaxe** invalide (`net/mail.ParseAddress`, RFC 5322, +
  rejet display-name / espaces / `@` multiples → adresse NUE exigée) ; (2)
  **domaine jetable** (`pkg/disposable_emails.IsDisposableEmail` appelé sur le
  **DOMAINE** — la liste embarquée est une liste de domaines, pas d'emails) ;
  (3) **domaine DNS-mort DÉCISIF** (NXDOMAIN, ou ni MX ni A/AAAA — implicit MX
  RFC 5321). RÉUTILISE le classifier MX du Lot 4 (nouvelle méthode
  `VeridianMXClassifier.ResolveDeliverability` qui distingue verdict DÉCISIF vs
  transitoire), PAS de nouveau lookup ni de duplication.
- **Marquage SANS re-tentative en boucle** (≠ throttle/cap qui reschedulent) :
  une adresse pré-filtrée part en **échec PERMANENT** via le chemin upstream
  existant `handleError(ClassifiedError{Type:recipient, Retryable:false})` →
  `MarkAsProcessing` (incrémente attempts) → `message_history` avec `FailedAt`
  (trace durable) → `Delete` de l'entrée queue. Statut « invalid » durable POUR
  CET ENVOI ; AUCUN SMTP ouvert ; le circuit breaker n'est PAS déclenché (erreur
  destinataire). Pas de touche à `contact_lists` : la **suppression durable du
  contact** reste la prérogative du bounce RÉEL (Lot 2, NDR Postfix →
  `MarkEmailsAsBounced`) — la dupliquer ici serait le contournement interdit. Le
  pré-filtre est la DERNIÈRE ligne de défense PAR ENVOI.
- **Best-effort STRICT (non-régression critique)** : syntaxe + jetable = checks
  PURS zéro I/O (toujours actifs, déterministes). DNS = best-effort : timeout /
  erreur transitoire / resolver sans capacité host / suffixe public connu →
  verdict INDÉTERMINÉ → l'envoi PASSE. JAMAIS un glitch DNS ne bloque un
  destinataire légitime. Suffixe public connu (gmail/orange/…) = délivrable
  SANS lookup (hot path).
- **Fichiers veridian** : `internal/service/queue/veridian_prefilter.go`
  (gate `veridianPrefilterRecipient` + helpers `veridianValidEmailSyntax` /
  `veridianEmailDomain`) + `_test.go` colocalisé (syntaxe / jetable / NXDOMAIN
  skippés ; adresse valide PASSE ; suffixe connu sans lookup ; timeout DNS ne
  bloque pas ; classifier absent ne bloque pas). Extension de
  `veridian_provider_class_mx.go` : `ResolveDeliverability` +
  `VeridianMXDeliverability` (3 états) + `veridianMXNotFound` (NXDOMAIN décisif
  vs transitoire) + `LookupHostAddrs` sur le resolver de prod (fallback A/AAAA),
  capacité optionnelle `veridianHostResolver` détectée par type-assertion (les
  resolvers de test sans elle restent INDÉTERMINÉS = ne bloquent pas).
- Pas de migration, pas de `config.VERSION` bump (aucun schéma DB touché).

⚠️ **Diffs INLINE supplémentaires** (pré-filtrage Lot 7) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/queue/worker.go` | +gate `veridianPrefilterRecipient` dans `processEntry` (APRÈS le daily cap, AVANT `MarkAsProcessing`) → route vers `handleError` permanent (skip SMTP, jamais re-tenté). Importe `fmt`/`emailerror` déjà présents. |

