# [NOTIFUSE] Spec mail-account-settings prod-smoke fail "Something went wrong!"

> **OBSOLÈTE 2026-06-13** — pivot envoi stand-alone 31/05, archi mail-gateway via Hub abandonnée.

> **Sévérité** : 🟢 P3 — debug E2E harness, pas de régression prod
> **Owner** : agent Notifuse
> **Créé** : 2026-05-25 par team-lead vague 6

## Symptôme

`console/e2e-prod-smoke/mail-account-settings.spec.ts` fail systématiquement en CI avec :
- "Something went wrong!" (React error boundary)
- `expect(getByRole('heading', { name: /Mail account/i })).toBeVisible()` timeout

## Ce qui marche

- **Vitest unit** `veridian_mail_account_settings.test.tsx` : 6/6 verts
- **Migration V48** + endpoints HMAC : Go tests verts
- **Lib `pkg/hub_mail_gateway`** : 17 tests verts
- **App.go wiring** `SetMailProviderService` : build OK, service injecté

## Ce qui ne marche pas

Le harness E2E prod-smoke spécifiquement. Cause hypothétique : `WorkspaceSettingsPage` déclenche des fetches additionnels (settings persisted, blog config, etc.) qui ne sont pas stubbés → un fetch crash → error boundary.

## Tentatives de fix infructueuses (vague 6)

1. ✅ Stub URL pattern `https://prod-smoke-stub.invalid/**` → corrigé en `**/api/**` (aligné mobile-responsive.spec.ts qui marche)
2. ✅ Stub `user.me` shape `{user}` → corrigé en `{user, workspaces[{id, name, settings}]}`
3. ✅ Stub `workspaces.members` shape `{members: [{user_id, role}]}` → corrigé en `{members: [{user_id, email, permissions: {...}}]}`

Malgré ces 3 fixes, error boundary persiste. Skipped via `test.describe.skip` en attendant debug dédié.

## Debug à faire (vague 7+)

1. Lancer la spec en `--headed --debug` pour voir l'erreur exacte dans React DevTools
2. Cliquer le bouton "Show Error" du error boundary pour récupérer le stack trace JS
3. Identifier la route fetch non-stubbée qui crash → ajouter au stub
4. Réactiver la spec en retirant `.skip`

## Workaround actuel

- Spec **skipped** depuis commit team-lead vague 6 (cette session)
- Vitest unit garantit le bon mount du composant
- Validation manuelle prod possible via curl + navigation prod URL

## DoD

- [ ] Cause racine identifiée
- [ ] Stub étendu pour couvrir la route manquante
- [ ] Spec réactivée (retirer `.skip`)
- [ ] CI verte sur la spec
- [ ] Ticket archivé dans done/
