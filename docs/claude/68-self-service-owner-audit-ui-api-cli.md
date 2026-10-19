# 68 — Self-service owner : audit UI / API / CLI du 19/10/2026 (workspace jetable, console de PROD, headless)

Consigne (Robert, 19/10) : un OWNER fait tout dans l'UI, Robert fait tout par API/CLI, sans agent ni root.
Méthode : workspace jetable `clitest19y`, connexion par le lien d'auto-login, Playwright headless sur
`notifuse.app.veridian.site` : chaque parcours est joué pour de vrai, les appels API et messages UI relevés.
Côté API owner et CLI : `notifuse-admin` couvre 209/209 routes (`coverage_test.sh` vert) ; l'API owner est ouverte
à l'owner humain (JWT) pour toutes les lignes ci-dessous.

| Ressource / réglage | UI owner avant | API owner | CLI admin | UI owner après | Note |
|---|---|---|---|---|---|
| Profil d'envoi SMTP + mot de passe | oui (SMTP + IMAP, champ Password) | oui | oui (`integrations:create-smtp`, `profiles:create`) | oui | secret masqué, jamais réaffiché |
| Profil Gmail (mot de passe d'application) | oui | oui | oui | oui | |
| Boîte IMAP + mot de passe | oui (« Add an inbox ») | oui | oui (`create-imap`) | oui | |
| LLM (Anthropic, OpenAI) + clé | NON : l'écran n'affichait que les intégrations existantes | oui | oui | oui (boutons « Add Anthropic / OpenAI ») | clé en champ mot de passe, « laisser vide pour conserver » |
| Firecrawl + clé | NON (idem) | oui | oui | oui (« Add Firecrawl ») | |
| Supabase + clés de hook | NON (idem) | oui | oui | oui (« Add Supabase ») | |
| Webhooks sortants + secret | oui | oui | oui | oui | secret généré côté serveur |
| Clés API (création, révocation) | oui (clé affichée une seule fois) | oui | oui | oui | |
| Jeton d'installation agent | oui | oui | oui | oui | |
| Inviter / renvoyer une invitation | NON : 503 « hub invitation flow disabled » (la route Hub n'est pas configurée en prod) | oui | oui (`members:invite`) | oui (repli automatique sur l'invitation locale sur 503) | |
| Retirer un membre, permissions | oui | oui | oui | oui | |
| Listes, contacts, segments, modèles, automations, broadcasts, transactionnel | oui | oui | oui | oui | modèles : mode Texte (fiche 67) |
| Réglages généraux du workspace | oui (enregistrement 200) | oui | oui | oui | |
| Plafonds, fenêtres, anti-détection, classes exclues | oui (Cold outreach, Apply / Save) | oui | oui | oui | |
| Étiquettes de champs personnalisés | oui | oui | oui | oui | |
| Suppression du workspace | oui (Danger Zone) | oui | oui (`workspaces:delete`) | oui | |
| Gestionnaire de fichiers (S3) : saisir ses clés | oui (formulaire) | oui | oui | oui | LIMITE ci-dessous |

Parcours SOUMIS pour de vrai dans l'UI (statut API relevé) : boîte IMAP avec mot de passe (201), webhook (201), clé
API (200, jeton affiché une fois), réglages généraux (200), liste (201), boîte IMAP sur 127.0.0.1 (400 avec raison),
invitation (503 avant correctif). Formulaires ouverts et inspectés sans soumission : profil SMTP+IMAP et Gmail,
gestionnaire de fichiers, Cold outreach, Danger Zone.

## Garde réseau (SSRF) : message clair

Une IP interne saisie dans une intégration IMAP/SMTP ou un webhook est refusée par le serveur avec la raison
(`host 127.0.0.1 targets a disallowed address: ...`, HTTP 400). L'écran des intégrations n'affiche plus un
« Failed to save » muet : il ajoute le détail renvoyé par le serveur.

## Corrigé dans ce lot

1. `Integrations.tsx` : boutons « Add Anthropic (Claude) / Add OpenAI / Add Firecrawl / Add Supabase » (owner seulement),
   message d'erreur avec le détail serveur.
2. `WorkspaceMembers.tsx` : l'invitation passe par le Hub ; sur 503 (Hub non configuré côté Notifuse), repli sur
   l'invitation locale (route owner-only de la fiche 67). Une autre erreur (502 Hub en panne...) n'est PAS contournée.
3. Tests vitest : `Integrations.add-buttons.test.tsx` (owner voit les 4 boutons, non-owner aucun, création Firecrawl
   avec clé en champ mot de passe), `WorkspaceMembers.invite-fallback.test.tsx` (repli sur 503, pas de repli sur 502,
   Hub seul s'il répond). Les 3 tests de comportement échouent sur l'ancien code (vérifié).

## Reste fermé volontairement (inchangé)

Clé de rôle member sur la gestion du workspace (équipe, clés, jetons, intégrations, webhooks, suppression) ; tout accès
cross-workspace ; `generateMagicLink` réservé aux clés gérées par le Hub.

## Limites connues, NON corrigées ici

- Gestionnaire de fichiers : le navigateur parle directement à S3 avec la clé, or l'API ne renvoie plus jamais le
  secret (fiche 10). Un owner peut saisir sa configuration mais l'explorateur ne peut pas relire une clé déjà
  enregistrée : il faut un proxy S3 côté serveur (URLs présignées). Chantier à part, aucun workspace prod n'a de
  FileManager configuré.
- `lists.create` renvoie 500 « Failed to create list » sur un identifiant déjà pris (devrait être un 409 lisible).

## Correctif trouvé par l'audit en prod : URL de base Firecrawl / OpenAI

Mesure en prod (19/10) : une intégration Firecrawl avec base_url `http://169.254.169.254/` était ACCEPTÉE (201) :
le client Firecrawl était déjà gardé à l'usage, mais le refus arrivait tard et en silence, et le client OpenAI
(endpoint compatible, `base_url` du locataire) n'était pas gardé du tout. Corrigé : `domain.ValidateTenantBaseURL`
(http/https, pas d'identifiants, IP interne refusée avec la raison) appelé par `FirecrawlSettings.Validate` et
`OpenAISettings.Validate` ; le flux OpenAI passe par `NewTenantOutboundClient` quand une base_url est posée.
Anthropic n'a pas de base_url. Tests : `veridian_outbound_base_url_test.go`, `llm_service_openai_netguard_test.go`.

Mesure invitation en prod (UI headless) : 503 de la route Hub puis 200 de `/api/workspaces.inviteMember` (repli).
Suppression du workspace par l'UI : parcours de confirmation non joué (sélecteur) ; le jetable est effacé par
`notifuse-admin wipe`.
