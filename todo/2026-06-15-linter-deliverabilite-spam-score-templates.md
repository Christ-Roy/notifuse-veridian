# [NOTIFUSE] 🟡 P1 — Linter de délivrabilité (spam score) sur les templates cold

> **Sévérité** : 🟡 P1 délivrabilité cold (garde-fou avant envoi)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-15 par Robert. "Avoir un score spam sur les templates pour dégrossir,
> sans usine à gaz (pas rspamd en démon permanent, pas Python embarqué)."

## Décision d'archi (tranchée avec Robert)
- ❌ **Pas de rspamd en service permanent** : ~100MB+ RAM en continu (dev server déjà à 85%),
  maintenance config Lua, surdimensionné pour scorer quelques templates.
- ❌ **Pas de SpamAssassin via lib Go (`spamc`)** : ces libs sont des clients vers un démon
  `spamd` externe → n'évitent pas d'installer/maintenir SpamAssassin quelque part.
- ❌ **Pas de Python embarqué dans Go** (cgo/libpython) : lourd, fragile, et SpamAssassin
  n'est pas une lib Python de toute façon (c'est du Perl). Détour sans gain.
- ✅ **Linter de délivrabilité Go NATIF, in-process** : un package `veridian_deliverability`
  (~200 lignes, zéro dépendance) qui réimplémente les RÈGLES qui comptent pour le cold.
  Instantané dans le preview (`templates.compile` / un endpoint dédié). Zéro infra, zéro
  démon, zéro maintenance. C'est ce que font Lemlist/Instantly (linter maison, pas SA).

## Contenu du linter (v1 — catégories par poids réel, calquées sur les règles SpamAssassin publiques)

### 🔴 Structurel (le plus déterminant)
- Ratio liens/texte (cold 1-to-1 = 0-1 lien max idéal).
- Lien de tracking sur domaine ≠ From (déjà géré via track.agences-veridian.fr, le linter vérifie).
- Longueur du corps (ni trop court "check this out", ni trop long).
- Si HTML : ratio image/texte, images sans `alt`, HTML-only sans partie texte
  (règles SA `HTML_IMAGE_ONLY`, `MIME_HTML_ONLY`). En **plain text pur → ces règles sautent**.

### 🟡 Contenu / mots déclencheurs
- Mots/phrases spammy pondérés ("free", "click here", "act now", "100% guaranteed", "$$$"...).
- MAJUSCULES excessives sujet/corps (SA `SUBJ_ALL_CAPS`).
- Ponctuation excessive `!!!` `???` `$$$` (SA `SUBJ_EXCESSIVE_QMARK`).
- Sujet : trop long, tout majuscule, emojis en rafale, fake "Re:".

### 🟢 Personnalisation / fraîcheur (spécifique cold)
- Variables Liquid non résolues qui fuient (`{{ first_name }}` visible = bug amateur).
- Spintax non résolu qui fuit (`{A|B}` dans le rendu final).
- Zéro personnalisation sur un cold = template générique = bulk.

### Rendu
Score 0-10 (façon SA, >5 = risque) + **liste des règles déclenchées avec leur poids**
(`LINKS_RATIO_HIGH: +1.5`, `ALL_CAPS_SUBJECT: +2.0`) → savoir QUOI corriger, pas juste un chiffre.
Analyse le RENDU FINAL (après Liquid + spintax), pas le template brut.

## 🎯 MODES DE LINTER (par classe de provider destinataire — important)
Le linter doit avoir des **profils** selon le provider ciblé, car les exigences diffèrent :
- **Mode "gros provider" (Google/Microsoft)** : STRICT. Les gros providers détestent en cold :
  - les **liens** (surtout au 1er contact) → pénaliser fort tout lien.
  - le **tracking** (pixel ouverture + redirect clic) → pénaliser, recommander OFF.
  - préfèrent **plain text pur** au premier contact → bonus plain text, malus HTML.
  - → un cold vers Google/MS devrait idéalement être : plain text, 0 lien, 0 tracking, perso.
- **Mode "petit provider" (freemail_fr, corporate, OVH, etc.)** : plus tolérant — liens/tracking
  acceptés, HTML léger ok. (cohérent avec le pixel par classe déjà livré : OFF gros / ON petits).
- Le mode se déduit de la classe de provider destinataire (réutilise `veridian_provider_class`)
  ou est forcé en preview ("simuler envoi vers Google").

## 🔬 RAFFINEMENT (évolution, pas v1)
- Installer **rspamd EN LOCAL** (dev/jetable, pas en prod permanent) pour faire des TESTS :
  comparer le score du linter maison vs rspamd sur de vrais mails, voir quelles règles ont un
  VRAI impact, et **calibrer les coefficients** du linter en conséquence. rspamd sert d'oracle
  de calibration ponctuel, pas de service runtime.
- Affiner les poids sur de vrais envois (les seuils v1 sont des estimations basées sur SA public).

## 🔥 RAPPEL WARMUP (lié, à ne pas oublier)
Le linter ne remplace PAS le **warm-up IP/domaine** : monter progressivement le volume sur une
IP/domaine neuf est indispensable pour le cold (surtout vers gros providers). Le relai
`agences-veridian.fr` (IP dev 37.187.199.185) doit être warmé. Cf. skill `postfix` (warm-up cold).
Prévoir : rampe progressive du volume (J1: 10/j, J2: 20/j... cohérent avec les caps/jour par
classe déjà livrés) + monitoring réputation. À cadrer dans un ticket warmup dédié si pas déjà fait.

## DoD v1
- [ ] Package `internal/.../veridian_deliverability.go` (+ test) : score + règles + poids.
- [ ] Modes provider (gros strict / petit tolérant), déduits de la classe destinataire.
- [ ] Exposé en preview (endpoint ou templates.compile) → score visible dans l'éditeur de template.
- [ ] Analyse le rendu final (Liquid+spintax résolus).
- [ ] Tests Go (chaque règle, chaque mode).
- [ ] Doc : ce que chaque règle pénalise + comment calibrer via rspamd local.
