# 60. Capacité d'un profil d'envoi : somme par fournisseur destinataire (lot 6, 10/10/2026)

Demande de Robert (10/10) : le « plafond plat » d'un profil n'est pas intelligent. La capacité d'envoi d'un profil est **la somme de ce que chaque classe de fournisseur destinataire peut absorber**, bornée par la chauffe et par le stock réellement en file pour chaque classe. La console la montre en barre empilée et le CLI agent embarqué donne le même calcul.

## Règle de calcul (une seule source : `internal/service/queue/veridian_plan_capacity.go`)

Pour une classe, sur la journée de compte du profil :

    restant(classe) = min( plafond de classe - envoyés (après facteur du fusible),
                           débit effectif x minutes d'ouverture restantes de la fenêtre )

0 si la classe est exclue, arrêtée par le fusible, ou si la fenêtre est close. Plafonds et débits sont lus par les MÊMES résolutions que les portes du worker (`veridianResolveCapLimits`, `veridianEffectiveClassRate`) : aucun second calcul. Aucune limite de volume pour une classe = restant `nil` (illimité), jamais 0 par défaut.

- **Somme des classes**, puis bornée par les portes de profil : chauffe, plafond par expéditeur, plafond plat (désormais facultatif). La plus basse gagne et est nommée (`limiting`).
- **Configurée** = envoyés + restant ; **utile** = envoyés + min(restant, stock dû aujourd'hui pour la classe). Le stock (file + séquences, `GetDueStock`) est partagé par le pool : `VeridianAllocateStock` le répartit entre les profils au prorata de ce que chacun peut absorber (plus grand reste, somme des parts = stock). Stock illisible = **inconnu** (`stock_known=false`), jamais « vide ».
- **Fenêtre** : `VeridianSendingWindow.OpenMinutes` intègre le même test que `IsWithinWindow` ; fenêtre close aujourd'hui, la « journée type » (`full_day_configured`) est donnée à côté.
- **Classes fines du 10/10** (infomaniak, gandi, hostinger, o2switch, lws, scaleway, website_builder) : elles entrent dans la somme comme n'importe quelle classe (`VeridianAllProviderClasses`) et héritent débit, plafond et exclusion d'other_hoster tant qu'elles n'ont pas d'entrée propre (`VeridianRateForClass`, `VeridianCapForClass`). Conséquence mesurable : sur le profil de test de Robert, la somme passe de 407 à 960 (7 x 79 de plus). Ce n'est pas un bug, c'est la capacité réelle que le worker admet.
- Fusible proportionné : une plainte ÷4 divise les plafonds ET les débits, la capacité suit.
- Le journal des décisions de la fiche 62 n'est pas branché ici : la capacité est un calcul d'avance, pas une trace. `queue:explain` répond à « pourquoi CE mail n'est pas parti », la capacité à « combien peut partir ».

- **Fenêtre close pour tous** (dimanche, hors horaires) : aucun profil n'absorbe rien, donc le prorata n'avait aucun poids et le stock s'affichait à 0 alors que la file en portait 2 322 (trouvé par la mesure en prod le 11/10, dimanche). `VeridianAllocateStock` répartit alors le stock à parts égales : il reste visible, la capacité utile reste 0 (min(reste 0, stock)).

## Exposition

- API : `emailProfiles.overview` (`plan.capacity` + champs par classe `capacity_configured|useful`, `capacity_limiter`, `stock`; totaux `commercial_capacity_today`, `commercial_useful_capacity_today`).
- Console, page « Profils d'envoi » : `veridian_capacity_block.tsx`, barre empilée envoyé / à envoyer / inutilisable faute de contacts, ligne « Limité par », classe qui manque de contacts, détail par fournisseur dépliable. Traductions dans les 8 catalogues (fr et en vérifiées).
- CLI agent embarqué (`internal/http/agentcli`, `MANIFEST.sha256` régénéré par `sha256sum`, source de vérité `~/.claude/skills/notifuse-cli/bin`) : `profiles` / `emailProfiles.overview` affiche la capacité du jour, l'utile, la limite, la fenêtre et le détail par fournisseur.

## Tests (mapping 1-pour-1)

- `veridian_plan_capacity_test.go` : parité avec le worker réel (le worker admet EXACTEMENT le restant annoncé), débit x fenêtre, rétrécissement dans la journée, fenêtre close, fusible, plafond de classe, stock, portes de profil, `VeridianAllocateStock`, parité minute par minute de `OpenMinutes`. Les attendus tiennent compte des classes fines.
- `veridian_capacity_rules.test.ts`, `veridian_capacity_block.test.tsx`, tests du service et du dépôt de l'overview.

## Preuve en production

Voir le rapport de session du 11/10 (calcul pour les 3 profils de `robertbrunon` comparé au stock de la file, capture headless de la page en lecture seule).
