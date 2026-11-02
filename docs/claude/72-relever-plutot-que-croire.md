# 72. Relever plutôt que croire (commandes de vérité)

Déplacé depuis CLAUDE.md le 02/11/2026 pour repasser sous 12 000 caractères (fiche 71 ajoute une entrée d'index). Contenu inchangé.

## Commandes

```bash
# état réel du cluster Nomad + du job notifuse (prod/staging)
ssh bastion nomad-v state
ssh bastion 'nomad job status notifuse'

# la dette de tests non couverts (baseline transitoire, cible 0)
wc -l tests-pending.txt

# le mapping test colocalisé est-il vert sur le HEAD actuel ?
BASE_REF=origin/veridian scripts/ci/check-test-mapping.sh

# tous les fichiers custom Veridian (convention flat veridian_*.go)
git ls-files | grep -E 'veridian_|veridian\.go' | wc -l

# la vraie valeur des secrets HMAC/infra (jamais dans les .env locaux)
ssh bastion 'nomad var get nomad/jobs/notifuse'
```

