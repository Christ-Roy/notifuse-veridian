# [NOTIFUSE] 🔴 P0 — Classification destinataire par MX réel (Option A : table de patterns MX)

> **Sévérité** : 🔴 P0 réputation email
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-14. Décision archi tranchée par Robert : **Option A** = résolution
> MX universelle + table de patterns MX→classe versionnée (PAS de détection magique
> dynamique, PAS de hardcode de domaines destinataires — on mappe le HOST MX, stable).
> Remplace/complète le ticket `2026-06-14-classification-mx-nebuleuses-google-microsoft.md`.

## Décision d'architecture (Option A, actée)

La VÉRITÉ universelle du provider destinataire = le **MX record** du domaine (le DNS
te dit où le mail atterrit, peu importe le nom de domaine). On NE classe PLUS par
suffixe de domaine destinataire (faux pour ~70% des leads B2B). On :
1. résout le MX du domaine (moteur universel, zéro maintenance),
2. mappe le **hostname MX** à une classe via une **table de patterns versionnée**
   (~40 lignes, données d'infra STABLES — Google = `*.google.com` depuis 15 ans),
3. fallback `corporate_selfhost` (débit prudent) pour l'inconnu,
4. cache `domaine→classe` (ne pas résoudre 7,8M de MX à chaque envoi).

Ni "tout dynamique" (illusoire → centaines de micro-classes ingérables), ni hardcode
fragile. La table se NOURRIT de la vraie data (script mxmap), pas d'une liste théorique.

## ⚠️ ULTIME VÉRIFICATION — patterns MX RÉELS dérivés de ta data (email_verification, MX déjà résolus)

Ces patterns viennent de la VRAIE base (odh-scrape-db.email_verification, 48k vérifiés
+ confirmés sur prospection prod 286k). Matching **case-INSENSITIVE** (vu en data :
`ASPMX.L.GOOGLE.com`, `ASPMX.L.GOOGLE.COM`, `aspmx.l.google.com` = idem) et sur
**suffixe du hostname MX** (substring/suffixe, pas égalité stricte).

| Pattern(s) MX host (lowercase, suffixe) | Classe | Volume observé |
|---|---|---|
| `.google.com`, `gmail-smtp-in.l.google.com`, `aspmx.l.google.com`, `smtp.google.com` | `google` | ~12k (25%) |
| `.protection.outlook.com`, `.olc.protection.outlook.com`, `.outlook.com` | `microsoft` | ~6k (13-17%) |
| `.ovh.net` (mx0/mx1/mx3/mx4.mail.ovh.net) | `ovh` | ~8k (17%) — NOUVELLE classe |
| `.ionos.fr`, `.ionos.com`, `.ionos.de`, `.ionos.es`, `.ionos.co.uk`, `.kundenserver.de` | `ionos` | ~3k (6%) — NOUVELLE |
| `.yahoodns.net` | `yahoo_aol` | ~1k |
| `smtp-in.orange.fr`, `.sfr.fr`, `.free.fr`, `smtpz4.laposte.net`, + FAI FR | `freemail_fr` | (existant) |
| `.infomaniak.ch`, `mta-gw.infomaniak.ch` | `infomaniak` ou `other_hoster` | ~1,2k |
| `.hostinger.com`, `.hostinger.fr` | `other_hoster` | ~1k |
| `.mail.gandi.net` | `other_hoster` | ~0,7k |
| `.mail.icloud.com` | `apple_icloud` | ~0,2k — NOUVELLE |
| `.zoho.eu`, `.zoho.com` | `other_hoster` | ~0,2k |
| `.online.net`, `.titan.email`, `.webador.com`, `.webmo.fr` | `other_hoster` | petits |
| `.vadesecure.com`, `.mailinblack.com`, `.proofpoint`, `.mimecast`, `.hornetsecurity.com`, `.securemail.pro`, `.sophos`, `.retarus`, `.trendmicro`, `.mimecast` | **`security_gateway`** | ~1k — NOUVELLE, débit ULTRA-prudent / quasi-exclusion cold (anti-spam pro qui blackliste) |
| `.protonmail.ch`, `.proton.me` | `other_hoster` (ou `proton`) | ~0,2k |
| sinon (mail.\<domaine\>, smtp.\<domaine\>, inconnu) | `corporate_selfhost` | fallback, débit prudent |

➡️ **Classes finales recommandées** (étend les 5 actuelles) : `google`, `microsoft`,
`ovh`, `ionos`, `yahoo_aol`, `freemail_fr`, `apple_icloud`, `security_gateway`,
`other_hoster`, `corporate_selfhost`. (Garder `corporate` comme alias rétrocompat si besoin.)

⚠️ Le set exact des classes est à confirmer avec Robert AVANT de coder (impacte l'UI
+ les configs existantes). Reco : commencer par ajouter `ovh`, `microsoft` (déjà là),
`security_gateway`, `apple_icloud` — les plus impactants — + `other_hoster` fourre-tout
des hébergeurs propres. Ne pas multiplier inutilement.

## TRAVAUX DÉJÀ ACCOMPLIS (paths — point de départ, NE PAS refaire)

- **Classification actuelle (à étendre, PAS réécrire)** :
  `internal/domain/veridian_provider_class.go` — `ClassifyProviderClass(email)` (ligne ~156),
  table statique `veridianProviderDomainTable`, classes `ProviderClassGoogle/...` (l.25-29),
  set `veridianProviderClassSet`, `VeridianDomainsForClass` (utilisé par le breakdown + daily cap).
- **Consommateurs de la classe (à mettre à jour si on ajoute des classes)** :
  - `internal/service/queue/veridian_provider_throttle.go` (gate rates/min)
  - `internal/service/queue/veridian_provider_rate_limiter.go` (rate limiter keyé `integrationID|classe`)
  - `internal/service/queue/veridian_daily_cap.go` (cap journalier classe + destinataire — utilise `VeridianDomainsForClass`)
  - `internal/domain/veridian_provider_breakdown.go` (R1 breakdown — réutilise `ClassifyProviderClass`)
  - UI : `console/src/components/settings/veridian_cold_outreach_settings.tsx` (cartes par classe) + `console/src/services/api/workspace.ts` (VERIDIAN_PROVIDER_CLASSES, VERIDIAN_DEFAULT_OPEN_PIXEL)
- **Cartographie + script (la data source de la table)** :
  - Doc réf : `notifuse-veridian/docs/PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md` (à committer s'il ne l'est pas)
  - Script : `veridian-prospection/scripts/mx_provider_map.py` + `mx_provider_map.README.md`
  - Data : `veridian-prospection/scripts/mx_provider_map_data/` — `prospection_classified_2026-06-14.csv` (domaine→mx→classe), `mx_cache.json` (cache MX), `prospection_report_2026-06-14.txt` (distribution)
  - Pipeline MX existant à exploiter : `odh-scrape-db.email_verification` (mx_provider, mx_host, catch_all, smtp result — déjà résolu, ne pas relancer les lookups pour ces domaines)
- Mémoire : `reference_providers_destinataires_cartographie.md`

## Implémentation (Option A)

1. **Garder la table de suffixes** (freemail grand public gmail.com/orange.fr → classés
   SANS lookup, rapide + zéro I/O). Inchangée pour ces domaines.
2. **Ajouter la couche MX** dans `veridian_provider_class.go` : si le suffixe n'est PAS
   dans la table connue → résoudre le MX (resolver fiable **1.1.1.1/8.8.8.8 + fallback**,
   PAS 127.0.0.1 instable ; timeout court `+time=3 +tries=2`) → matcher la table de
   patterns MX (case-insensitive, suffixe) → classe. Fallback `corporate_selfhost`.
3. **Cache** : table DB `provider_class_cache(domain TEXT PK, classe TEXT, mx_host TEXT,
   resolved_at TIMESTAMP)` OU réutiliser le cache à l'import (cf point 5). TTL long
   (MX change rarement). NE PAS résoudre à chaque envoi.
4. **Best-effort STRICT** : lookup échec/timeout → `corporate_selfhost`, JAMAIS bloquer
   l'envoi (même contrat que tout le throttle Veridian).
5. **Pré-remplissage à l'import** (le plus performant pour 7,8M) : à l'import de contacts,
   dériver la classe (depuis email_verification.mx_provider si dispo, sinon lookup caché)
   et la stocker dans `custom_string_5` (le tag déjà consommé par `ClassifyProviderClass`
   en override). Ainsi zéro lookup à l'envoi. ➜ Ticket Prospection séparé pour câbler
   l'enrichissement amont.
6. **Tests** : MX google→google, MX outlook.protection→microsoft, MX ovh.net→ovh,
   MX vadesecure→security_gateway, casse mixte, échec lookup→corporate_selfhost (fallback),
   cache hit, suffixe connu→pas de lookup (non-régression). Tests colocalisés stricts.

## 🔴 Décision liée — PAS de lien unsubscribe sur le cold (façon Lemlist, acté Robert 2026-06-14)

Le cold outreach Veridian assume **AUCUN lien/​header unsubscribe** sur les envois cold
(comme Lemlist/Instantly) : le cold B2B se présente comme du 1-to-1 personnel, un footer
"se désabonner" + header `List-Unsubscribe` = signal "mailing de masse" → tue la
délivrabilité et le côté personnel. On l'assume en connaissance de cause (risque légal
B2B accepté par Robert, cf sa note `feedback-robert-tranche-tu-executes.md`).

