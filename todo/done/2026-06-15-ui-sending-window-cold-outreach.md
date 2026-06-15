# [NOTIFUSE] 🟢 P2 — UI : exposer la sending window dans Settings → Cold outreach

> **Sévérité** : 🟢 P2 (confort config — backend déjà livré et fonctionnel)
> **Owner** : agent notifuse-veridian (front / agent uifix)
> **Créé** : 2026-06-15. Signalé par l'agent envoi-strategy après livraison du backend
> sending windows (commit 5a9278de).

## Contexte
Le backend des **horaires ouvrables (sending windows)** est livré et fonctionnel :
- Gate `veridianSendingWindowGate` dans `worker.go:processEntry` (skip-and-reschedule à
  NextOpening, cascade broadcast→infra→workspace, défaut 24/7 = non-régression).
- Type `domain.VeridianSendingWindow` (jours + plage horaire + timezone).
- Persisté dans les settings workspace (cascade 3 niveaux comme les rates/caps).

## Le trou
Le composant UI `console/src/components/settings/veridian_cold_outreach_settings.tsx`
n'expose PAS encore l'édition de la sending window (jours ouvrables + heures + timezone).
Un non-dev ne peut donc pas la régler depuis la console (seulement via API / IAC).

NB : le manifeste IAC (`iac/coldtunnel/workspace.yaml`) la déclare déjà (section
`sending_window`), donc elle est réglable via le CLI IAC en attendant l'UI.

## À faire
- [ ] Ajouter une carte "Sending window" dans veridian_cold_outreach_settings.tsx :
      sélecteur jours (lun-dim), plage horaire (HH:MM-HH:MM), timezone (défaut Europe/Paris).
- [ ] Brancher sur le settings workspace (même save POST /api/workspaces.update que le reste).
- [ ] ⚠️ Piège Lingui (libellés littéraux, pas de `t` hors composant) + piège SW cache
      (valider staging avec ?cachebust=).
- [ ] Valider par RENDU RÉEL Chrome (labels non-vides), pas comptage DOM.

## Reste vérifié OK (rien à faire)
- Caps/jour par provider destinataire : déjà dans l'UI (R0/R1) + cascade R2.
- Round-robin multi-SMTP par provider + caps : backend livré (5a9278de).
