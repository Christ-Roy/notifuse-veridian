# 53. Console assumée : inventaire et plan (fork Notifuse Veridian)

Date : 07/10/2026. Base mesurée : `origin/veridian` au commit `503dba2e`. Document d'inventaire et de spécification : aucun code produit, aucun push, aucune écriture sur `robertbrunon`.

Méthode : lecture du code (Go et console React), mesure du catalogue i18n par script, comparaison de ce que l'écran affiche avec ce que le worker applique. Les numéros de ligne sont ceux du commit ci-dessus.

---

## 0. Ce qu'il faut retenir

1. **Il n'existe aucune vue qui dit la vérité d'un profil.** Le plafond effectif du jour (le minimum de la chauffe, du plafond profil, des plafonds par classe et par adresse émettrice, de la fenêtre) n'est calculé qu'en Python dans le CLI (`_effective_caps_for_integration`, `notifuse_common.py:2710`). Le worker Go a sa propre logique (`veridianResolveDailyCaps`, `veridianDailyCapGate`, `veridianReserveDailyQuota`). Deux implémentations, aucune côté API, aucune dans l'UI.
2. **La ligne « Limite de débit pour le marketing : 6 emails par minute ≈ 360 par heure » est fausse comme capacité.** Elle affiche `rate_limit_per_minute` (`Integrations.tsx:2518-2534`). Ce champ existe toujours et le worker l'applique encore comme cadence technique (`worker.go:466`, multiplié par le nombre d'adresses émettrices). Mais ce n'est pas la capacité : elle est fixée par le plafond du profil, la chauffe, les plafonds par classe, la fenêtre et le fusible.
3. **Rien n'empêche un même profil d'être dans la rotation commerciale ET d'être le profil transactionnel.** `Workspace.Validate` contrôle le pool (`ValidateVeridianMarketingEmailProfiles`) mais jamais `TransactionalEmailProviderID` (aucune référence dans `workspace.go` hors lecture, ligne 747 pour le pool). Le bouton « Utiliser pour le transactionnel » est proposé sur les profils du pool.
4. **Le fusible de réputation compte par domaine d'envoi dans tout `message_history`**, transactionnel compris (`CountHardBouncedSinceForSenderDomain`, `message_history_postgre.go:1502`). Un profil transactionnel sur le même domaine que le commercial peut geler le commercial, et inversement. Et le fusible n'a aucun écran : `messages.reputationStatus` existe côté API, la console ne l'appelle jamais.
5. **La boîte IMAP n'est liée à aucun profil.** C'est une intégration `type: imap` autonome (`veridian_imap_integration.go`), la console n'en gère qu'une seule (`find(i => i.type === 'imap')`, `veridian_cold_outreach_settings.tsx:176`), enfouie dans Réglages, Cold outreach, Reply & bounce inbox. Dans la liste des intégrations elle tombe sur la carte générique `Type: imap` (`Integrations.tsx:1707`), exactement ce que montre la capture de Robert.
6. **Le blog pèse 20 000 lignes de console** (`components/blog` + `blog_editor` : 20 266 lignes, plus `BlogPage` 186 et `BlogSettings` 475) et 1 994 lignes de service Go, pour zéro usage Veridian.
7. **Traductions : 423 messages sur 2 774 sans traduction française (15,2 %)**, dont 236 dans les trois fichiers qui forment l'écran Intégrations. En, 0 manquant. Détail en section 3.
8. **Réglages en double à trois niveaux** (broadcast, profil, workspace) pour huit familles de limites, avec une règle de résolution qui remplace la table entière au premier niveau non vide (pas de fusion par classe). Section 2.3.
9. **Le suivi de l'upstream sur la console est déjà perdu en pratique** : 106 commits de retard, 440 fichiers console modifiés côté upstream depuis la base, 140 côté fork, 66 en commun. `Integrations.tsx` : +656/-136 côté fork, +593/-61 côté upstream. Recommandation section 6.
10. **La lecture et la réponse aux mails n'existent pas** : le poller IMAP lit, transmet aux consommateurs (détection de réponse, rejets) et acquitte l'UID. Il ne stocke aucun message.

---

## 1. Inventaire des features Veridian du fork (backend)

Légende : **UI** = où la console l'affiche aujourd'hui. **Règle** = la règle affichée correspond-elle à ce que fait le worker. **Doublon** = recoupe un réglage natif ou un autre réglage Veridian.

