# E2E tunnel cold outbound — validation bout-en-bout via sink SMTP local (2026-06-13)

> **Type** : Doc de validation (méthode réutilisable pour les futurs E2E tunnel)
> **Sévérité** : 🟢 résolu — tunnel VALIDÉ yeux fermés
> **Owner** : agent notifuse-veridian
> **SHA staging validé** : `v48.0-veridian.53e6b2d8`

## Verdict

🟢 **Tunnel mécaniquement opérationnel, validé en conditions réelles.** Chaîne
complète prouvée : config UI/DB → worker → queue par classe → résolution pixel
par classe (fallback workspace) → compilation HTML → SMTP → réception. ZÉRO mail
sorti vers un provider externe (envoi 100% local vers un sink cul-de-sac).

## Matrice pixel/clics observée (HTML RÉELLEMENT émis au SMTP)

Corrélation rigoureuse par message (1 To par bloc vérifié, quoted-printable
décodé, corrélé par X-Message-ID) :

| destinataire (classe) | pixel ouverture `/t/` | clic `/r/` | attendu | verdict |
|---|---|---|---|---|
| google      | ABSENT | présent | OFF / ON | ✅ |
| microsoft   | ABSENT | présent | OFF / ON | ✅ |
| yahoo_aol   | PRÉSENT | présent | ON / ON | ✅ |
| freemail_fr | PRÉSENT | présent | ON / ON | ✅ |
| corporate   | PRÉSENT | présent | ON / ON | ✅ |

= contrat tunnel respecté : pixel d'ouverture OFF sur gros providers
(google/microsoft, réputation), ON sur petits ; clics trackés PARTOUT
(découplage pixel/clics confirmé). 10/10 messages (2 rounds × 5) en statut SENT,
zéro erreur.

Ce run exerce le **fallback WORKSPACE** du pixel (config `veridian_open_pixel_by_class`
posée au niveau WORKSPACE, PAS sur le broadcast) → prouve end-to-end le fix P1
backender (DI `SetVeridianWorkspaceRepo`, un GetByID mémoïsé par batch,
best-effort). Valide aussi le fix allowlist `UpdateWorkspace` (bcc23764) : la
config cold outreach posée au niveau workspace persiste bien.

## Méthode sink local (RÉUTILISABLE pour les futurs E2E)

Objectif : tirer la mécanique RÉELLE (queue + throttle + pixel + SMTP) SANS
envoyer un seul mail dehors (consigne Robert : zéro provider externe, zéro Lark).

1. **Sink** : conteneur `smtp-sink` (python:3.12-alpine) sur dev-pub, lancé
   `aiosmtpd -n -l 0.0.0.0:1025 -d` → handler Debugging : imprime le message
   complet dans les logs docker et le JETTE. Aucun relayhost, aucun MX :
   cul-de-sac absolu, incapable de livrer dehors.
2. **Réseau** : `smtp-sink` (172.20.0.4) et `notifuse-staging` (172.20.0.3)
   partagent le réseau docker `notifuse-staging_notifuse-internal`. Alias DNS
   docker `smtp-sink` résoluble depuis le conteneur Notifuse.
3. **Cible intégration** (tenant coldtest staging) : `host=smtp-sink port=1025
   use_tls=false`, **username/password VIDES** (cf. piège AUTH ci-dessous).
   ⚠️ DOUBLE-CHECK obligatoire `host=smtp-sink` AVANT tout schedule (anti
   fat-finger : un seul mail vers le relai sortant 100.92.215.42:587 = échec
   de la consigne Robert).
4. **Tir** : `scripts/e2e/tunnel-send.sh --real-send --rounds 2`
   (`SMTP_RELAY_HOST=smtp-sink SMTP_RELAY_PORT=1025 SMTP_RELAY_USER="" SMTP_RELAY_PASS="" SMTP_RELAY_TLS=false`).
5. **Observation** : `docker logs smtp-sink` (le `-d` imprime headers + HTML
   complet) → parser par bloc `MESSAGE FOLLOWS … END MESSAGE`, décoder le
   quoted-printable, corréler `To:` ↔ présence `/t/` (pixel) et `/r/` (clics).
   ⚠️ NE PAS parser au grep brut : le HTML multi-ligne QP casse le découpage
   naïf et mélange les blocs (faux positif "microsoft pixel-ON" observé en
   parsing brut, levé par corrélation propre nbTo=1 par bloc).
6. **APRÈS** : RE-BASCULER l'intégration coldtest vers le relai normal
   (`100.92.215.42:587 use_tls=true`) — ne pas laisser le tenant pointé sink.

## Piège AUTH (sink sans authenticator)

`aiosmtpd -n` annonce `AUTH LOGIN/PLAIN` mais n'a AUCUN authenticator → rejette
l'AUTH avec **SMTP 538**. Notifuse, si l'intégration a un username/password,
tente `AUTH PLAIN` → 538 → envoi échoue avant le DATA. FIX : intégration SANS
credentials (`username="" password=""`) → Notifuse skip l'AUTH
(`internal/service/smtp_service.go:270` : `if Username != "" && Password != ""`)
→ le sink accepte le DATA.

⚠️ **Nuance de complétude** : ce run teste le chemin SMTP **no-AUTH**. Le maillon
"AUTH SMTP vers relai réel" n'est PAS couvert ici — il l'a été le 11/06 avec le
vrai relai agences (AUTH PLAIN OK). L'AUTH est orthogonale au tunnel
(queue/throttle/pixel/clics), donc zéro impact sur ce qui est validé ici.

## Throttle — couverture

Inter-round observé : round 1 sent à T, round 2 sent à T+1min (le throttle a
fait attendre le round 2). L'étalement INTRA-classe par débit (google 1/min vs
corporate 60/min) n'est pas démontrable avec 1 mail/classe/round (le reschedule
ne se déclenche qu'au 2e mail d'une même classe dans un broadcast). La mécanique
fine (reschedule sans incrément d'attempts, no head-of-line blocking,
no busy-loop) est couverte par les tests unitaires
`internal/service/queue/veridian_provider_throttle_test.go` (`ProcessEntry_*`).
Un run multi-mails/classe vers le sink la démontrerait en réel si besoin.

## Bugs débusqués pendant l'enquête (pourriture en couches)

L'E2E + la validation terrain ont révélé 3 bugs distincts, chacun masquant le
suivant :
1. Host Hub fantôme `hub.veridian.site` (NXDOMAIN) → fix 600b7001 (`app.veridian.site`).
2. Contrat refill `[2]int` vs `{min,max,perLead}` → fix dérivé (cassait le décodage du catalogue entier).
3. (Faux 4e signal "microsoft pixel-ON" = artefact de parsing du log, levé par corrélation propre.)