ÉTAT DU CODE (vérifié) — c'est FAISABLE sans toucher au core, c'est conditionnel :
- Le footer `{{unsubscribe_url}}` n'existe QUE dans les templates de démo (`demo_service.go`),
  jamais imposé. ✓
- Le header RFC-8058 `List-Unsubscribe` n'est ajouté QUE si `EmailOptions.ListUnsubscribeURL`
  est non-vide (ses_service.go:973, mailgun_service.go:765, sparkpost_service.go:863). ✓
- Le broadcast remplit `ListUnsubscribeURL` SEULEMENT si `data["oneclick_unsubscribe_url"]`
  est non-vide (`queue_message_sender.go:420-421`, `message_sender.go:360-361`).

À FAIRE (petit, dans le tunnel cold) : en contexte tunnel (tag custom_string_5 / config
cold présente — même détection que le pixel par classe), NE PAS générer
`oneclick_unsubscribe_url` → donc pas de header List-Unsubscribe, pas de footer. Gater
ça côté Veridian (fichier veridian_*, ne pas patcher le compilateur upstream), même
pattern que `VeridianResolveOpenPixel`. Tests : contexte tunnel → ListUnsubscribeURL vide ;
hors tunnel → comportement upstream inchangé (non-régression : les vrais broadcasts
marketing GARDENT leur unsubscribe). Peut être un sous-lot de ce ticket ou un ticket
dédié `2026-06-14-cold-no-unsubscribe.md` — à l'agent de juger.

## DoD
- [ ] Set de classes confirmé avec Robert
- [ ] Cold sans unsubscribe : `oneclick_unsubscribe_url` non généré en contexte tunnel (header + footer absents), broadcasts marketing non affectés
- [ ] Couche MX + table de patterns câblée dans `veridian_provider_class.go`
- [ ] Cache (DB ou import) — pas de lookup par envoi
- [ ] Resolver fiable + best-effort + timeout
- [ ] Classes consommatrices mises à jour (throttle, daily_cap, breakdown, UI, défauts pixel)
- [ ] Tests stricts (les cas ci-dessus)
- [ ] Mesure avant/après : combien de "corporate" reclassés
- [ ] Ticket Prospection ouvert (enrichissement custom_string_5 à l'import)
- [ ] Doc cartographie committée + mise à jour avec la table finale
