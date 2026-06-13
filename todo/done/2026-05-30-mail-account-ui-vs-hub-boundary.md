# Frontière UI Notifuse ↔ Hub pour la gestion des comptes mail

> **OBSOLÈTE 2026-06-13** — pivot envoi stand-alone 31/05, archi mail-gateway via Hub abandonnée.

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse-veridian (+ coordination agent Hub)
> **Créé** : 2026-05-30
> **Demandé par** : Robert ("peut-on tout faire depuis Notifuse sans passer par le Hub côté UI ?")

## Question posée

Le client doit-il quitter Notifuse pour aller sur le Hub afin de gérer ses
comptes d'envoi mail ? Peut-on tout garder dans l'UI Notifuse ?

## Réponse d'audit (2026-05-30)

### Ce qui DOIT rester au Hub (incompressible)

- **Le consent OAuth Google/Microsoft `gmail.send`** : Notifuse n'a PAS de
  client OAuth sending à lui (vérifié : seuls OAuth Notifuse = magic-link /
  SSO / invite, pas de scope `gmail.send`). Raison : 1 seul consent screen +
  1 seul audit Google centralisé (audit "restricted scope" Google = 15-75k$,
  par app — on n'en veut qu'UN, au Hub). Le refresh token user vit au Hub.
- Donc le **moment du consent** (quelques secondes, redirect → Google → rebond)
  passe forcément par le Hub. C'est déjà le cas et le rebond a été fixé
  (cf. todo/2026-05-30-hub-oauth-gmail-rebond-livre-prod.md).

### Ce qui PEUT (et devrait) vivre dans l'UI Notifuse

- **Lister les comptes connectés** : déjà fait via proxy
  `GET /api/veridian/mail-accounts/me` (200, marche).
- **Choisir le sender / le compte par défaut** : déjà UI Notifuse (à câbler au
  backend, cf. ticket harmonisation Axe B).
- **Configurer un provider SMTP/SES classique** : déjà natif Notifuse
  (écran Integrations > Email Providers).
- **Déconnecter un compte** : pas encore (TODO vague 8, endpoint Hub DELETE
  pas spécifié — cf. handleDisconnect du composant qui log juste un warn).

## Demande

1. **Décider du modèle cible** avec Robert :
   - (a) Notifuse = UI complète sauf le clic-consent (recommandé : le client
     ne "voit" le Hub que 3 secondes pendant le consent Google, tout le reste
     est dans Notifuse). OU
   - (b) Assumer que la gestion des comptes mail se fait sur le Hub
     (Notifuse affiche juste un lien "Gérer mes comptes sur Veridian").

2. **Si (a)** : ouvrir les sous-tickets manquants côté Notifuse + Hub :
   - Endpoint Hub `DELETE /api/users/{userId}/mail-accounts/{id}` (déconnexion
     depuis Notifuse sans aller au Hub) → ticket à déposer dans veridian-hub/todo/
   - Endpoint Hub `GET .../mail-accounts` déjà OK (proxifié).
   - UI Notifuse : bouton Disconnect fonctionnel (aujourd'hui no-op).

3. **Trancher mono vs multi-compte** (déjà signalé) : l'UI Notifuse promet du
   multi-compte ("Connect another Gmail", badge Default), le Hub gère du
   mono-compte (`accounts.find(...)`). Aligner les deux. Décision produit +
   coordination agent Hub.

## Dépendances

- Coordination agent Hub pour les endpoints DELETE + multi-compte.
- Ticket harmonisation libellés : todo/2026-05-30-audit-mail-ui-harmonisation.md
