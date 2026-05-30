# Audit + harmonisation UI/UX du choix d'envoi mail (sender) Notifuse

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-05-30
> **Demandé par** : Robert (constat : "Veridian generic sender" affiché mais inexistant + UI à ajuster)

## Constat (audit terrain 2026-05-30)

L'UI Settings > Mail account propose un radio sender avec 2 options :
- `smtp_generic` → libellé **"Veridian generic sender (default)"**
- `hub_gmail` → "My connected account"

**Problème : les deux libellés mentent sur l'état réel du backend.**

### Ce qui est cassé / trompeur

1. **"Veridian generic sender" n'existe pas.**
   - Aucun SMTP Veridian global n'est pré-configuré. Notifuse upstream
     fonctionne par **provider configuré par workspace** (écran Integrations >
     Email Providers : SMTP/SES/SparkPost/Postmark/Mailgun/Mailjet/SendGrid).
   - Le libellé laisse croire qu'un sender Veridian clé-en-main existe. Faux.

2. **Le choix `MailProviderChoice` n'est JAMAIS lu à l'envoi.**
   - Aveu dans le code : `internal/service/veridian_broadcast_rate_limit_handler.go:14-15`
     "l'EmailService upstream route tout via SMTP generique et ignore le choix
     `workspace.MailProviderChoice`".
   - `hub_gmail` ne déclenche aucun envoi via Hub Gateway par défaut (wrapper
     `veridian_broadcast_rate_limit_handler` "n'est PAS branché par défaut",
     ligne 20).
   - **Le radio est purement cosmétique** : une préférence stockée que rien ne
     consomme au moment de l'envoi. Dette déjà tracée :
     `todo/2026-05-26-mail-gateway-refactor-emailservice.md` §3.3 (non livré).

### Conséquence UX

Le client choisit un sender → ça ne change rien à ses envois. Il connecte un
Gmail → ça ne route rien. C'est un faux choix. À corriger AVANT de promettre
quoi que ce soit en prod.

## Demande — harmonisation (2 axes)

### Axe A — Vérité des libellés (quick win, sans backend)

- Remplacer "Veridian generic sender" par un libellé honnête qui reflète l'état
  réel : soit "Configured email provider (SMTP/SES/...)" pointant vers l'écran
  Integrations, soit retirer le radio tant que le routing §3.3 n'est pas livré.
- Si le routing n'est pas câblé : ne PAS afficher un choix qui ne fait rien.
  Afficher à la place l'état réel ("Aucun provider configuré — configurez-en un
  dans Integrations" / "Gmail connecté via Veridian").

### Axe B — Câbler le routing réel (dépend de §3.3)

- Implémenter le refactor `EmailService` (`todo/2026-05-26-mail-gateway-refactor-emailservice.md`
  §3.3) pour que `MailProviderChoice` soit RÉELLEMENT lu à l'envoi :
  - `hub_gmail` → route via `pkg/hub_mail_gateway` POST /api/mail/send-as-user
  - sinon → provider workspace configuré (comportement upstream)
- Une fois câblé, le radio redevient un vrai choix → réactiver Axe A avec les
  bons libellés.

### Axe C — Clarifier le modèle UI "Notifuse vs Hub" (cf. ticket séparé)

Voir `todo/2026-05-30-mail-account-ui-vs-hub-boundary.md`.

## Note importante

NE PAS livrer l'Axe A "libellé honnête" en se contentant de renommer si le
fond reste un faux choix. Le bon ordre : soit (1) câbler le routing (Axe B)
puis libellés vrais, soit (2) masquer le choix tant que B n'est pas là.
Trancher avec Robert : quick win cosmétique d'abord, ou attendre B ?
