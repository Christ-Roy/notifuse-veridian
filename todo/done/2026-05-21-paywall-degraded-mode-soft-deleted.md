# [NOTIFUSE] Paywall mode dégradé sur soft-delete — UX

> **Type** : UX + middleware backend
> **Sévérité** : 🟡 P1 UX (impact rétention client + image de marque)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-21
> **Lien** : remplace partiellement le ticket existant
> `2026-05-19-paywall-obfuscation-degrade.md` (à archiver après ce
> ticket livré).

---

## Problème actuel

Quand le Hub soft-delete un tenant (cycle de vie GDPR / trial expiré /
churn / suspension manuelle), Notifuse renvoie **un mur béton** :

- Paywall middleware → `402 Payment Required: tenant deleted`
- Console Notifuse → JWT fail → page d'erreur cryptique
- Aucun chemin de récupération visible pour le user

Conséquence business :
- Client perdu sans pouvoir exporter ses données → mauvaise PR
- Pas de funnel de réactivation → 0 conversion churn → re-signup
- Support submergé de tickets "j'ai perdu accès à mes contacts"

## Vision cible : mode dégradé en lecture seule

Quand un tenant est soft-deleted (`deleted_at IS NOT NULL` mais pas
encore purgé — fenêtre 30j cf. CONTRAT-HUB §5.7), la console doit :

- Afficher un **bandeau rouge persistant** : "Votre compte a été
  suspendu le X. Vos données seront définitivement supprimées le Y.
  [Réactiver] [Exporter]"
- Permettre **lecture** : liste contacts, historique messages, templates
- **Bloquer mutations** : pas d'envoi, pas de nouveau contact, pas de
  template créé/modifié
- Permettre **export** : CSV contacts, ZIP templates → laisse partir
  les données au lieu de les retenir en otage
- Lien **Réactiver** → redirige vers `app.veridian.site` (le Hub) pour
  upgrade/payment

---

## Périmètre

### Backend Notifuse

- ✅ Le middleware paywall actuel (cf. `veridian_paywall.go`) bloque
  déjà en 402 sur tenant deleted → **changer la sémantique** pour
  retourner `403 + degraded_mode: true` au lieu de 402.
- ✅ Lister les endpoints de **lecture safe** qui doivent passer en
  mode dégradé :
  - `GET /api/contacts.list` ✅
  - `GET /api/messages.list` ✅
  - `GET /api/templates.list` ✅
  - `GET /api/broadcasts.list` ✅
  - `POST /api/contacts.export` ✅ (export safe)
  - `POST /api/templates.export` ✅
- ✅ Lister les endpoints de **mutation** qui restent bloqués :
  - `POST /api/contacts.upsert` ❌
  - `POST /api/contacts.import` ❌
  - `POST /api/broadcasts.create` ❌
  - `POST /api/transactional.send` ❌
  - tout ce qui modifie l'état

### Frontend console Notifuse

- Lecture du nouveau header response `X-Tenant-Degraded: 2026-06-21T...`
  (date de purge) au login → afficher le bandeau persistant
- Désactiver visuellement les boutons "Send", "Create contact",
  "New template" (grisés + tooltip "Compte suspendu")
- Ajouter dans la sidebar un lien **"Réactiver mon compte"** → `https://app.veridian.site/reactivate?tenant_id=X`
- Ajouter un onglet **"Exporter mes données"** dans Settings :
  CSV contacts, ZIP templates, JSON message history

---

## Livrables

### 1. Backend — extension du paywall middleware

Fichier : `internal/http/middleware/veridian_paywall.go`

**Nouveau comportement** sur tenant deleted :

```go
if entry.plan.DeletedAt != nil {
    // Mode dégradé : on laisse passer les GET et les exports,
    // on bloque les mutations.
    if isDegradedReadAllowed(r.URL.Path, r.Method) {
        w.Header().Set("X-Tenant-Degraded", "true")
        if entry.plan.PurgeEligibleAt != nil {
            w.Header().Set("X-Tenant-Purge-At", entry.plan.PurgeEligibleAt.Format(time.RFC3339))
        }
        next.ServeHTTP(w, r)
        return
    }
    // Mutation → blocage propre avec code degraded_mode
    w.WriteHeader(http.StatusForbidden)
    json.NewEncoder(w).Encode(map[string]interface{}{
        "error":         "Account suspended — read-only mode active",
        "error_code":    "tenant_degraded_readonly",
        "purge_at":      entry.plan.PurgeEligibleAt,
        "reactivate_url": "https://app.veridian.site/reactivate?tenant_id=" + workspaceID,
    })
    return
}
```

