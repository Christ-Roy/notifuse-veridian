# Spintax — variation de contenu anti-empreinte (Lot 6 cold outreach, 2026-06-15)

Résout la syntaxe spintax `{option A|option B|option C}` du contenu email, PAR
DESTINATAIRE, avec un seed DÉTERMINISTE = l'email du contact. But cold : 500 mails
au HTML identique = signature spam triviale (fuzzy hashing) ; varier le corps casse
l'empreinte commune. Même destinataire re-rendu → même variante (debug, audit) ;
destinataires différents → variantes potentiellement différentes.

- **Package veridian dédié** : `pkg/veridian_spintax/veridian_spintax.go` —
  fonction pure `ResolveSpintax(input, seed string) string`. Parseur récursif
  descendant (FNV-1a sur le seed + finaliseur splitmix64 par index de groupe).
  Gère le **nesting** (`{Bonjour {Monsieur|Madame}|Salut}`), **préserve Liquid**
  (`{{ }}` et `{% %}` recopiés intacts, jamais interprétés comme spintax ; les
  accolades Liquid ne comptent pas dans l'équilibrage), **no-op STRICT** sans
  spintax (sortie = entrée à l'octet près), **robuste** aux accolades
  déséquilibrées (best-effort, zéro panic). Convention : `{{` collé = TOUJOURS
  Liquid (jamais nesting spintax) ; `{texte}` sans `|` = littéral préservé.
- **Point d'appel** (une ligne, zone disjointe du tracking Lot 5) :
  `pkg/notifuse_mjml/template_compilation.go` — résolution sur `mjmlString`
  complet APRÈS tout rendu Liquid et AVANT `preprocessMjmlForXML` (les `{{ }}`
  sont déjà remplacés, le spintax restant n'est que des accolades simples).
  Sujet + preview spintaxés juste après leur rendu Liquid (un sujet identique
  est aussi une signature spam). Helper `veridian_spintax_apply.go`
  (`veridianApplySpintax` : seed vide = no-op strict).
- **Graine câblée** = email du destinataire, dans les deux senders broadcast.
  Le sujet (rendu hors `CompileTemplate`) est spintaxé explicitement côté sender.

⚠️ **Diffs INLINE supplémentaires** (spintax) :

| Fichier upstream | Diff Veridian |
|---|---|
| `pkg/notifuse_mjml/template_compilation.go` | +1 champ `CompileTemplateRequest.VeridianSpintaxSeed` (string, omitempty) ; +appel `veridianApplySpintax` sur le corps (avant `preprocessMjmlForXML`) et sur subject/preview (après rendu Liquid) |
| `internal/service/broadcast/queue_message_sender.go` | `buildQueueEntry` : `CompileTemplateRequest.VeridianSpintaxSeed = email` + `subject = veridian_spintax.ResolveSpintax(subject, email)` |
| `internal/service/broadcast/message_sender.go` | `SendToRecipient` : `CompileTemplateRequest.VeridianSpintaxSeed = email` + `processedSubject = veridian_spintax.ResolveSpintax(processedSubject, email)` |