Ordre des portes dans le worker pour un envoi commercial (`worker.go:300-540`, `veridian_pool_failover.go:322`) : classification MX du destinataire, sélection du profil dans le pool (exclusion de classe, fusible réputation, débit par classe, plafond journalier par classe/chauffe, plafond par adresse émettrice, fenêtre d'envoi, rejouées par candidat), pré-filtre d'adresse, rendu au dépilage, anti-hash (journal seulement), débit natif du profil, contrôles automation/broadcast, **réservation atomique du quota journalier du profil** (dernière porte), envoi SMTP.

### 1.1 Plafonds, débits, rythme

| Feature | Où dans le code | UI aujourd'hui | Règle juste ? | Doublon ? |
|---|---|---|---|---|
| Plafond journalier du profil `veridian_profile_daily_cap` (défaut Gmail 30, max 450 perso, 1800 Workspace, via `veridian_gmail_account_type`) | `email_provider.go:113,307-326`, `veridian_daily_quota.go` | Formulaire profil (`Integrations.tsx:1884`), carte « Daily profile cap » avec l'usage du jour | Oui pour le Gmail. Pour un SMTP, 0 = pas de plafond profil. Affiche « Today's usage is unavailable » hors pool (voir 1.4) | Non |
| Plafond par classe de destinataire `veridian_provider_class_daily_cap` | profil, workspace, metadata broadcast ; `veridian_daily_cap.go:87` | Cold outreach (workspace) et `InfraLimitsCard` (profil) | Règle de résolution trompeuse : le premier niveau non vide remplace la table entière, sans fusion par classe | Oui : 3 niveaux |
| Débit par classe `veridian_provider_class_rates` (emails/min) | `veridian_provider_throttle.go:46` | idem | Idem (remplacement de table) | Oui : 3 niveaux, et chevauche le débit natif du profil |
| Plafond par destinataire `veridian_per_recipient_daily_cap` | profil, workspace, broadcast | Carte « Per-recipient daily cap » | Oui (0 = illimité) | Oui : 3 niveaux |
| Plafond par adresse émettrice `veridian_per_sender_daily_cap` | `veridian_per_sender_cap.go` | Carte « Per-sender daily cap » | Oui | Oui : 3 niveaux |
| Chauffe progressive (`veridian_warmup_started_at`, `_schedule`, `_step_days`) | `veridian_warmup.go` ; calculée à la lecture, pas de cron | `InfraLimitsCard` (profil seulement) | Oui. Quand elle est active, elle PRIME sur le plafond par classe et le court-circuite. Cette priorité n'est dite nulle part dans l'UI | Non (niveau profil seulement) |
| Fenêtre d'envoi `veridian_sending_window` (jours, heures, fuseau) | `veridian_sending_window_gate.go` | `SendingWindowCard`, jours codés en dur en français (`veridian_cold_outreach_settings.tsx:1115-1121`) | Oui | Oui : 3 niveaux. Doublon fonctionnel avec la fenêtre d'une automation ? non mesuré |
| Jitter `veridian_jitter_pct` (défaut 0,30) | `veridian_jitter.go` | `JitterAntiHashCard` | Oui | Oui : 3 niveaux |
| Anti-hash `veridian_anti_hash_enabled` / `_window_hours` (défaut ON, 72 h) | `veridian_content_hash*.go` | idem | Écran dit « bloque » ; le garde du worker est **journal seulement** (`worker.go:451-457`). La variété est assurée à l'enqueue (spintax). À dire correctement | Oui : 3 niveaux |
| Exclusion de classes `veridian_excluded_provider_classes` | `veridian_excluded_class_gate.go` | `ExcludedClassesCard` | Oui. Sortie en échec définitif si toutes les classes du pool sont exclues | Oui : 3 niveaux |
| Pixel d'ouverture par classe `veridian_open_pixel_by_class` | `veridian_open_pixel.go` | Cold outreach, tri-état par classe | **Contradictoire** avec le choix produit (texte brut, zéro pixel, ouvertures à zéro voulues) et avec le réglage natif `email_tracking_enabled` (Réglages, Général) | Oui : natif + 3 niveaux |
| Domaine de tracking `veridian_tracking_domain` | `veridian_tracking_domain.go` | `TrackingDomainCard` (profil) | Utile seulement si le tracking est actif. Inutile en texte brut | Oui : recoupe `custom_endpoint_url` du workspace (Général) |
| Rotation des adresses émettrices (round robin) | `veridian_sender_rotation.go`, `VeridianEffectiveRateLimit` = débit natif × nombre d'adresses | Pas d'écran propre ; alerte « Round-robin needs at least 2 sending addresses » dans les presets | Le débit natif est **multiplié** par le nombre d'adresses, ce que l'écran natif ne dit pas | Non |

Presets de politique d'envoi (`VERIDIAN_SENDING_POLICY_PRESETS`, trois jeux de valeurs : chauffe, croisière, prudence Microsoft) : écrasent les valeurs du **workspace**, pas celles d'un profil. Gardables, mais à ré-aiguiller vers le profil (section 4).

### 1.2 Pool, rotation, bascule

| Feature | Où | UI | Règle juste ? | Doublon ? |
|---|---|---|---|---|
| Pool de rotation `veridian_marketing_email_provider_ids` | `veridian_email_profiles.go`, `workspace_service.go:1985` | Bouton « Add to rotation / Remove from rotation » sur chaque carte ; bandeau `SendingProfilesOverview` | Un profil non testé est refusé (sauf le singleton historique) | **Oui, avec `marketing_email_provider_id` natif** : les deux coexistent, le singleton reprend la tête du pool à la suppression (`veridianRemoveEmailProfileReference`) |
| Bascule en cours d'envoi (failover) | `veridian_pool_failover.go` | Rien | Ordre : continuité de séquence, profil assigné, puis le reste du pool | Non |
| Équilibrage du pool | branche `agent/pool-balance-20261006` (image 6168d28d en prod) | Rien | Non audité ici | Non |
| Transport vérifié `veridian_transport_verified_at` | `email_service.go:128-155` | Badges « Ready / Not tested » | Remis à nul à chaque modification de transport (`workspace_service.go:1604,1740`). Juste | Non |
| Mise en pause d'un profil | **n'existe pas** | « Remove from rotation » en tient lieu | La retrait du pool ne met pas en pause les entrées déjà en file | Manque |

### 1.3 Réputation, rejets, réponses

| Feature | Où | UI | Règle juste ? | Doublon ? |
|---|---|---|---|---|
| Fusible de réputation par domaine d'envoi : 7 jours glissants, bounces durs ≥ seuil, ou une plainte (`veridian_reputation_gate.go`) | worker + `messages.reputationStatus` | **Aucune**. Seul le seuil est réglable, côté API/CLI (`veridian_hard_bounce_freeze_threshold`, 1 % à 15 %, défaut 3 %) | Le dénominateur et le périmètre (tout `message_history` du domaine) mélangent transactionnel et commercial. Échec fermé sur erreur DB | Non |
| Gel par fournisseur destinataire (en cours chez un autre agent) | non visible dans `origin/veridian` | Aucune | Voir le contrat de lecture proposé en 3.4 | n/a |
| Sortie de séquence sur réponse (`veridian_reply_service.go`, `veridian_cold_exit.go`) | poller IMAP, détection par Message-ID (fort) puis expéditeur (faible) | Rien d'autre que des événements timeline | Juste, idempotente | Non |
| Typage des réponses humaine/auto/challenge (`veridian_reply_type.go`) | consommateur de réponses | `EmailMetricsChart` (réponses humaines) | Juste | Non |
| Rejets (NDR) : parse DSN, chaîne native de suppression (`veridian_bounce_consumer.go`) | poller IMAP | Colonne Bounces du tableau de bord (hard/soft) | Juste | Non |
| Pré-filtre d'adresse (syntaxe, jetable, DNS) (`veridian_prefilter.go`) | worker | « Deliberately excluded » dans le tableau de bord | Juste | Non |
| Blocklist clients | **hors fork** : fichier `batch/client-domain-blocklist.txt` côté acquisition, statut natif `unsubscribed`, alerte `client_blocklist_breach` | Aucune | Aucune garde dans Notifuse lui-même | Angle mort : un client louant l'outil n'a aucun « ne jamais contacter » |
| Mot de passe d'application Gmail et OAuth | `veridian_gmail_*`, `email_provider_smtp.go` | Formulaire Gmail (lien apppasswords présent, `Integrations.tsx:1822`) ; OAuth annoncé « Coming soon » et désactivé (`:1244`, `:2603`) | Le transport OAuth Google existe (SMTP `auth_type=oauth2`, `oauth2_provider=google`, client id/secret/refresh token) mais **aucun flux de consentement** n'existe dans la console | Non |

### 1.4 Usage, analytics, API

| Feature | Où | UI | Remarque |
|---|---|---|---|
| Usage du jour par profil `emailProfiles.usage` | `veridian_email_profile_usage.go` | Carte profil et bandeau | **Limité au pool commercial** (`VeridianMarketingEmailProfiles()`). Un profil hors pool, dont le profil transactionnel, n'a pas d'usage |
| Ventilation des contacts par classe `contacts.providerBreakdown` | `veridian_provider_breakdown.go` | Aide dans Cold outreach | Utile, à garder dans la page Profils |
| Réponses `messages.replyStats` | `veridian_reply_stats_service.go` | `EmailMetricsChart` | Pas par séquence ni par segment (reste 6bis) |
| Engagement par classe `messages.engagementByClass` | `veridian_engagement_by_class_service.go` | `veridian_engagement_by_class.tsx` | Mesure des ouvertures alors que le tracking est coupé : à revoir |
| Score de délivrabilité d'un modèle `templates.deliverabilityScore` | `veridian_deliverability_score_service.go` | `TemplatePreviewDrawer` | À garder |
| Rendu au dépilage (`veridian_render_at_send.go`), garde texte résiduel, HTML vers texte | worker | Rien | Invisible, bon |
| API et agents : clés, grâce de rotation, transfert de propriété, `/agent/install.sh` | `veridian_rotate_transfer_service.go`, `veridian_api_key_grace_cleanup.go`, `ApiAgentsSettings.tsx` | Réglages, API & agents | À garder (Développeurs) |
| Idempotence, jetons, webhook vers le Hub, plan et paywall, soft delete, auto-login, GC des bases orphelines | `veridian_*` divers | Section Plan, bandeaux | Hub/Plan : à garder mais hors périmètre « envoi » |

### 1.5 Réglages en double : la cascade

Résolution actuelle, huit familles (classes quotidiennes, débits par classe, destinataire, émetteur, fenêtre, jitter, anti-hash, exclusion) :

`metadata du broadcast` (figé dans le payload à l'enqueue, `VeridianApplyProviderThrottle`) puis `profil` puis `workspace`.

Trois défauts de conception :
- **Remplacement de table.** Un profil qui pose `{google: 50}` supprime silencieusement les plafonds `microsoft`, `yahoo_aol`… posés au workspace (`veridian_daily_cap.go:87-97`). C'est le « réglage qui prime en silence » de la reprise.
- **Figé à l'enqueue.** Le plafond posé au broadcast ou au profil au moment de la mise en file reste dans le payload (`_queue_staleness` du CLI le mesure). Changer le profil ne change pas les mails déjà en file.
- **Aucun écran ne montre la valeur effective ni son origine.** Le CLI a la logique (source : profil, workspace, défaut) ; l'UI non.

---

## 2. Inventaire de la console

### 2.1 Routes (`router.tsx`) et sidebar (`WorkspaceLayout.tsx:218-410`)

Sidebar actuelle, dans l'ordre : Dashboard, Contacts, Lists, Templates, Broadcasts, Automations, Transactional, **Blog**, File Manager, Logs, Settings.

| Route | Page | Dans la sidebar | Lignes | Verdict |
|---|---|---|---|---|
| `/console/` | `DashboardPage` (sélecteur de workspace, tiroir réglages système) | non | 102 | **Garder** |
| `/console/signin`, `/logout`, `/setup`, `/accept-invitation`, `/workspace/create` | connexion, installation, invitation | non | 219 / 32 / 702 / 180 / 281 | **Garder** (l'assistant d'installation à relire pour l'i18n : 13 messages fr vides) |
| `/workspace/$id` (index) | `AnalyticsPage` : tableau de bord | oui, « Dashboard » | 114 | **Garder, refondre** : devient le tableau de bord prospection (reprise 6bis). Retirer les deux cartes « Transactional Provider / Marketing Provider » (`AnalyticsDashboard.tsx:158-196`), fusionnées dans Profils d'envoi |
| `/workspace/$id/analytics` | `AnalyticsPage` (même composant que l'index) | non | | **Supprimer la route** (redirection vers l'index) : doublon pur |
| `/workspace/$id/dashboard` | redirection vers l'index | non | | **Garder inerte** (liens anciens), commentaire de 2026-06-14 |
| `/workspace/$id/contacts` | `ContactsPage` (segments inclus) | oui | 1 199 | **Garder** (section Commercial) |
| `/workspace/$id/lists` | `ListsPage` | oui | 353 | **Garder** (Commercial) |
| `/workspace/$id/templates` | `TemplatesPage` | oui | 447 | **Garder**, un onglet par catégorie (Commercial, Transactionnel) |
| `/workspace/$id/broadcasts` | `BroadcastsPage` | oui | 1 476 | **Garder**, renommer « Campagnes » (Commercial) |
| `/workspace/$id/automations` | `AutomationsPage` | oui | 247 | **Garder**, renommer « Séquences » (Commercial) |
| `/workspace/$id/transactional-notifications` | `TransactionalNotificationsPage` | oui, « Transactional » | 498 | **Garder** (section Transactionnel) |
| `/workspace/$id/blog` | `BlogPage` | oui | 186 (+ 20 266 de composants) | **Supprimer de l'UI** (voir 2.2) |
| `/workspace/$id/file-manager` | `FileManagerPage` | oui | 128 | **Masquer** de la sidebar (reste atteignable par le sélecteur d'images des modèles). Texte brut = usage faible. À confirmer qu'aucun workspace client n'en dépend avant retrait définitif |
| `/workspace/$id/logs` | `LogsPage` : onglets Message History, Incoming Webhooks, Outgoing Webhooks | oui | 56 | **Garder**, renommer « Journal d'envoi ». Fusion : l'onglet Outgoing Webhooks (livraisons) rejoint Réglages, Webhooks (configuration), sous « Développeurs » |
| `/workspace/$id/debug-segment` | `DebugSegmentPage` (listes factices, `console.log`) | non | 105 | **Supprimer** : page de debug sans entrée, jeu de données fictif |
| `/workspace/$id/settings` | redirection vers `settings/team` | oui, « Settings » | | **Garder** |
| `/workspace/$id/settings/$section` | `WorkspaceSettingsPage` | | 213 | **Garder** (sections ci-dessous) |

Pas de route « Profils d'envoi » : c'est le manque principal.

Sections de Réglages (`SettingsSidebar.tsx`, `WorkspaceSettingsPage.tsx`) :

| Section | Contenu | Verdict |
|---|---|---|
| Team | membres, permissions | **Garder** |
| Integrations | profils d'envoi (SMTP, Gmail, SES, SparkPost, Postmark, Mailgun, Mailjet, SendGrid) + Supabase + LLM + Firecrawl + intégrations IMAP (carte générique) | **Éclater** : tout ce qui est profil d'envoi et IMAP passe dans la nouvelle page. Supabase, LLM, Firecrawl : **masquer** (inertes) après mesure qu'aucun workspace n'en a, voir 6 |
| Webhooks | abonnements sortants | **Garder** (Développeurs) |
| **Blog** | `BlogSettings` | **Supprimer** |
| Custom Fields | libellés des champs personnalisés | **Garder** |
| SMTP Bridge | relais SMTP entrant pour le transactionnel | **Fusionner** dans la section Transactionnel (c'est le point d'entrée des apps clientes) |
| General | nom, site, fuseau, langues, tracking ouvertures/clics, domaine d'endpoint | **Garder**, mais le tracking et le domaine d'endpoint ne doivent plus avoir de jumeau par profil (voir 1.1) |
| Plan | plan Veridian | **Garder** |
| Cold outreach | 8 familles de limites au niveau workspace, 4 cartes par infra, IMAP, presets (2 715 lignes, dont la moitié est le même rendu écrit deux fois : lecture seule et édition) | **Fusionner** dans Profils d'envoi (limites par profil) ; ne reste au niveau workspace qu'une « politique par défaut des nouveaux profils » (section 4.5) |
| API & agents | clés, commande d'installation | **Garder** (Développeurs) |
| Danger Zone | suppression | **Garder** |

### 2.2 Suppression du blog

Périmètre console à retirer : entrée sidebar (`WorkspaceLayout.tsx:340-363`), `selectedKey = 'blog'` et `currentPath.includes('/blog')` (lignes 74, 159), route et `BlogSearch` (`router.tsx:322`), page, section Réglages (`SettingsSidebar`, `WorkspaceSettingsPage:160`), `components/blog`, `components/blog_editor`, `services/api/blog.ts`, `utils/mockBlogData.ts`, `liquidRenderer/liquidConfig` (références), `components/seo` (si seulement utilisé par le blog), permissions `blog` dans `WorkspaceMembers.tsx` et `AuthContext.tsx`, interrupteur `blog_enabled` de Général. Les routes et services Go du blog (`blog_handler.go`, `blog_service.go`, 1 994 lignes) restent **inertes** : recommandation confirmée (aucune valeur à les enlever côté serveur, ils suivent l'upstream sans coût). Une seule précaution : `blog_enabled` doit rester à `false` par défaut pour qu'aucune page publique ne soit servie par erreur.

### 2.3 Écran Intégrations : défauts mesurés

- Cartes d'intégration : titre libre `Type: ${integration.type}` pour tout ce qui n'est ni email, Supabase, LLM ni Firecrawl (`Integrations.tsx:1707`) : c'est le cas de l'IMAP.
- Libellés en anglais en dur : « Edit SUPABASE Integration », « Add New … Integration », « Edit Firecrawl Integration », « Enabled/Disabled » du bac à sable Mailjet.
- Boutons sans verbe de rôle : « Use for Transactional » et « Add to rotation » sur la même carte, avec des règles différentes.
- Alerte « Email Provider Configuration Needed » recommande de brancher un fournisseur marketing et un transactionnel, sans dire qu'ils doivent être distincts.
- `SendingProfilesOverview` dit « Add another profile to spread marketing volume across independent Gmail accounts » : tourné Gmail uniquement.

---

## 3. Traductions

Mesure par script sur `console/src/i18n/locales/*.po` (HEAD `503dba2e`).

| Langue | Messages | Sans traduction |
|---|---|---|
| en | 2 774 | 0 |
| **fr** | 2 774 | **423 (15,2 %)** |
| de, es, it, ja, pt-BR, ca | 2 774 | 589 chacune |

Fichiers qui concentrent les trous du français : `veridian_cold_outreach_settings.tsx` **159**, `Integrations.tsx` **52**, `SystemSettingsDrawer.tsx` 27, `veridian_sending_profiles_ui.tsx` **25**, `MjmlCodeEditor.tsx` 19, `TemplatePreviewDrawer.tsx` 15, `SetupWizard.tsx` 13, `BulkActionsBar.tsx` 10, `BlogSettings.tsx` 10, puis un long reste.

Les trois fichiers de l'écran vu par Robert totalisent **236 messages vides**. Exemples vérifiés vides en français : « Credentials saved », « Remove from rotation », « Daily profile cap », « Recipient-provider policy », « Marketing rotation », « Sending profile rotation », « Reply & bounce inbox (IMAP) », « Rate & volume », « Not tested ». Seuls quelques libellés ont une traduction (« Use for Transactional », « Rate Limit for Marketing »).

Autres écarts :
- Chaînes non traduisibles : jours de la semaine en français en dur (`veridian_cold_outreach_settings.tsx:1115-1121`) alors que le reste est en anglais ; titres en anglais en dur dans les tiroirs Supabase, LLM, Firecrawl.
- Huit messages identiques à l'identifiant (probables non traduits) : `Cold outreach`, `{0} emails/min`, plusieurs libellés de fournisseurs.
- Le catalogue est à jour par rapport au code (les chaînes statiques `t` des fichiers concernés sont toutes extraites ; les 25 « absentes » repérées par le script sont des backticks dans des commentaires).
- Piège Lingui déjà documenté dans le code : un `t` passé en paramètre d'une fonction hors composant renvoie vide (`veridian_plan_settings.tsx:36`, `classLabel`). À retenir pour les nouveaux écrans.

Contrôle à poser (lot 1) : un test qui échoue si un fichier `veridian_*.tsx` ou de la nouvelle page contient un message dont le `msgstr` français est vide. Les langues autres que fr et en : **ne pas les maintenir** (589 trous chacune) ; les retirer du sélecteur pour ne pas afficher d'interface à moitié anglaise.

---

## 4. Spécification de la page « Profils d'envoi »

### 4.1 Place et structure

Entrée de sidebar « Profils d'envoi » (icône enveloppe), dans la section ENVOI, au-dessus de « Journal d'envoi ». Route `/console/workspace/$id/sending-profiles`. Accès : membres avec permission lecture ; édition réservée au propriétaire (même règle que `isOwner` actuel).

En-tête : titre, bouton **Ajouter un profil**, et un bandeau de synthèse : capacité réelle du jour (somme des plafonds effectifs des profils commerciaux), envoyés aujourd'hui, profils gelés ou en pause.

Deux groupes de cartes : **Commercial** (rotation) puis **Transactionnel** (un profil réservé). Aucun profil n'est dans les deux.

Les fournisseurs API (SES, SparkPost, Postmark, Mailgun, Mailjet, SendGrid) restent possibles sous un lien « Autre fournisseur » de l'assistant, sans carte spéciale : même carte, type affiché par le nom du fournisseur.

### 4.2 La carte d'un profil

Une carte, quatre blocs. Libellés en français.

1. **Identité** : nom ; pastille de type (`SMTP`, `Gmail, mot de passe d'application`, `Gmail, OAuth`, ou nom du fournisseur API) ; statut de transport : `Vérifié le JJ/MM HH:mm` (vert) ou `À tester` (orange, avec le bouton Tester en évidence) ; expéditeur(s) : liste `Nom <adresse>` avec l'adresse par défaut marquée ; **usage** : sélecteur exclusif à deux valeurs, `Commercial` ou `Transactionnel` (pas de « Non assigné » : un profil créé sans usage est commercial hors rotation tant qu'il n'est pas testé).
2. **Règles du jour** (le cœur de la page) :
   - `Plafond effectif aujourd'hui : N envois`, et dessous `Limité par : <facteur>` avec une phrase. Facteurs possibles, dans l'ordre de la logique du worker : `Chauffe (jour 4 sur 12)`, `Plafond du profil`, `Plafond par adresse émettrice × K adresses`, `Plafond de la classe Google`, `Fenêtre d'envoi fermée (reprise à 08:00)`, `Gelé par le fusible de réputation (taux de rejets durs 4,1 % ≥ 3 %)`, `En pause`, `Profil non vérifié`.
   - Barre de progression `utilisés / plafond` avec `acceptés` (SMTP a répondu 250) et `réservés` côte à côte quand ils diffèrent (le quota réserve avant l'envoi).
   - Tableau compact par classe de destinataire : débit (par minute), plafond du jour, utilisé, avec l'origine de la valeur (`Profil`, `Défaut`, `Chauffe`).
   - Fenêtre d'envoi : `Lun-Ven 08:00-19:00 Europe/Paris`, ouverte ou fermée maintenant.
   - **Aucune mention de « emails par minute ≈ par heure ≈ par jour »**. La cadence technique du transport (ex-« Limite de débit pour le marketing ») n'apparaît que dans un volet « Avancé » du formulaire d'édition, sous l'intitulé « Cadence technique maximale du transport (sécurité) », avec la phrase « Ce n'est pas la capacité du profil ».
3. **Réputation** : pastille globale (`Sain`, `Surveillé`, `Gelé`) pour ce profil ; sept jours glissants : envoyés, rejets durs, plaintes, taux, seuil du fusible (réglable ici). Puis une ligne par fournisseur destinataire (Google, Microsoft, Yahoo/AOL, FAI français, entreprise, autres) avec son état. Le gel par fournisseur destinataire est **en cours chez un autre agent** : la page lit un champ optionnel `by_provider_class` du contrat de la section 4.4 et ne montre le détail que s'il existe. En attendant, la ligne globale du profil suffit.
4. **Boîte de retour liée** : si une boîte IMAP est liée, `<adresse>` + `Réponses et rejets` + état (`Connectée`, `Erreur de connexion depuis HH:mm`, `Dernière lecture il y a 2 min`) + compte du jour (réponses reçues, rejets traités). Sinon : `Aucune boîte liée` et le bouton `Lier une boîte` (voir 4.5). Trois mots seulement dans la carte ; le détail IMAP (hôte, port, dossier, intervalle) s'ouvre dans un tiroir.

Actions de la carte, dans cet ordre : `Tester` (envoie à l'adresse du propriétaire, met à jour « Vérifié le »), `Mettre en pause` / `Reprendre`, `Retirer de la rotation` (profils commerciaux seulement, désactivé si dernier profil), `Modifier`, `Supprimer` (désactivé s'il reste des entrées en file : l'API le refuse déjà, `ErrEmailIntegrationQueueActive`). La confirmation de pause dit combien d'entrées en file sont concernées.

### 4.3 Assistant « Ajouter un profil » (trois choix)

Un tiroir, une première étape à trois grandes cartes, puis un formulaire court. Aucun champ « débit » ni « plafond » à l'ajout : le profil démarre avec la politique par défaut et la chauffe proposée (section 4.6).

**Choix 1. SMTP + IMAP.** Champs : nom du profil, adresse expéditrice et nom affiché, hôte/port/chiffrement/identifiant/mot de passe SMTP ; case `Lier une boîte de retour` cochée par défaut avec hôte/port/identifiant/mot de passe IMAP (ou `Utiliser une boîte déjà configurée`, liste des IMAP existants). Bouton `Tester et enregistrer` : teste SMTP, teste la connexion IMAP, enregistre les deux et les lie.

**Choix 2. Gmail, mot de passe d'application.** Encart de trois étapes, avec le lien **https://myaccount.google.com/apppasswords** en bouton principal (ouvre dans un nouvel onglet) :
1. Activer la validation en deux étapes sur le compte Google (https://myaccount.google.com/signinoptions/two-step-verification).
2. Ouvrir la page des mots de passe d'application, créer un mot de passe nommé « Notifuse ».
3. Copier les 16 caractères et les coller ci-dessous (le mot de passe Gmail habituel ne fonctionne pas).
Champs : adresse Gmail, nom affiché, mot de passe d'application (en écriture seule), type de compte (Gmail personnel ou Google Workspace, qui fixe le plafond maximal 450 ou 1 800), plafond journalier (défaut 30). SMTP (`smtp.gmail.com`) **et IMAP** (`imap.gmail.com:993`, même adresse, même mot de passe) sont créés et liés d'un seul geste : l'utilisateur ne saisit le secret qu'une fois. Un avertissement dit que l'IMAP Gmail doit être activé (Gmail, Paramètres, Transfert et POP/IMAP).

**Choix 3. Gmail, OAuth.** Aujourd'hui désactivé « Coming soon ». Le transport existe côté serveur (SMTP OAuth2 Google) mais il manque le flux de consentement. À spécifier ici comme étape finale du lot 3 : bouton `Se connecter avec Google` qui lance le consentement (scopes SMTP et IMAP), stocke le refresh token chiffré et crée SMTP + IMAP liés. Tant que le flux n'existe pas : la carte reste visible, grisée, avec la mention `Bientôt` et le lien de repli vers le choix 2. Ne pas livrer un faux bouton.

Tous les secrets : champs en écriture seule, jamais renvoyés (déjà vrai pour `SMTPSettings.MarshalJSON` et `IMAPSettings.MarshalJSON`), jamais loggés.

### 4.4 Contrat d'API à créer (un seul appel par écran)

Ajouter, dans le même esprit que `emailProfiles.usage`, un endpoint de lecture `GET /api/veridian/emailProfiles.overview?workspace_id=…` qui renvoie pour chaque profil email du workspace :

```
{
  date, timezone,
  profiles: [{
    integration_id, name, kind, mode,              // smtp | gmail_app_password | gmail_oauth | ses | ...
    usage: "commercial" | "transactional",
    in_rotation, paused,
    verified_at, credentials_configured,
    senders: [{ email, name, is_default }],
    effective: {
      daily_cap_today, limiting_factor,            // enum : warmup|profile_cap|per_sender|class_cap|window|frozen|paused|unverified
      limiting_detail,                             // texte court, déjà localisable côté UI
      used_reserved, used_accepted,
      window: { open_now, next_open_at, label },
      warmup: { active, day, of, cap_today },
      per_class: [{ class, rate_per_min, daily_cap, accepted_today, source }],
      per_recipient_daily_cap, per_sender_daily_cap
    },
    reputation: { state, window_days, sent, hard_bounces, complaints, rate, threshold,
                  by_provider_class?: [{ class, state, frozen_until?, reason? }] },
    return_inbox: { integration_id, address, roles, last_polled_at, last_error?, replies_today, bounces_today } | null
  }],
  totals: { capacity_today, accepted_today }
}
```

Règle de construction : **une seule fonction Go, `EffectivePlan(workspace, provider, now)`, appelée à la fois par les portes du worker et par cet endpoint** (test de contrat : les deux retournent le même plafond sur les mêmes entrées). Le port Python du CLI (`_effective_caps_for_integration`) est ensuite remplacé par un appel à l'endpoint, ce qui supprime la troisième implémentation. Par construction l'écran ne peut plus diverger du worker (leçon de la section 5 du CLAUDE global : un instrument qui ne bouge pas avec son paramètre ne le mesure pas).

Endpoints d'écriture, tous testables en CLI : `emailProfiles.create` (type `smtp_imap` | `gmail_app_password`, corps avec secrets lus sur stdin côté CLI, atomique : SMTP + IMAP + lien, ou rien), `emailProfiles.setUsage` (exclusif), `emailProfiles.pause` / `resume`, `emailProfiles.linkInbox`. `Tester` réutilise `TestEmailProviderByIntegrationID` (existant). L'état `paused` est un champ du profil lu par la porte de sélection du pool (une porte de plus dans `veridianSelectSendableIntegration`, tier risque 🔴 du Constitution CI : E2E on-premise obligatoire).

### 4.5 Liaison profil et boîte IMAP

Ajouter au profil un champ `veridian_return_inbox_integration_id` (dans le blob `integrations`, `omitempty`, aucune migration, comme les autres champs R2) ; validation : doit désigner une intégration `type: imap` du même workspace. Un IMAP peut être lié à plusieurs profils (cas du relais Postfix : une seule boîte de rejets pour plusieurs adresses). Rôles du lien : `replies`, `bounces`, par défaut les deux.

Effet sur le comportement : **aucun**. La détection de réponse et de rejet reste globale au workspace (Message-ID, `veridian_reply_detection.go`), le lien sert l'affichage, la santé par profil et l'attribution des réponses par profil (via `message_history.veridian_profile_id`, déjà persisté). Pas de régression possible sur la prospection en cours.

Santé de la boîte : aujourd'hui `lastPolledAt` n'est qu'une carte en mémoire du poller (`veridian_imap_poller.go:72`). Pour afficher « Connectée / erreur depuis… » il faut persister le dernier poll et la dernière erreur (une petite table système, ou un champ dans le blob de l'intégration IMAP). Chiffrer l'effort au lot 2.

Politique par défaut au niveau workspace (remplace les huit familles de réglages workspace) : devient un **modèle** `Politique des nouveaux profils`, copié dans le profil à sa création (copie au moment de l'ajout, pas d'héritage dynamique). Conséquence : plus de valeur effective qui dépend d'un réglage workspace oublié. Le niveau « metadata de broadcast » reste réservé à l'API et n'est pas montré. La résolution backend peut rester `broadcast, profil, workspace` pour rester compatible : on cesse seulement de l'éditer au workspace.

### 4.6 Suppression de la ligne native

Retirer l'affichage de `rate_limit_per_minute` partout : `Descriptions.Item key="rate_limit"` (`Integrations.tsx:2518-2534`), le champ « Rate limit for marketing emails » du formulaire (`:2311`) déplacé dans le volet Avancé, l'étiquette `emails per minute / hour / day`. Le champ reste dans l'API (compat. upstream). Valeur par défaut posée par l'assistant : Gmail 1/min (comportement actuel), SMTP 60/min, afin qu'un profil sans réglage ne soit pas freiné à 6/min sans que personne le sache. Attention : ce garde est un frein réel (`rateLimiter.Wait`, `worker.go:466`). Un profil à fort volume (cas ASD) doit voir cette cadence relevée à sa création, sinon le plafond affiché ne sera pas atteignable. Test à écrire : pour chaque profil créé par l'assistant, `cadence × durée de fenêtre ≥ plafond effectif`, sinon avertissement dans la carte.

### 4.7 Chauffe proposée à la création

Proposer, pour un profil neuf, le calendrier de chauffe déjà supporté (`veridian_warmup_schedule`, par défaut croissant) avec bouton `Chauffe automatique` activé pour les domaines jamais utilisés. Ne pas l'imposer pour un profil importé : demander `Ce domaine a-t-il déjà envoyé ?`.

---

## 5. Séparation transactionnel et commercial

### 5.1 Comment le backend route aujourd'hui

- **Commercial** (broadcasts et automations) : tout passe par la file `email_queue` (`EmailQueueSourceBroadcast|Automation`), le worker choisit le profil dans le pool `veridian_marketing_email_provider_ids` (repli : `marketing_email_provider_id`), applique toutes les portes Veridian, envoie avec `SendEmail(..., isMarketing=true)` (`worker.go:530`).
- **Transactionnel** (API `transactional.send`, relais SMTP Bridge) : `transactional_service.go:627` appelle `workspace.GetEmailProviderWithIntegrationID(false)`, c'est-à-dire `settings.transactional_email_provider_id`, et envoie **directement**, sans file, sans pool, sans portes Veridian (ni plafond, ni fenêtre, ni fusible, ni quota), avec `isMarketing=false`. C'est bien le comportement voulu pour un mail transactionnel. Mais :

Défauts de séparation :
1. aucune validation d'exclusivité (profil à la fois dans le pool et transactionnel) ;
2. les statistiques d'usage ignorent les profils hors pool (donc le transactionnel) ;
3. le fusible de réputation compte par **domaine d'envoi**, tous types de messages confondus ;
4. le tableau de bord `EmailMetricsChart` filtre « Broadcasts / Transactional » mais n'a pas la dimension « profil » ni « réputation » ;
5. un profil transactionnel unique est supposé : pas de bascule transactionnelle, et pas de quota (pas de protection contre une boucle d'erreur côté client qui enverrait 50 000 mails transactionnels) ;
6. le modèle `Template.category` (marketing, transactional, welcome, opt_in, unsubscribe, blocklist) n'est relié à aucun profil.

### 5.2 Séparation proposée

**Règles de fond (backend)**
- Un profil a un `usage` exclusif : `commercial` ou `transactional`. Stocké par la liste existante (pool commercial) et `transactional_email_provider_id`, mais **validé** : `Workspace.Validate` refuse un profil dans les deux. La carte de l'UI n'expose que l'usage, jamais les deux listes.
- Le worker n'ajoute jamais un profil transactionnel à un candidat de bascule ; garde en profondeur : `veridianBuildFailoverCandidates` ignore tout profil dont l'ID égale `transactional_email_provider_id`.
- **Aucune porte de plafond, fenêtre ou fusible commercial ne s'applique au transactionnel.** Un mail transactionnel ne doit jamais être gelé par la prospection : un mot de passe oublié part toujours. En revanche : un **garde-fou de volume transactionnel** (plafond horaire très haut, alerte, jamais blocage silencieux) pour détecter une boucle.
- La réputation du profil transactionnel est **mesurée** (rejets, plaintes) mais **n'est pas un fusible bloquant** : elle déclenche une alerte lisible. Le fusible commercial ne compte plus que les messages commerciaux : `broadcast_id IS NOT NULL OR automation_id IS NOT NULL`, ou, mieux, par `veridian_profile_id` (colonne déjà présente dans `message_history`, `database/init.go:218`) plutôt que par domaine. Cas où deux profils partagent un domaine : comptés ensemble pour l'alerte, pas pour le gel.
- Recommandé : refuser par défaut qu'un profil transactionnel et un profil commercial partagent le **même domaine d'envoi**. Avertissement fort dans l'assistant, blocage possible plus tard. (La séparation de domaine est la vraie protection de réputation ; c'est une recommandation de produit, pas une contrainte technique.)

**Sidebar**

```
Tableau de bord
COMMERCIAL
  Contacts
  Listes
  Modèles            (onglet Commercial)
  Campagnes
  Séquences
TRANSACTIONNEL
  Notifications      (page actuelle Transactional)
  Modèles            (même page Modèles, onglet Transactionnel)
  Relais SMTP        (ex section Réglages)
ENVOI
  Profils d'envoi
  Journal d'envoi    (onglets Messages, Événements entrants)
RÉGLAGES
  Équipe, Général, Champs personnalisés, Webhooks, API et agents, Plan, Zone de danger
```

Les sections Commercial et Transactionnel sont de vrais groupes (titres de groupe antd `type: 'group'`), pas des entrées déguisées. Le tableau de bord ouvre sur le commercial, avec un sélecteur `Commercial | Transactionnel` (le filtre existant `all | broadcasts | transactional` devient `Commercial | Transactionnel`, « Tous » supprimé car il additionne des choses qui n'ont pas les mêmes règles).

**Métriques séparées** : le tableau de bord commercial = envois, réponses, rejets, plaintes, désinscriptions, séquences, stock restant, plafond effectif. Le tableau de bord transactionnel = volume par heure, taux de livraison, rejets, latence d'envoi, taux d'erreur de l'API, jamais de plafond, jamais de « réponses ». Même source (`message_history`), filtre par profil et par type (`transactional_notification_id IS NOT NULL`).

### 5.3 Cas ASD

Un workspace client ASD, loué :
- **Profil `asd-transactionnel`** (SMTP ou fournisseur API, domaine d'envoi dédié, par exemple `notifications.asd…` : le nom réel est à fournir par le client). Usage `Transactionnel`. Pas de chauffe, pas de pool, pas de fenêtre. Alimenté par l'API `transactional.send` et/ou le relais SMTP.
- **Profils `asd-commercial-1..N`** (Gmail ou SMTP, domaine distinct). Usage `Commercial`. Chauffe, plafonds, fenêtre, fusible, pool.
- ASD voit deux entrées de sidebar distinctes, deux tableaux de bord distincts, deux familles de modèles. Le client ne voit jamais les réglages d'une famille dans l'autre.
- Une panne ou un gel du commercial ne touche pas le transactionnel ; une boucle côté application cliente déclenche l'alerte transactionnelle et pas le fusible commercial.
- Provisionnement par CLI : `notifuse profiles create --usage transactional …`, `--usage commercial …` avec secret sur stdin, test d'envoi vers le propriétaire du workspace.

---

## 6. Décision sur l'upstream (fork assumé)

Mesuré : le fork est 106 commits derrière `upstream/main` ; sur `console/src`, l'upstream a modifié 440 fichiers depuis la base commune, le fork 140, dont 66 en commun. `Integrations.tsx` : +656/-136 côté fork contre +593/-61 côté upstream. Un merge de la console est donc un chantier de conflits, pas une synchronisation.

**Recommandation : assumer la console comme à nous.** Concrètement :
- on arrête de fusionner `console/` depuis l'upstream (on peut piquer à la main un correctif de sécurité ou une dépendance) ;
- on continue de suivre l'upstream **côté Go** (sécurité, correctifs, migrations), là où la discipline des fichiers `veridian_*.go` limite les conflits ;
- les nouvelles pages vivent dans des fichiers `veridian_*.tsx` (déjà la convention) pour que le diff avec la base reste lisible ;
- les routes et services Go du blog et des intégrations LLM/Supabase/Firecrawl restent inertes : on ne les touche pas, c'est ce qui rend le suivi du Go supportable. Ce n'est pas du code mort à nettoyer maintenant ;
- une ligne dans `CLAUDE.md` du fork (une règle, pas un récit) et le renvoi vers ce document.

Décision à ma charge : aucune dépense, aucun risque. Alternative écartée : continuer à fusionner la console (coût estimé : plusieurs jours de conflits à chaque passage pour une valeur nulle, puisque Robert veut précisément s'éloigner de cette UI).

À mesurer avant de masquer Supabase, LLM, Firecrawl et le gestionnaire de fichiers : lister par lecture seule (CLI `notifuse`, workspaces jetables ou clients, jamais `robertbrunon`) combien de workspaces ont ces intégrations. Non fait ici.

---

## 7. Plan de livraison

Principe commun : chaque lot est livrable seul en prod, passe par la CI du fork, est suivi jusqu'à « Deploy prod » et « E2E prod » verts, et se termine par une mesure sur l'image servie. Tests : règle 1-pour-1 du Constitution CI (chaque nouvelle fonction exportée d'un fichier critique a son `TestXxx` colocalisé). Tier de risque selon `docs/claude/48`.

### Lot 1. Nettoyage de la coque (console seule, tier 🟡)

Contenu :
- Retrait du blog dans l'UI (2.2) et de la page `debug-segment`, route `/analytics` redirigée vers l'index.
- Suppression de l'affichage de la limite de débit native (4.6), le champ passe dans un volet Avancé.
- Masquage des intégrations Supabase, LLM, Firecrawl et du gestionnaire de fichiers dans la sidebar (sous réserve de la mesure de la section 6).
- Complétion du français sur les trois fichiers de l'écran Intégrations (236 messages) et correction des chaînes en dur (jours en français, titres en anglais). Nouveau test de complétude pour les fichiers `veridian_*.tsx`.
- Retrait de `de, es, it, ja, pt-BR, ca` du sélecteur de langue.
- Sidebar en groupes (Commercial, Transactionnel, Envoi, Réglages) : seulement la mise en page, les pages restent celles d'aujourd'hui.

Fichiers touchés : `layouts/WorkspaceLayout.tsx`, `router.tsx`, `components/settings/SettingsSidebar.tsx`, `pages/WorkspaceSettingsPage.tsx`, `components/settings/Integrations.tsx` (suppression, pas d'ajout), `components/settings/veridian_cold_outreach_settings.tsx` (jours), `components/analytics/AnalyticsDashboard.tsx`, `LanguageSwitcher.tsx`, `contexts/LocaleContext.tsx`, `i18n/locales/fr.po` et `.js`, suppression de `components/blog*`, `pages/BlogPage.tsx`, `pages/DebugSegmentPage.tsx`, `components/settings/BlogSettings.tsx`, `services/api/blog.ts`. Tests : mettre à jour `__tests__/pages.smoke.test.tsx`, ajouter le test de complétude fr, tests de la sidebar (absence du blog).
Risque de conflit upstream : **élevé** sur `WorkspaceLayout`, `router`, `Integrations`, `SettingsSidebar` (fichiers modifiés des deux côtés). Assumé par la décision de la section 6.
Preuve de fin : capture Playwright headless sur le bastion de la sidebar et de l'ancienne page Intégrations, sans blog ni ligne de débit natif, en français ; `grep` du bundle servi.

### Lot 2. Vérité d'un profil : API (backend, tier 🔴 pour la pause, 🟡 pour le reste)

Contenu :
- `EffectivePlan` unique, partagée avec les portes du worker ; endpoint `emailProfiles.overview` (4.4) ; lecture de la réputation par profil (sur `veridian_profile_id`, pas par domaine) avec `by_provider_class` optionnel.
- Champs `veridian_return_inbox_integration_id` et `paused`, validations (IMAP du même workspace, exclusivité usage commercial/transactionnel), porte de pause dans la sélection du pool.
- Persistance du dernier poll et de la dernière erreur IMAP.
- Remplacement du calcul Python du CLI par l'appel à l'endpoint ; commandes CLI `profiles list|create|pause|resume|link-inbox|set-usage` (secret sur stdin).
- Extension de `emailProfiles.usage` aux profils hors pool.

Fichiers touchés : `internal/domain/email_provider.go`, `workspace.go` (Validate), nouveau `internal/domain/veridian_effective_plan.go`, `internal/service/queue/veridian_daily_cap.go`, `veridian_pool_failover.go`, `veridian_daily_quota.go`, `internal/service/veridian_email_profile_usage_service.go`, nouveau `veridian_email_profile_overview_service.go` et handler, `internal/service/queue/veridian_imap_poller.go`, `internal/app/app.go`, `~/.claude/skills/notifuse-cli/bin/notifuse_common.py`, `openapi`. Tests : test de contrat « porte du worker et endpoint donnent le même plafond », table de cas du facteur limitant, pause sans perte des entrées en file, exclusivité, E2E on-premise sur staging (vrai worker, sink SMTP) pour la pause et la bascule.
Risque de conflit upstream : **faible** (fichiers `veridian_*`) ; `workspace.go` et `app.go` : modifications ponctuelles déjà habituelles.
Coordination : le gel par fournisseur destinataire (autre agent) s'insère dans `reputation` ; figer le contrat `by_provider_class` avec lui avant d'écrire.

### Lot 3. Page « Profils d'envoi » (console, tier 🟡)

Contenu : la page de la section 4 (cartes, règles du jour, réputation, boîte liée, actions), l'assistant en trois choix, création atomique SMTP + IMAP pour Gmail (endpoint `emailProfiles.create` fourni par le lot 2 ou ajouté ici), carte Gmail OAuth grisée tant que le flux n'existe pas, suppression de la section Intégrations pour les profils d'envoi et de la carte générique `Type: imap`. Cold outreach au niveau workspace réduit à la « Politique des nouveaux profils » ; ses cartes par infra migrent dans la carte du profil (volet « Règles avancées »).

Fichiers touchés : nouveaux `pages/SendingProfilesPage.tsx`, `components/sending_profiles/veridian_*.tsx` (carte, assistant, tiroir IMAP), `services/api/veridian_email_profiles.ts`, `router.tsx`, `WorkspaceLayout.tsx`, `Integrations.tsx` (retrait), `veridian_cold_outreach_settings.tsx` (fort allègement : suppression du doublon lecture seule / édition, 2 715 lignes à environ la moitié), `veridian_sending_profiles_ui.tsx` (remplacé), `i18n/locales/fr.po`. Tests Vitest par composant, test d'accessibilité de l'assistant, parcours Playwright : ajout d'un profil Gmail fictif (jetable), test, pause, reprise, suppression.
Risque de conflit upstream : **moyen**, car surtout du neuf ; le retrait dans `Integrations.tsx` est le seul point chaud.
Preuve : parcours Playwright headless sur un workspace jetable (`notifuse-admin`), captures avant/après, URL et deux liens.

### Lot 4. Séparation transactionnel et commercial (backend + console, tier 🔴)

Contenu : exclusivité validée côté serveur, fusible commercial recompté par profil et par type de message, garde-fou de volume transactionnel (alerte, pas de blocage), sections Commercial et Transactionnel réelles dans la sidebar, tableau de bord à sélecteur `Commercial | Transactionnel`, page Modèles à onglets de catégorie, Relais SMTP déplacé, parcours ASD sur workspace jeton (transactionnel via API + mass mailing via campagne, deux profils, deux domaines).

Fichiers touchés : `internal/domain/workspace.go`, `internal/service/queue/veridian_reputation_gate.go` et `message_history_postgre.go` (comptage), `internal/service/queue/veridian_pool_failover.go`, `internal/service/transactional_service.go` (compteur et garde-fou), `AnalyticsDashboard.tsx`, `EmailMetricsChart.tsx`, `TemplatesPage.tsx`, `WorkspaceLayout.tsx`, `SMTPBridgeSettings.tsx`. Tests : un profil dans le pool puis déclaré transactionnel est refusé ; un lot de rejets sur le transactionnel ne gèle pas le commercial (et inversement) ; E2E staging complet.
Risque de conflit upstream : **faible côté Go** (`veridian_*`), **élevé côté console** (mêmes fichiers que le lot 1, déjà assumés).
Nécessite que le comportement du gel par fournisseur destinataire soit livré (sinon recompter deux fois la même porte).

### Lot 5. Tableau de bord prospection et suivi (console + lecture, tier 🟡)

Contenu : la page de la reprise 6bis sur les données existantes (`acquisition status`, `messages.replyStats`, `messages.list`), envois par jour et par heure et par profil, réponses par séquence et par segment, rejets/désinscriptions/plaintes, avancement J0/J+4/J+10, stock restant et plafonds effectifs (issus de `emailProfiles.overview`), phrase explicite « ouvertures et clics non suivis, texte brut, sans pixel ni lien » à la place des zéros. Retrait du pixel d'ouverture et du bloc « engagement par classe » s'ils ne mesurent rien en texte brut (à décider avec Robert : la réponse est oui si le tracking reste coupé).

Fichiers touchés : `AnalyticsDashboard.tsx`, `EmailMetricsChart.tsx`, `veridian_engagement_by_class.tsx`, nouveaux services de lecture par séquence et par segment (`veridian_reply_stats_service.go`). Tests Vitest et Go.
Risque de conflit upstream : moyen (analytics modifiés des deux côtés).

### Hors lots : « à terme », boîte de réception depuis la console

Ce qui existe : connexion IMAP chiffrée (TLS complet), polling périodique multi-boîtes, lecture des enveloppes et corps bruts, détection de réponse par Message-ID, typage humain/auto/challenge, traitement des rejets, idempotence par UID (`veridian_imap_uid_seen`).
Ce qu'il manque : (1) **aucun stockage des messages** : tout est consommé puis oublié, il faut une table `veridian_inbox_message` (corps texte, en-têtes, thread, lu/non lu, profil lié) ; (2) lecture à la demande (fetch complet, pièces jointes, dossiers) ; (3) un fil de discussion rattaché au contact (la timeline reçoit déjà l'événement de réponse) ; (4) **la réponse** : envoi via le SMTP du profil lié, en-têtes `In-Reply-To` et `References` corrects, enregistrement dans les « Envoyés », mise en sortie de séquence déjà faite ; (5) droits (qui lit quelle boîte) et RGPD (durée de conservation) ; (6) l'interface (liste, fil, composeur). Risques : volume d'une boîte réelle, deuxième lecture parallèle d'une boîte déjà pollée (verrous UID), réputation (une réponse humaine envoyée depuis le profil de prospection doit rester hors de ses plafonds). À cadrer après le lot 3, quand la liaison profil-IMAP existe.

---

## 8. Ce que ce document ne règle pas

- Le contrat exact du gel par fournisseur destinataire (autre agent) : le contrat `by_provider_class` est laissé optionnel et à figer avec lui.
- Le nombre réel de workspaces utilisant Supabase, LLM, Firecrawl ou le gestionnaire de fichiers : non mesuré (lecture seule sur workspaces autres que `robertbrunon` à faire avant de masquer).
- L'équilibrage du pool (branche `agent/pool-balance-20261006`) n'a pas été relu ligne à ligne ici.
- Aucune capture d'écran n'a été prise : l'inventaire de l'UI vient du code, pas d'un rendu. Le lot 1 commence par une capture de référence « avant ».
