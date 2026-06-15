# [NOTIFUSE] 🟡 P1 — Conformité "vrai client mail" : multipart text+HTML + retirer X-Message-ID (cold)

> **Sévérité** : 🟡 P1 délivrabilité cold
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-15 par Robert. "Les headers doivent être parfaitement identiques
> à un client mail normal (genre Thunderbird), un truc clean."

## Ce qui a été vérifié (2026-06-15, code smtp_service.go + go-mail v0.7.2)

✅ **Déjà propre** : `X-Mailer: go-mail` / `User-Agent` go-mail **supprimés** via
`mail.NewMsg(mail.WithNoDefaultUserAgent())` (smtp_service.go:388). Message-ID au format
RFC822 `<id@domaine>` (Lot 3). Date/From/To/Subject/MIME-Version posés par go-mail.
List-Unsubscribe : posé UNIQUEMENT si `ListUnsubscribeURL` fourni (message_sender.go:387) —
pas sur le cold 1-to-1 (cohérent avec décision no-unsubscribe). OK.

## 🔴 LES 2 ÉCARTS vs un vrai client (à corriger)

### 1. HTML-only — PAS de partie text/plain (le vrai signal spam)
`smtp_service.go:459` : `msg.SetBodyString(mail.TypeTextHTML, request.Content)` — HTML SEUL.
Un vrai client (Thunderbird/Apple Mail) envoie `multipart/alternative` = partie **text/plain**
+ partie text/html. HTML-only est un signal anti-spam classique (corrélé HTML_IMAGE_ONLY,
MIME_HTML_ONLY côté SpamAssassin).
- `template.PlainText` EXISTE (template.go:428) mais c'est pour l'**indexation recherche**,
  PAS envoyé en MIME.
- **Fix** : générer/passer une partie text/plain (depuis le HTML, ou le champ template) et
  l'ajouter via `msg.AddAlternativeString(mail.TypeTextPlain, ...)` AVANT le HTML →
  multipart/alternative. Côté broadcast sender + transactional. Best-effort : si pas de
  texte, garder HTML-only (non-régression).

### 2. X-Message-ID — header custom non-standard (tell de machine)
`smtp_service.go:440` : `msg.SetGenHeader("X-Message-ID", request.MessageID)` posé sur TOUS
les envois. Un vrai client n'a pas de `X-Message-ID` (le Message-ID standard suffit). Tell
secondaire mais à nettoyer pour le cold.
- **Fix** : conditionner/retirer X-Message-ID sur le contexte cold (ou globalement — vérifier
  qu'aucun consommateur interne ne lit ce header ; le tracking interne utilise déjà le
  Message-ID standard + les tokens /t/ /r/).

## DoD
- [ ] Envoi cold = multipart/alternative (text/plain + text/html), vérifié sur le MIME RÉEL reçu
      (capturer un mail envoyé via staging, lire les headers/parts bruts — pas juste un test unit).
- [ ] X-Message-ID retiré (au moins sur le cold).
- [ ] Comparaison header-par-header vs un vrai mail Thunderbird documentée (ordre, Date, MIME).
- [ ] Tests + E2E. Non-régression : transactionnel/broadcast non-cold pas cassés.

## Note (option choisie par Robert)
Audit pixel-parfait complet (ordre exact headers/boundaries façon Thunderbird) = gain marginal
au-delà du multipart → non prioritaire. Le multipart + X-Message-ID sont l'essentiel.
