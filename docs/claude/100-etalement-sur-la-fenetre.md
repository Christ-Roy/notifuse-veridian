# 100. Étalement des envois commerciaux sur la fenêtre (27/11/2026)

Demande de Robert (27/11) : « on devrait prendre la fenêtre de temps pour espacer le plus possible les mails ».

## Le constat mesuré

`greenbeamcraft`, 27/11 : 87 envois entre 07:00 et 10:41 UTC, écart médian 70 s, 42 envois à moins d'une minute du
précédent, pour une fenêtre de 8 h à 19 h (Paris). Les jours précédents, même forme : départ à 07:00 pile, médian de 2
à 6 min, des rafales à 10 s. Cause : les débits sont posés PAR CLASSE de fournisseur destinataire (ovh 0,15/min, orange
0,05/min…) ; vingt-cinq classes qui avancent chacune à son rythme font plus d'un envoi par minute pour le profil, et le
plafond du jour (chauffe 300) se vide le matin. Aucune porte ne répartissait ce qui reste à envoyer sur ce qui reste de
fenêtre.

## La règle (`internal/service/queue/veridian_pacing.go`)

Après un envoi commercial (séquence, campagne) d'un profil, le suivant attend

    intervalle = minutes d'ouverture restantes / (envois utiles restants + 1)

dispersé de ±30 % (même amplitude et même cascade que le jitter du débit de classe, fiche 25 ; `*0` = sans dispersion),
borné à 90 min. Les deux termes viennent du plan du profil (fiche 60 : somme des classes bornée par la chauffe, les
plafonds et le stock dû), c'est-à-dire du calcul que montre l'écran Profils d'envoi : aucun second calcul de capacité.
Le plan est relu toutes les 2 min par espace (`service.NewVeridianPacingPlanner` sur
`veridianComputeProfilesOverview`, le calcul de l'aperçu sans sa porte d'authentification) ; entre deux lectures, les
envois du processus sont décomptés. L'intervalle se recalcule à chaque examen : du stock qui arrive, ou du retard pris,
raccourcit l'attente.

- **Place dans la cascade** : porte `pacing`, la dernière, dans l'évaluation de chaque candidat du pool
  (`veridianEvaluateCandidate`). Elle est LUE avec les portes sans effet de bord ; le jeton de débit de classe n'est
  pris que si le créneau est libre, et le créneau n'est réservé que si la classe passe aussi. Un profil qui vient
  d'envoyer passe la main au suivant du pool ; tous occupés = report avec la raison `pacing` (fiche 62) et le délai du
  créneau le plus proche.
- **Ne gouverne pas** : le transactionnel et les mails écrits depuis la boîte, un profil sans fenêtre, une capacité
  illimitée ou non mesurée, un plan illisible (la porte laisse passer, les plafonds tiennent toujours).
- **Coupe-circuit** : `VERIDIAN_PACING=off` dans le job.
- **État en mémoire, par processus** : après un redémarrage, le premier envoi d'un profil part tout de suite ; deux
  allocations simultanées (canari) peuvent doubler le rythme quelques minutes.
- **Console** : raison « Étalé sur la fenêtre d'envoi », porte « Étalement » dans la trace (File d'envoi).

## Tests

`veridian_pacing_test.go` : formule (dont le cas greenbeamcraft : 157 restants pour 435 min = 2 min 45 s), créneau et
dispersion, ce que la porte ne gouverne pas, coupe-circuit, et dans la cascade réelle le pool qui passe la main puis
l'attente `pacing`. Vu rouge porte débranchée. `veridian_pacing_planner_test.go` : extraction du plan.
