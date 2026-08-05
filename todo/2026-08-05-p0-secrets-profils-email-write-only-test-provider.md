# P0 sécurité - Secrets email write-only et endpoint de test non rejouable

> **Sévérité** : 🔴 P0 sécurité
> **Surface** : API workspace, console, test SMTP

## Vulnérabilité constatée

`SMTPSettings.MarshalJSON` masque le mot de passe en clair mais conserve le ciphertext chiffré, car le même type JSON sert aussi au stockage. L'API workspace peut donc rendre ce ciphertext à un membre.

En parallèle, `EmailService.TestEmailProvider` authentifie l'utilisateur dans le workspace sans imposer le rôle owner et accepte un provider complet ainsi qu'un destinataire arbitraire. Un membre pouvant lire le ciphertext peut le rejouer vers l'endpoint de test ; le serveur le déchiffre avec sa clé globale puis effectue un vrai envoi. Le rate limit API général ne remplace pas une autorisation ni un plafond dédié à cet effet externe.

## Correction exigée

- Séparer le modèle de persistance du DTO de réponse. Les champs password, app password, client secret, refresh token et leur ciphertext sont toujours absents des réponses JSON.
- Exposer seulement un booléen du type `credential_configured`, sans empreinte exploitable.
- Update sans nouveau secret conserve le secret stocké côté service ; ne jamais faire de round-trip du ciphertext via le navigateur.
- Restreindre create/update/delete/test d'un profil d'envoi au owner selon le contrat console actuel.
- Remplacer le test « provider arbitraire » par :
  - test d'une `integration_id` appartenant au workspace, secret récupéré côté serveur ; ou
  - test d'un provider non persisté avec secret neuf en clair, owner uniquement, jamais ciphertext accepté.
- Rate limit étroit par workspace + user + integration, audit log sans secret, destinataire borné à l'adresse owner ou à une adresse explicitement vérifiée.
- Refuser host/port privés ou metadata/cloud internes pour un provider arbitraire afin d'éviter SSRF ; le mode sink de staging doit être une capacité interne explicitement gardée, pas exposée à l'API SaaS standard.

## Briques à conserver

- Chiffrement au repos et masquage des champs runtime en clair.
- Conservation serveur du secret lors d'un update où le formulaire laisse le password vide.
- Contrôle owner déjà présent sur create/update/delete d'intégration.

Le correctif doit séparer stockage et représentation API, pas supprimer ces protections existantes.

## Tests obligatoires

- GET workspace et toutes erreurs : aucun clair, ciphertext, refresh token ni client secret.
- Membre non-owner : create/update/delete/test refusés.
- Ciphertext renvoyé comme mot de passe dans test/update : refusé, jamais déchiffré.
- Rate limit dédié prouvé ; audit n'enregistre ni secret ni payload sensible.
- Update sans password conserve le secret ; remplacement explicite fonctionne.
- Test SMTP prod borné ; test E2E automatisé uniquement contre sink loopback.

## Definition of Done

Un utilisateur qui possède toutes les réponses API et tout l'état de la console ne peut ni extraire ni rejouer un secret sauvegardé. L'endpoint de test ne constitue plus un relai arbitraire authentifié.
