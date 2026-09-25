# UI console — section Settings « Cold outreach » (2026-06-11)

Expose la config tunnel dans la console React (admin sans curl). Composants
veridian purs (préfixe identique au backend) :
- `console/src/components/settings/veridian_cold_outreach_settings.tsx` : édition
  débits + pixel par classe (lecture seule non-owner), save via
  `POST /api/workspaces.update`.
- `console/src/components/broadcasts/veridian_broadcast_rates_info.tsx` : affichage
  lecture seule des rates posés sur `broadcast.metadata`.
- Branchés : `SettingsSidebar` (+section `cold-outreach`), `WorkspaceSettingsPage`
  (case), `UpsertBroadcastDrawer` (tab content). Types front
  `WorkspaceSettings.veridian_*` + constantes dans `console/src/services/api/workspace.ts`.
- ⚠️ **Piège SW cache** (cf. memory project_notifuse_console_sw_cache) : valider
  toute nouvelle UI staging avec `?cachebust=` sinon l'ancien bundle est servi.

