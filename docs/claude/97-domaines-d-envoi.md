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
- Re-vérifier : clé retirée du DNS = retour « en attente », plus de signature ; DNS en panne (`temperror`) = statut gardé.
  **Aucune revérification planifiée** (loi 2) : c'est le bouton ou `domains:verify`.
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
