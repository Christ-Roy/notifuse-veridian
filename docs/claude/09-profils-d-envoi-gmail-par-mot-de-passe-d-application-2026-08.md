# Profils d'envoi Gmail par mot de passe d'application (2026-08-04)

La console expose un parcours dédié `Gmail + mot de passe d'application` en plus
du SMTP avancé et des autres providers. Le preset verrouille `smtp.gmail.com:587`,
STARTTLS, basic auth, crée le sender depuis l'adresse Gmail et limite le profil à
1 email/minute par défaut. Plusieurs intégrations email restent possibles dans un
même workspace ; les actions `Use for Marketing` et `Use for Transactional`
sélectionnent explicitement le profil actif.

- Le secret de 16 caractères est normalisé sans espaces et chiffré avant
  persistance. Il n'est jamais réaffiché en clair.
- Une édition avec le champ secret vide préserve le ciphertext existant pour tous
  les providers email concernés, au lieu d'effacer silencieusement le credential.
- Les réponses workspace ne retournent ni secret clair ni ciphertext. Elles
  exposent seulement `veridian_credentials_configured` et, pour SMTP, les flags
  `has_password` / `has_oauth2_client_secret` /
  `has_oauth2_refresh_token`. Ces flags API sont nettoyés avant persistance.
- Le test d'un profil sauvegardé passe uniquement son `integration_id` : le
  serveur hydrate le secret en mémoire. Un succès persiste
  `veridian_transport_verified_at`; seuls les profils vérifiés entrent dans un
  pool explicite, sauf le singleton marketing legacy déjà actif (grandfather).
  La route est owner-only, borne le destinataire à l'email du owner connecté et
  applique un plafond dédié de 3 tests/heure/workspace/profil.
- Fichiers Veridian :
  `console/src/components/settings/veridian_email_profiles.ts`,
  `internal/service/veridian_email_provider_secrets.go` et tests 1:1.
- Diffs inline nécessaires :
  `console/src/components/settings/Integrations.tsx`,
  `internal/service/workspace_service.go`, `internal/service/email_service.go`.

