# Batterie E2E on-premise — garde-fous anti-cramage de domaine (2026-06-17)

Harness rejouable qui PROUVE en conditions réelles (vrai worker staging, vraie DB
staging, vrai SMTP → sink local `smtp-sink`) que les **10 gates de protection du
domaine** tiennent, AVANT tout envoi cold prod. Spec : ticket
`todo/2026-06-17-batterie-e2e-onpremise-garde-fous-domaine.md` (P0). 🔴 ZÉRO mail
externe : SMTP cible = `smtp-sink:1025` (aiosmtpd `-n -d`, cul-de-sac sur dev-pub,
réseau `notifuse-staging_notifuse-internal`) ; double-check du host AVANT tout
envoi + vérif finale que le relai sortant `mail-relay` n'a vu AUCUN mail.

- **Harness** (réutilisable, à rejouer avant chaque campagne) :
  `scripts/e2e/cold-garde-fous.sh` (orchestrateur : provision workspace jetable
  `gfcheck<stamp>` + intégration SMTP→sink 2 senders + double-check host + wipe) +
  `scripts/e2e/cold-garde-fous-gates.sh` (les 10 gates). Pour chaque gate : cas
  NON-RÉGRESSION (config absente = no-op) ET cas ENFORCED (config active = bloque/
  étale). Lit le sink (`docker logs smtp-sink`, QP-normalisé), `message_history` et
  la queue. Verdict PASS/FAIL par gate + récap. Env : `NOTIFUSE_HUB_API_SECRET`.
  6 gates prouvés par CAMPAGNE RÉELLE → sink (exclusion, throttle, pré-filtre,
  anti-hash+spintax, pixel par classe, round-robin) ; 4 gates état/temps via
  `cold-simulate` (prédicat exact) + enforcement worker (circuit breaker, daily cap,
  per-sender cap, sending window).

- **Extension `cold-simulate` (3 modes neufs)** : pour prouver les gates état/temps
  sans envoi réel ni mock, frappant le PRÉDICAT EXACT du gate worker :
  - `class_cap_decision` : `CountSentSinceForDomains(classe) >= class_cap`
    (cap-CLASSE de `veridian_daily_cap.go`). ⚠️ Isolation : le COUNT est réel et
    partagé par classe → un test doit lire le baseline ou utiliser une classe non
    polluée par la campagne (sinon faux "FAIL" : la classe a déjà des envois du jour).
    **Étendu 2026-06-18** : param optionnel `sender_domain` (domaine nu OU adresse
    dont on extrait le domaine, normalisé comme `veridianEmailDomain`). Fourni → le
    COUNT passe par `CountSentSinceForDomainsAndSenderDomain` = le prédicat EXACT de
    `veridianCountClassForInfra` (compteur par INFRA ÉMETTRICE). Absent → COUNT
    workspace-global `CountSentSinceForDomains` inchangé (non-régression). La réponse
    expose `sender_domain` (normalisé) + `per_infra` (bool, true = chemin par infra).
  - `per_sender_cap_decision` : `CountSentSinceForSender >= per_sender_cap` (warmup
    IP, `veridian_per_sender_cap.go`). `seed_sent` accepte désormais `sender_email`
    (pose `veridian_sender_email`).
  - `sending_window_decision` : `IsWithinWindow(now)` pur (`veridian_sending_window_gate.go`),
    renvoie `within`/`would_be_skipped`/`next_opening_unix`.
  - Fichier : `internal/http/veridian_cold_simulate_handler.go` (+ tests colocalisés).

- **Pièges vécus (gravés)** :
  - **Circuit breaker — race provider-switch** : pour exercer le circuit, on bascule
    le provider du workspace sur une intégration SMTP KO (port fermé). Les envois
    échoués sont RESCHEDULÉS (backoff) ; si on RESTAURE le provider sain avant la fin
    de l'observation, le worker re-tente avec le sink sain → les mails partent au sink
    (faux négatif). FIX : workspace jetable, on NE restaure PAS — le provider KO reste
    actif, les entrées restent en échec, 0 sink. Preuve = `failed_at`≥1 + 0 sink +
    logs `connection refused`.
  - **Dead-DNS du pré-filtre** : un domaine corporate « valide » de test doit être
    RÉSOLVABLE (A/MX) sinon le pré-filtre le coupe lui aussi → on utilise `example.com`
    (A records), pas `.example`/`.invalid` (NXDOMAIN, réservés au cas dead-DNS).
  - **QP wrapping au sink** : aiosmtpd imprime le HTML en quoted-printable (soft-break
    `=\n`, `=3D`) → dé-wrapper avant de grep `/t/`, `/r/`, spintax.

