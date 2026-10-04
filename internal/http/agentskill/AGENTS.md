# Notifuse — agent email (générique, tous agents)

Ce fichier s'adresse à n'importe quel agent IA (Claude Code, un autre agent de
codage, un script) qui vient d'être branché sur un workspace Notifuse.

## La clé API : où elle est, ce qu'on en fait

- La clé est dans la variable d'environnement `NOTIFUSE_API_KEY`, chargée
  automatiquement depuis `~/.config/notifuse/env` (fichier `chmod 600`,
  créé par le script d'installation).
- **Ne jamais afficher cette clé** (pas de `cat ~/.config/notifuse/env`, pas
  de `echo $NOTIFUSE_API_KEY`, pas de la coller dans une réponse, un commit,
  un log, un message à l'utilisateur) tant que ce n'est pas strictement
  nécessaire. Ce n'est quasiment jamais nécessaire : le CLI la lit tout seul.
- Ne jamais committer ce fichier ni sa valeur dans un dépôt.
- Si la clé semble invalide (401) ou que l'accès à une ressource est refusé
  (403), ne pas tenter de la régénérer soi-même : dire à l'utilisateur de
  recréer une clé depuis la page "API & agents" de la console Notifuse.

## Utiliser le CLI, pas l'API brute

Le CLI `notifuse` (installé par le script, dans le PATH) sait déjà
s'authentifier avec `NOTIFUSE_API_KEY` et cible le bon workspace. Préférer
systématiquement :

```bash
notifuse lists:list
notifuse contacts:import --file contacts.csv --lists <list_id1>,<list_id2>
notifuse config
```

à un appel HTTP manuel (`curl` + construction de headers). Le CLI gère les
erreurs réseau, le format de sortie (JSON scriptable) et évite de jamais
avoir besoin d'écrire la clé en clair dans une commande.

## Pour la doctrine d'envoi en masse (plafonds, séquences, chauffe, diagnostic)

Voir `SKILL.md` dans ce même dossier : c'est le savoir-faire Veridian sur
l'emailing en masse, écrit pour être appliqué directement.
