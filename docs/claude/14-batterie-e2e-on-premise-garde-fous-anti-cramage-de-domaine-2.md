# Batterie E2E : garde-fous anti-cramage de domaine

`scripts/e2e/cold-garde-fous.py` (25/11/2026) prouve sur le **vrai worker de staging** que les dix portes tiennent, **par l'API seule** :
ni base, ni journal de conteneur, ni ssh, ni jeton d'exec (VER-250). Elle remplace les deux `.sh` du 17/06, qui lisaient `psql` et
`docker logs`. À rejouer avant chaque campagne, ~8 minutes :

```
python3 scripts/e2e/cold-garde-fous.py            # tout
python3 scripts/e2e/cold-garde-fous.py --only G2,G7 [--keep]
```

Sortie : `0` toutes tiennent · `1` une porte en défaut · `2` non abouti (témoin pas parti, staging redéployé pendant la mesure, version trop
ancienne) : jamais un vert par défaut.

**Aucun mail réel** : profil vers `127.0.0.1:1025` (puits sans relais de l'allocation de staging), relu avant tout envoi ; autre hôte = arrêt.
`1026` est un port fermé, pour G1. Espace jetable `gfcheck…`, effacé à la fin, avec reprise si le staging redémarre.

| Porte | Preuve |
|---|---|
| G1 disjoncteur | profil vers un port fermé : des essais refusés, le reste retenu (`queue.explain` = `circuit_open`), rien de parti |
| G2 classe exclue, G7 pré-filtre | `messages.list` : échec et sa raison, avec témoins google et corporate partis |
| G3 étalement | écarts de `sent_at` (google à 1 par minute), corporate parti avant le deuxième google |
| G4, G5, G6 | `cold-simulate` : prédicat exact, ouvert, fermé, rouvert |
| G8 contenu varié | `decisions.list?trace=true` : `trace.content.hash` distincts sur la même classe |
| G9 pixel par classe | `trace.content.open_pixel` : non vers google, oui vers corporate |
| G10 rotation | `trace.candidates[selected].from` : les deux expéditeurs |

`trace.content` (envois seulement) : `hash` = empreinte du sujet et du corps émis, adresses de liens et pixel mis à part (leurs jetons changent à
chaque message) ; `open_pixel` = le HTML émis porte le pixel. Le contenu lui-même n'est jamais rendu.

## Vue échouer (25/11)

Les dix portes, une par une : exclusion retirée, adresse jetable remplacée, graines de plafond absentes, jour ouvert, port ouvert au lieu de fermé, débit
non bridé, texte sans variante, pixel forcé, un seul expéditeur. Staging redéployé au milieu : « non abouti », sortie 2.

## Pièges

- **Le débit par défaut (0,3 par minute et par classe) décide de tout** si la campagne ne pose pas `veridian_provider_class_rates` : le témoin
  n'est pas parti à temps, donc « non abouti ».
- **`seed_sent` doit porter `provider_class`** : depuis le correctif du 28/09 le plafond de classe compte la classe PERSISTÉE. L'ancienne batterie ne
  la posait pas et aurait été rouge à tort depuis.
- **G1 : ne pas restaurer le profil sain** pendant la mesure, les envois reportés partiraient au puits.
- **Domaine « valide » de test** : il doit se résoudre (`example.com`), sinon le pré-filtre le coupe aussi.
- `messages.list` ne rend ni l'expéditeur ni l'empreinte : ils se lisent dans le journal des décisions.

`cold-simulate` (staging seul, HMAC) : modes `seed_sent`, `daily_cap_decision`, `class_cap_decision` (`sender_domain` = compteur par infra),
`per_sender_cap_decision`, `sending_window_decision`, `warmup_cap_decision`, `inbound_reply` ; `internal/http/veridian_cold_simulate_handler.go`.
