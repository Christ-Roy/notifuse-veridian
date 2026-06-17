# Batterie E2E on-premise — prouver TOUS les garde-fous anti-cramage de domaine

> **Sévérité** : 🔴 P0 — zéro droit à l'erreur (une faille = domaine d'envoi grillé)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (demande Robert : simuler une campagne cold complète, vérifier que la logique ne crée AUCUN problème avant d'envoyer pour de vrai)
> **Contrainte** : staging réel + sink local `smtp-sink` (dev-pub 172.20.0.4:1025, cul-de-sac aiosmtpd). ZÉRO mail vers un provider externe.

## But

Avant le premier vrai envoi cold en prod, prouver EN CONDITIONS RÉELLES (vrai worker,
vraie DB staging, vrai chemin SMTP→sink) que CHAQUE gate de protection du domaine tient.
Une erreur de logique ici = bounces en masse / volume non maîtrisé / envoi à Microsoft
en warm-up = domaine grillé.

## Les garde-fous à prouver (ordre réel dans worker.go processEntry)

1. **Circuit breaker** — 5 erreurs provider consécutives → circuit ouvert → plus d'envoi pendant le cooldown. Prouver : injecter des erreurs SMTP (sink qui refuse ?) → circuit s'ouvre → les entrées suivantes sont reschedulées sans taper le SMTP.
2. **Exclusion de classe** (microsoft/outlook) — un contact de classe exclue → échec PERMANENT, AUCUN SMTP ouvert, entrée queue supprimée, le reste de la campagne part. Prouver via le sink (aucun mail microsoft n'arrive) + message_history (FailedAt).
3. **Throttle par classe** (débit/min) — avec rate google=1/min, sur N contacts google, le sink reçoit ~1/min (pas de rafale). Prouver l'étalement temporel réel + PAS de head-of-line blocking (une classe lente ne bloque pas une classe rapide).
4. **Daily cap** (par destinataire ET par classe) — cap=1/jour : 2e envoi vers la même adresse / 2e vers la même classe le même jour = bloqué. Prouver via cold-simulate seed_sent + daily_cap_decision + un vrai 2e envoi qui ne part pas.
5. **Per-sender cap** (warmup IP) — cap=N/jour par adresse émettrice : la N+1e tentative depuis ce sender est bloquée. Prouver via seed message_history sur le sender + envoi bloqué.
6. **Sending window** — hors horaires → reschedule à la prochaine ouverture, AUCUN envoi hors fenêtre. Prouver avec une fenêtre passée/future.
7. **Pré-filtre** — adresse syntaxe invalide / domaine jetable / DNS-mort → skip permanent, pas de SMTP. Prouver via le sink (n'arrive pas) + FailedAt.
8. **Anti-hash + spintax** — 2 mails même classe en <72h au rendu identique = re-spin/bloqué (anti-empreinte). Prouver via le HTML émis au sink (variété).
9. **Pixel par classe + tracking** (déjà validé v53 mais re-confirmer dans la campagne complète) — pixel OFF google/microsoft, ON petits ; clics partout.
10. **Round-robin senders** — ≥2 senders cold → rotation effective entre adresses (pas toujours le même FROM). Prouver via les FROM observés au sink.

## Méthode

- Étendre le harness : `scripts/e2e/tunnel-send.sh` (campagne sink) + l'endpoint
  `cold-simulate` (déroule la logique sans SMTP — étendre ses modes si besoin pour
  couvrir circuit-breaker, sending-window, per-sender, exclusion).
- Une spec Playwright dédiée `tests/e2e-veridian/specs/cold-garde-fous.spec.ts` OU un
  script bash orchestrateur qui : provisionne un workspace jetable, configure chaque
  gate, déclenche une campagne, lit le sink (docker logs smtp-sink) + message_history +
  queue, ASSERTE le comportement attendu pour chaque gate, wipe le workspace.
- Pour chaque gate : un cas PASS (config absente = no-op, non-régression) ET un cas
  ENFORCED (config active = bloque/étale comme prévu). Les DEUX comptent.
- ZÉRO mail dehors : SMTP cible = smtp-sink:1025 (double-check host avant tout envoi,
  comme la session du 2026-06-13). Si le moindre doute de fuite externe → STOP.

## DoD
- [ ] Les 10 gates prouvés en conditions réelles (PASS + ENFORCED chacun)
- [ ] Rapport clair : pour chaque gate, ce qui a été observé (sink/DB/queue) vs attendu
- [ ] Tout trou/faille de logique trouvé = ticket P0 + signalé au lead AVANT tout envoi prod
- [ ] Harness réutilisable (script/spec versionné) pour rejouer la batterie avant chaque envoi
- [ ] Aucun mail externe (vérifié : relai sortant = 0 sur toute la batterie)