Nouvelle map `degradedReadAllowedPaths` (ou function) listant les paths
+ méthodes safe en mode dégradé.

### 2. Backend — extension du `IsBlocked` domain

Fichier : `internal/domain/veridian.go`

```go
// IsBlockedForMutation retourne true si le tenant doit voir ses
// mutations bloquées (suspended, deleted). Différent de IsBlocked
// qui bloque tout : on peut être en mode dégradé (lecture OK,
// mutation KO).
func (p *VeridianPlan) IsBlockedForMutation() (blocked bool, reason string) {
    if p.DeletedAt != nil {
        return true, "tenant deleted — read-only mode"
    }
    if p.Status == PlanStatusSuspended {
        return true, p.SuspendedReason
    }
    return false, ""
}

// IsDegraded retourne true si le tenant est en mode dégradé (deleted
// mais pas encore purgé). La console doit afficher le bandeau.
func (p *VeridianPlan) IsDegraded() bool {
    return p.DeletedAt != nil && p.PurgeEligibleAt != nil && time.Now().Before(*p.PurgeEligibleAt)
}
```

### 3. Backend — endpoints export de masse (si absents upstream)

À vérifier en reco terrain :
- Est-ce que `POST /api/contacts.export` existe déjà upstream Notifuse ?
- Si oui : juste s'assurer qu'il passe en mode dégradé
- Si non : ticket séparé (ne pas l'inclure ici)

### 4. Frontend console — bandeau + désactivation mutations

**Hors scope ticket Notifuse direct** — la console (React) vit dans
`console/src/`. Étapes :
- Hook React `useDegradedMode()` qui scrute le header `X-Tenant-Degraded`
- Composant `<DegradedBanner>` injecté en haut de chaque page
- Wrapper `<MutationButton>` qui désactive si dégradé
- Lien dans `Settings → Export my data`

Découper en sous-ticket UI séparé si trop gros.

### 5. Tests

- Middleware : tenant deleted + GET = 200 avec headers
- Middleware : tenant deleted + POST mutation = 403 avec error_code
- Middleware : tenant active + tout = passe (régression check)
- Middleware : tenant suspended (pas deleted) = comportement actuel
  (402, pas de mode dégradé pour suspended) ou degraded aussi ? — **à
  décider** : pour l'instant je dirais same comme deleted (donne au
  user un chemin de réactivation rapide).
- Domain : `IsBlockedForMutation` vs `IsDegraded` couvrent les 4 états
  (active/suspended/deleted/purged)

---

## Risques identifiés

1. **List des paths "safe en lecture" doit être maintenue** : risque
   d'oubli si on ajoute un endpoint GET sans le whitelister. **Mitigation** :
   par défaut **autoriser GET** + maintenir une **deny-list explicite**
   plutôt qu'une allow-list (les rares GET qui modifient l'état doivent
   être listés).

2. **Cache paywall 60s** : un tenant qui vient d'être deleted ne sera
   en mode dégradé qu'après expiration du cache (≤60s). **Pas grave**
   pour le UX (60s d'incohérence en transition n'est pas critique).
   Le Hub peut invalider via `/api/veridian/admin/cache/invalidate`
   pour propager immédiat.

3. **Front-end pas à jour** : si la console n'est pas mise à jour pour
   lire les nouveaux headers, l'expérience reste cryptique (les API
   répondent 403 sur mutations, mais l'UI ne montre rien de cohérent).
   **Mitigation** : commit backend + frontend dans le même PR ou même
   feature flag.

4. **Suspended vs Deleted** : décision à figer. Actuellement suspended
   = pas pareil (admin/billing reason, peut être réactivé par paiement).
   Pour le 1er jet je propose : **suspended = même comportement que
   deleted (mode dégradé)**. Plus simple, plus user-friendly.

---

## Status

- [ ] Décision figée suspended vs deleted (degraded_mode pour les 2 ?)
- [ ] Reco terrain endpoints export Notifuse (existent ? si non, ticket
      séparé)
- [ ] Backend middleware étendu
- [ ] Backend domain `IsBlockedForMutation` + `IsDegraded`
- [ ] Tests backend
- [ ] Decision UI / ticket frontend séparé créé
- [ ] Curl live post-deploy : soft-delete un canary test → GET passe,
      POST renvoie 403 avec error_code
