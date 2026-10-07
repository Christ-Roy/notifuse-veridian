# 55. Page « Profils d'envoi » (lot 3 du plan 53)

Date : 08/10/2026. Base : `origin/veridian` au commit `a30095aa` (lot 2 en prod, `v60.0-veridian.a30095aa`).

## Ce que ça pose

1. **Route `/console/workspace/$id/sending-profiles`**, entrée de sidebar « Profils d'envoi » (groupe Envoi, avant le Journal d'envoi). La page lit `emailProfiles.overview` (lot 2) et ne recalcule rien : plafond, porte limitante, ralentissements, fenêtre viennent du plan serveur (`VeridianEffectivePlan`, partagé avec le worker).
2. **Une carte par profil** (`components/sending_profiles/veridian_profile_card.tsx`) : nom, type (SMTP, Gmail mot de passe d'application, Gmail OAuth, nom du fournisseur API), badge vérifié, expéditeurs, « Aujourd'hui » (envoyés / plafond effectif, porte limitante en clair : « Chauffe, jour 3/5 », « Plafond du profil », « Fenêtre fermée jusqu'à vendredi 08:00 »), réputation par fournisseur destinataire (seulement les classes ralenties ou arrêtées, avec facteur et taux), classes exclues, boîte IMAP liée, actions. Sections Commercial et Transactionnel séparées, encart des boîtes de retour globales. Aucune extrapolation « par minute ≈ par jour » : la cadence technique (`native_rate_per_min`) n'est pas affichée sur la carte.
3. **Assistant « Ajouter un profil »** (`veridian_profile_wizard.tsx`), trois choix : SMTP + IMAP (IMAP de retour facultatif, prérempli sur le même hôte), Gmail avec mot de passe d'application (lien direct `https://myaccount.google.com/apppasswords`, 3 étapes, plafond 30 par défaut), Gmail OAuth grisé « Bientôt ». Puis test de transport vers l'adresse du propriétaire connecté, et proposition : rotation commerciale, réservé au transactionnel, ou plus tard.
4. **`POST /api/veridian/emailProfiles.create`** (propriétaire seulement) : profil SMTP et boîte IMAP liée écrits dans UN SEUL `workspaceRepo.Update`, tout validé et chiffré en mémoire avant. Une erreur ne laisse rien. Gmail : hôtes et ports imposés (`smtp.gmail.com:587`, `imap.gmail.com:993`), plafond 30, cadence technique 1 par minute ; SMTP : 60 par minute. Le profil naît hors rotation, non vérifié. Réponse : identifiants seulement.
5. **Plan** : `VeridianPlanClass` gagne `slowdown_rate` (taux de rejets durs ou de refus 5.7.x qui a déclenché le ralentissement) et `sent_7d`, pour afficher « 9,9 % de rejets ». Additif.
6. **Modifier** : tiroir (nom, usage exclusif, expéditeurs, transport avec mot de passe en écriture seule, boîte IMAP liée ou nouvelle) et réglages avancés repliés : plafond du profil, chauffe, fenêtre, débit et plafond par classe, plafond par destinataire et par adresse, classes exclues, jitter, anti-hash, seuil du fusible, frein technique SMTP. Chaque réglage dit sa source : « Défini sur ce profil », « Hérité du workspace », « Défaut ». Personnaliser une table par classe copie TOUTE la table héritée (la table du profil remplace celle du workspace en bloc côté backend).
7. **Réglages > Intégrations** ne porte plus ni profils d'envoi ni boîtes IMAP (ni la carte générique « Type: imap ») ; un lien renvoie vers la page. Supabase, LLM, Firecrawl restent.

## Écritures

Toutes passent par `veridian_profile_ops.ts`, qui relit le workspace avant d'écrire et retire de la requête les champs que seul le serveur émet (`veridian_transport_verified_at`, `has_*`). Pause : `veridian_paused` (le worker bascule sur le reste du pool). Rotation et usage : `workspaces.update` (exclusivité validée côté serveur, doublée côté console : un profil transactionnel ne rejoint pas la rotation, la rotation ne se vide pas, un profil non vérifié n'y entre pas).

## Tests

Vitest : règles pures, carte (porte limitante, réputation, exclusivité), assistant (trois parcours, secret jamais réaffiché, test refusé, refus serveur), opérations, réglages avancés et héritage, page (chiffres égaux à l'overview), garde de coque, complétude du français (portée étendue à `veridian_*.ts`, `SendingProfilesPage.tsx`, variables conservées). Go : service et handler de création (atomique, secret chiffré et jamais renvoyé, propriétaire seulement), taux du plan. Playwright prod-smoke (`e2e-prod-smoke/sending-profiles.spec.ts`) : la page sur le build de production, en anglais et en français (vraie macro Lingui, catalogue compilé).

## Pas fait ici

OAuth Gmail (flux de consentement absent : carte grisée), persistance du dernier relevé IMAP (la carte montre l'adresse, l'hôte et le dossier, pas « dernière lecture »), `emailProfiles.setUsage` / `pause` côté API (la console passe par `updateIntegration` et `workspaces.update`), réputation par `veridian_profile_id` (lot 4), Cold outreach au niveau workspace non réduit (lot 5), fournisseurs API (SES, Postmark...) non proposés dans l'assistant (ils restent visibles et modifiables : nom, expéditeurs, usage, règles).
