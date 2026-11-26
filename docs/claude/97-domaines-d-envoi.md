# 97. Domaines d'envoi authentifiés : DKIM, SPF, DMARC au nom du client (VER-166, 26/11/2026)

**Pourquoi** : jusqu'ici Notifuse ne signait rien ; le DKIM venait du relais (d=agence-veridian.fr, d=messagerie-nord-776.fr) ou du
fournisseur du client. Un client qui écrit « From: …@son-domaine.fr » par nos relais partait donc sans signature alignée : DMARC
échoue, Gmail et Outlook trient. **Portée** : tout espace, tout profil SMTP (nos relais, le SMTP du client, Gmail par API).

## Ce qui existe

| Brique | Où |
|---|---|
| Table système `veridian_sending_domain` (V83) : espace, domaine, sélecteur, clé publique, clé privée chiffrée par `SECRET_KEY`, statut, relevé | `internal/migrations/veridian/v83_domaines_envoi.go` |
| Clé RSA-2048, sélecteur daté `vdAAAAMMxxxxxx`, enregistrements à publier, vérification DNS, signature | `internal/veridian/domauth/` |
| Routes `GET sendingDomains.list`, `POST sendingDomains.create`, `.verify`, `.delete` (sous `/api/veridian/`), droit `workspace` lecture / écriture | `domauth/api.go` |
| CLI `domains`, `domains:add --domain`, `domains:verify --id`, `domains:delete --id --yes` (clé scopée de l'espace) | `notifuse_mail.py` (skill), embarquée |
| Console : Réglages > « Domaines d'envoi » (enregistrements à copier, bouton Vérifier) | `console/src/components/settings/veridian_sending_domains.tsx` |
| Signature à l'envoi : une ligne dans `SMTPService.SendEmail`, juste après la composition MIME, avant SMTP brut ou API Gmail | `internal/service/veridian_outgoing_dkim.go` |
| Notre SPF : `_spf-envoi.veridian.site` = `v=spf1 ip4:37.187.199.185 ip4:51.210.7.44 ~all` | `iac/dns/veridian-site.yaml`, `VERIDIAN_SPF_INCLUDE` |

## Règles

- **Vérifié = le TXT `<sélecteur>._domainkey.<domaine>` porte exactement notre clé publique.** Seule condition pour signer : signer
  sans clé publiée produirait `dkim=fail`. SPF et DMARC sont relevés et rendus (`complete` = les trois), pas exigés pour signer.
- Signature : From **exactement** sur un domaine vérifié **de cet espace** (un sous-domaine n'hérite pas), d=ce domaine,
  relaxed/relaxed, rsa-sha256, From/To/Subject sur-signés. Le relais peut ajouter sa signature : deux signatures, DMARC prend l'alignée.
- Échec de signature (base, `SECRET_KEY`) : journalisé, le mail part **tel quel** (comme avant). Jamais d'envoi bloqué pour ça.
- Cache du signataire : une minute par (espace, domaine), vidé à la création, à la vérification et à la suppression.
- **Revérification à l'usage** (suite du 26/11, aucune tâche planifiée) : un envoi qui trouve un domaine vérifié dont le dernier
  contrôle a plus de 24 h lance un contrôle DNS en tâche de fond (`Signer.maybeRecheck`, `Rechecker`) : un seul à la fois par domaine,
  au plus un par 24 h et par processus, l'envoi n'attend pas. Clé absente (NXDOMAIN, pas de TXT) ou différente : le domaine passe
  « en attente », `lost_at` (date du constat) et `lost_reason` (`missing`, `mismatch`) sont posés (V84), le cache est vidé : plus de
  signature. Panne DNS passagère (SERVFAIL, délai) : statut gardé, contrôle daté. Le bouton Vérifier (ou `domains:verify`) ramène le
  domaine et efface le constat. La console montre « Signature coupée le … » et le motif ; la CLI `domains` aussi.
- Console : conseils et motifs traduits côté console (lingui), jamais le texte français du serveur (`note`, `detail` restent pour l'API).
- OpenAPI : `openapi/paths/veridian-domains.yaml`, schémas `veridian-domains.yaml`, bundle `openapi.json`. `TestOpenAPI_DocumenteChaqueRoute`
  échoue si une route de `domauth.Routes` (ou sa méthode) manque. Dette relevée : 35 `$ref` cassés dans `veridian-policy.yaml` et
  `veridian-reservoir.yaml` (bundle seulement avec `--force`).
- SPF vérifié en vrai : chaque IP de notre include passe `check_host` du domaine (include, ip4 recopiée, redirect : la forme importe peu).
  Un relais neuf = son IP dans `_spf-envoi`, pour tous les clients d'un coup.
- Règle 9 : aucune réponse ne porte la clé privée, ni claire ni chiffrée (`Domain` ne la contient pas, test `TestAPI_ParcoursEtRedaction`).
- Profils par API d'un fournisseur (SES, Postmark, Mailgun…) : non signés par nous, ils ont leur propre authentification de domaine.

## Tests (vus échouer par mutation le 26/11)

`domauth` (résolveur simulé, PostgreSQL `testpg`) et `service` (`TestSMTPService_SendEmail_VeridianSignsVerifiedDomain` : message reçu
par un serveur SMTP qui garde les octets, signature vérifiée par go-msgauth). Onze mutations, toutes rouges : ligne de branchement
commentée, sur-signature retirée, statut ignoré à la signature, garde d'espace retirée, clé stockée en clair, DKIM accepté sans
comparer la clé, SPF non évalué, panne DNS qui dé-vérifie, cache sans espace, droit d'écriture abaissé, clé publique exposée.
Console : deux mutations rouges (verdict non affiché, mauvais id vérifié).

## Épreuve en prod (26/11/2026, commit `5b717261`)

Outil rejouable : `scripts/e2e/domaine-envoi-prod.py --workspace <espace jetable du Hub> --real-send` (puis `--nettoyer`).
Domaine d'épreuve `essai-envoi.veridian.site` (zone Cloudflare à nous, aucun achat), espace jetable `e2esso17ywnhkbjafb7g`.

- Route `sendingDomains.verify` : DKIM `pass` (clé conforme), SPF `pass` (nos deux relais passent `check_host`), DMARC `pass` (p=none).
- Envoi réel par le relais agence, From `essai-718263@essai-envoi.veridian.site`, vers une boîte de profil neuve de
  `messagerie-nord-776.fr` du même espace. Parti le 26/11 à 18:53:02 UTC, `Message-ID: <6b5d215d-91da-4a4c-a363-aa127948065f@essai-envoi.veridian.site>`.
- Reçu par notre MX (`Received: from [37.187.199.185] by mx.veridian.site`) :
  `Authentication-Results: mx.veridian.site; spf=pass smtp.helo=smtp.agence-veridian.fr smtp.mailfrom=essai-718263@essai-envoi.veridian.site; dkim=pass header.d=essai-envoi.veridian.site header.i=@essai-envoi.veridian.site; dmarc=pass header.from=essai-envoi.veridian.site`
- Le relais agence, qui vérifie en route, dit pareil (`dkim=pass (2048-bit key) header.d=essai-envoi.veridian.site header.s=vd2026117025e6`)
  et n'ajoute pas sa propre signature pour un domaine étranger : une seule signature, la nôtre.
- Capture console (fr, ordinateur et téléphone, aucun débordement) : `tests/e2e-veridian/domaines-capture.mjs` par `lourd --navigateur`.
- Nettoyé : domaine, profils d'épreuve et TXT de `essai-envoi.veridian.site` retirés. `_spf-envoi.veridian.site` reste (service).

Le relais agence accepte un MAIL FROM d'un domaine étranger avec ses accès SASL (sondé sans envoi, RSET après RCPT) : c'est ce qui
permet l'alignement SPF ; s'il venait à durcir (anti-usurpation), DKIM seul garde DMARC `pass`.

## Épreuve en prod de la revérification à l'usage (26/11/2026, commit `64d7645d`)

`domaine-envoi-prod.py --perte` puis `--retour` (espace jetable `e2esso3gp89e0404app4`). Le passage de 24 h est simulé en reculant le
dernier contrôle de la seule ligne d'épreuve (`scripts/e2e/vieillir-domaine-envoi.sh`, refuse tout autre domaine ou espace).

| Heure (UTC) | Fait | État lu par `sendingDomains.list` |
|---|---|---|
| 20:12:11 | avant | `verified`, dkim `pass`, `lost_at` vide |
| 20:12:14 | TXT DKIM retiré (Cloudflare), attente des caches | |
| 20:14:44 | un envoi (le déclencheur, signé avec l'état connu) | |
| 20:15:07 | | `pending`, dkim `missing`, `lost_at` 20:14:45, `lost_reason` `missing` |
| 20:15:08 | envoi suivant | reçu : `dkim=none`, aucune `DKIM-Signature`, `dmarc=pass` par SPF aligné |
| 20:17:50 | TXT republié, bouton Vérifier (route) | `verified`, `lost_at` effacé |

Captures EN et FR de l'état « Signature coupée » prises par `domaines-capture.mjs` (`LANGUE=en|fr`), sans un mot de l'autre langue.
Vu en route : juste après la publication d'un domaine, un résolveur peut garder une réponse négative (SPF lu « absent » 30 min) ; seul
DKIM décide du statut, et une clé déjà publiée n'est pas concernée.
