#!/usr/bin/env python3
"""
notifuse — CLI unifié pour piloter 100% de l'app Notifuse Veridian par API.

Modèle EXACT du CLI `analytics` (~/.claude/skills/analytics-provision/bin/analytics) :
argparse + subparsers, load_env()/get_key() lisant ~/credentials/.all-creds.env,
call() central, out() uniforme, --env (DÉFAUT PROD), doctor en self-test.

Notifuse a TROIS mécaniques d'auth (le CLI les choisit tout seul selon la route) :

  1. HMAC Hub  — routes /api/tenants/* et /api/veridian/admin/* (provision, wipe,
     cold-simulate, grant-unlimited, gc, stats…). Canonical `${ts}.${rawBody}`,
     headers x-veridian-app/x-veridian-timestamp(ms)/X-Veridian-Hub-Signature.
     Secret : NOTIFUSE_HUB_API_SECRET.
  2. JWT owner — routes OWNER-ONLY (workspaces.createIntegration/updateIntegration,
     workspaces.update settings cold). Obtenu programmatiquement :
     provision idempotente HMAC → auto_login_url → auth_token embarqué dans le HTML.
  3. JWT api_key — toutes les autres routes applicatives (broadcasts, templates,
     contacts, automations, transactional, webhooks…). NOTIFUSE_API_KEY si fournie,
     sinon dérivée à la volée d'une provision (api_key du workspace).

ZÉRO mail réel : `verify`/`dry-run` tapent les PRÉDICATS EXACTS des gates cold via
`/api/veridian/admin/cold-simulate` (staging-only) — aucun SMTP ouvert. L'envoi réel
(transactional.send / broadcasts.sendToIndividual) exige le flag --real-send.

CREDENTIALS — jamais hardcodés, lus depuis ~/credentials/.all-creds.env :
  NOTIFUSE_HUB_API_SECRET   (HMAC Hub → provision/wipe/admin/cold-simulate + owner login)
  NOTIFUSE_API_KEY          (JWT api_key machine, optionnel)
Défaut = PROD. `--env staging` bascule sur staging.

USAGE — survol (détail : notifuse <cmd> --help, ou SKILL.md)
============================================================
  notifuse doctor [--env]                self-test : health 200 + HMAC round-trip + (staging) cold-simulate
  notifuse provision <tid> --email <e>    provisionne un tenant (HMAC). Renvoie ws+api_key+auto_login_url
  notifuse wipe --prefix P | --ids a,b    wipe tenant(s) de test (HMAC, staging)
  notifuse verify <ws> [--cap N ...]      DRY-RUN cold : les gates bloqueraient-ils ? ZÉRO mail (cold-simulate)
  notifuse dry-run <ws>                   alias de verify (batterie complète de prédicats)
  notifuse status <ws>                    état consolidé : plan, contacts, intégrations, config cold, breakdown
  notifuse breakdown <ws> [--list L]      contacts par classe de provider destinataire

  notifuse keys:list <ws>                 liste les api_keys (jamais le secret)
  notifuse keys:provision <ws>            émet une nouvelle api_key (workspaces.createAPIKey)

  notifuse integrations:list|get <ws>     intégrations (5 types) + config cold par infra
  notifuse integrations:plan|apply [--env]  IAC cold (délègue notifuse-iac.sh)
  notifuse integrations:cold <ws> --id I  pose rates/caps/exclusion/window/pixel/tracking/--bounce-freeze-threshold sur une infra (owner)
  notifuse integrations:create-smtp|create-imap <ws> --name N --host H --port P --user U --password-env VAR ...
  notifuse integrations:create-supabase <ws> --name N [--email-hook-key K] [--user-hook-key K] (owner)
  notifuse integrations:create-firecrawl <ws> --name N --api-key K (owner)
  notifuse integrations:create-llm <ws> --name N --provider anthropic|openai --api-key K [--model M] (owner)
  notifuse integrations:update|delete <ws> --id I [...]   (owner ; update préserve les secrets)

  notifuse settings:get <ws> [--key K]    dump des settings du workspace (owner)
  notifuse settings:set <ws> <key> <val>  patche UNE clé (merge+update intégral, owner)
  notifuse settings:update <ws> --file s.json   merge multi-clés sur l'existant (owner)

  notifuse templates:push <ws> --file X.mjml --name N [--id ID] [--subject S]   create/update idempotent, code-mode, IA-first
  notifuse broadcasts:list|get|create|update|delete|schedule|cancel|pause|resume <ws> ...
  notifuse broadcasts:send-test <ws> --id B --email a@b.c [--real-send]
  notifuse templates:list|get|create|update|delete|compile <ws> ...
  notifuse contacts:list|count|get|upsert|import|delete <ws> ...
  notifuse lists:list|get|create|update|delete|stats|subscribe <ws> ...
  notifuse segments:list|get|create|delete|contacts|rebuild <ws> ...
  notifuse events:list|get|upsert <ws> ...   (customEvents : champs top-level, external_id requis)
  notifuse members:list|invite|remove|permissions <ws> ...   (Team Settings, owner)
  notifuse magic-link <ws> [--email e]    génère un lien de connexion (clé éphémère, révoquée aussitôt)
  notifuse profiles:overview [ws] [--json]  VÉRITÉ des profils d'envoi : plafond du jour + porte limitante, reste,
                                          fusible par couple, fenêtre, classes exclues, pause, IMAP lié (lecture, clé scopée OK)
  notifuse profiles:link-imap <ws> --id P (--imap I | --none)   lie un profil à sa boîte IMAP de retour (owner)
  notifuse profiles:set-usage <ws> --id P --usage commercial|transactional|unassigned
                                                              usage EXCLUSIF d'un profil : rotation commerciale, profil transactionnel réservé, ou hors service (serveur : exclusivité, rotation jamais vidée, profil vérifié)
  notifuse profiles:create <ws> --type smtp|gmail-app-password --name N --from-email E (--secret-stdin | --secret-file F) ...
                                                              crée un profil d'envoi (+ boîte IMAP liée), atomique, owner. Le secret se lit sur stdin ou dans un
                                                              fichier, JAMAIS en argument, jamais affiché ni loggé. Gmail : https://myaccount.google.com/apppasswords
  notifuse prospection:stats <ws> [--start AAAA-MM-JJ] [--end AAAA-MM-JJ]   agrégats du tableau de bord de prospection (réponses par séquence et liste,
                                                              avancement J0/J+4/J+10, sorties par raison, stock par liste ; lecture, clé scopée OK)
  notifuse queue:explain <ws> [--group-by node,reason,profile,class,automation] [--automation X] [--node N] [--reason R] [--profile P] [--class C] [--status S] [--entry ID] [--json]
                                                              pourquoi les mails sont en file : tableau groupé (count, jamais examinés, plus ancienne, prochaine tentative) + orphelins ;
                                                              --entry ID = détail de l'entrée et sa dernière décision gate par gate (lecture, clé scopée OK)
  notifuse logs:decisions <ws> [--email E] [--automation X] [--node N] [--entry ID] [--reason R] [--outcome sent|deferred|failed|discarded|exited|recomputed]
                               [--since 2h|7d|RFC3339] [--limit N] [--trace] [--all] [--json]    journal des décisions du worker (--all : jusqu'à 5 pages ; lecture)
  notifuse queue:recompute <ws> --automation X [--node N] [--reason R] [--profile P] [--entry ID ...] --limit N [--yes]
                                                              remet des entrées au recalcul (next_retry_at et raison effacés, rien supprimé) ; SANS --yes : affiche le corps et ne fait rien
  notifuse profiles:pause|resume <ws> --id P                    met en pause / relance un profil commercial (workspace:write, effet immédiat au worker ; refusé sur un transactionnel)
  notifuse analytics:query <ws> --query @q.json    (analytics:schemas pour les schémas)
  notifuse messages:list <ws> [--param limit=50]   historique des envois
  notifuse automations:list|get|create|update|delete|activate|pause|enroll <ws> ...
  notifuse transactional:list|get|create|update|delete <ws> ...
  notifuse transactional:send <ws> --id N --email e [--data '<json>'] --real-send
  notifuse webhooks:list|get|create|update|delete|test|deliveries|toggle <ws> ...

  notifuse admin:tenants [--prefix P] [--orphans]     listing admin (HMAC)
  notifuse admin:grant-unlimited <tid>                tenant → enterprise illimité (HMAC)
  notifuse admin:gc-orphans [--dry-run] [--cap N]     GC bases workspace orphelines (HMAC, staging)
  notifuse admin:stats                                stats cron cleanup (HMAC, staging)
  notifuse admin:cold-simulate <ws> --mode M [...]    frappe un prédicat de gate cold (HMAC, staging)

  notifuse api <GET|POST> <resource.verb> --workspace <ws> [--param k=v ...] [--data '<json>'|@file] [--auth A]
        Échappatoire : tape N'IMPORTE quelle des 84 routes. --auth = auto|hmac|owner|apikey|none.
        ex: notifuse api GET contacts.count --workspace ws_x
        ex: notifuse api POST templates.create --data @tpl.json

Toute commande sort un JSON lisible (jq-friendly) + exit code != 0 si l'API
renvoie une erreur (401/403/404/400/409/5xx) — donc scriptable/testable.
"""
import argparse
import csv
import getpass
import html
import hashlib
import hmac as hmaclib
import json
import uuid
import os
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request
import urllib.error
from datetime import datetime, timezone
from pathlib import Path
try:
    from zoneinfo import ZoneInfo
except Exception:  # pragma: no cover — très vieux Python sans stdlib zoneinfo
    ZoneInfo = None

ENV_PATH = Path.home() / "credentials" / ".all-creds.env"
REPO_ROOT = Path.home() / "Bureau" / "veridian-platform" / "notifuse-veridian"
IAC_SCRIPT = REPO_ROOT / "scripts" / "iac" / "notifuse-iac.sh"

BASES = {
    "prod": "https://notifuse.app.veridian.site",
    "staging": "https://notifuse.staging.veridian.site",
}
HMAC_SECRET_VAR = "NOTIFUSE_HUB_API_SECRET"
APIKEY_VAR = "NOTIFUSE_API_KEY"

# Routes qui exigent l'auth HMAC Hub (préfixes). Tout le reste = JWT.
HMAC_PREFIXES = ("/api/tenants/", "/api/veridian/admin/", "/api/sso/", "/api/users/by-email")
# Routes /api/<resource>.<verb> OWNER-ONLY (cf docs/AGENT-API.md §1).
OWNER_ONLY = {"workspaces.createIntegration", "workspaces.updateIntegration",
              "workspaces.deleteIntegration", "workspaces.update",
              "workspaces.createAPIKey", "workspaces.create", "workspaces.delete"}
# Routes app servies en GET (query params, pas de body).
GET_VERBS = {
    "automations.get", "automations.list", "automations.nodeExecutions",
    "broadcasts.get", "broadcasts.getTestResults", "broadcasts.list",
    "contacts.count", "contacts.getByEmail", "contacts.getByExternalID", "contacts.list",
    "templates.get", "templates.list",
    "webhookSubscriptions.deliveries", "webhookSubscriptions.eventTypes",
    "webhookSubscriptions.get", "webhookSubscriptions.list",
    "veridian/contacts.providerBreakdown",
    "veridian/messages.engagementByClass", "veridian/messages.replyStats",
    "veridian/templates.deliverabilityScore",
    "workspaces.get", "workspaces.list", "workspaces.members",
    "lists.get", "lists.list", "lists.stats",
    "segments.get", "segments.list", "segments.contacts", "transactional.get", "transactional.list",
    "customEvents.get", "customEvents.list", "messages.list", "messages.broadcastStats",
    "settings.get", "user.me", "timeline.list", "inboundWebhookEvents.list",
    "blogPosts.list", "blogPosts.get", "blogCategories.list", "blogThemes.list",
    # Ajouts couverture totale 2026-10-03 (mission "pilotage pixel") :
    "automations.nodeExecutions", "contactLists.getByIDs",
    "contactLists.getContactsByList", "contactLists.getListsByContact",
    "contacts.getByExternalID", "blogThemes.getPublished", "blogThemes.get",
    "templateBlocks.list", "templateBlocks.get", "tasks.list", "tasks.get",
    "workspaces.list", "workspaces.acceptInvitation", "workspaces.verifyInvitationToken",
    "user.updateLanguage", "setup.status", "webhooks.status",
    "veridian/emailProfiles.usage", "veridian/emailProfiles.overview",
    "veridian/hub-discovery/me", "veridian/mode",
}

# ---------------------------------------------------------------------------
# COUVERTURE TOTALE (mission 2026-10-03 "pilotage pixel") — chaque route
# /api/* du fork a une commande CLI dédiée. Le principe anti-dérive : TOUT
# nom de commande ci-dessous DOIT exister réellement dans argparse (vérifié
# au parsing, voir build_parser) et la liste des routes couvertes DOIT être
# égale à l'ensemble extrait du routeur Go (voir scripts/coverage_test.sh du
# skill — "notifuse __routes" dumpe cet ensemble, diffé contre le routeur).
# Retirer une commande SANS retirer sa ligne ici fait échouer le check
# d'existence ; retirer sa ligne ici sans retirer la commande fait échouer
# le diff de couverture. Les deux sens sont gardés RED par construction.
# ---------------------------------------------------------------------------
_REGISTERED_COMMANDS = set()          # rempli par le shim de sub.add_parser
COMMAND_ROUTES = {}                    # nom de commande -> [routes /api/... couvertes]

# Commandes PRÉ-EXISTANTES (avant la mission) -> route(s) qu'elles couvrent
# réellement (audit du code, 2026-10-03 : grep des call_hmac/call_jwt/app_call
# littéraux — cf rapport). Une route peut être couverte par plusieurs commandes
# (ex workspaces.get par status/integrations:list/settings:get...) : on ne
# liste ici qu'UNE commande suffisante par route, les autres sont des bonus.
EXISTING_COMMAND_ROUTES = {
    "analytics:query": ["/api/analytics.query"],
    "analytics:schemas": ["/api/analytics.schemas"],
    "automations:list": ["/api/automations.list"],
    "automations:get": ["/api/automations.get"],
    "automations:create": ["/api/automations.create"],
    "automations:activate": ["/api/automations.activate"],
    "automations:pause": ["/api/automations.pause"],
    "automations:delete": ["/api/automations.delete"],
    "automations:update": ["/api/automations.update"],
    "automations:enroll": ["/api/automations.enroll"],
    "broadcasts:list": ["/api/broadcasts.list"],
    "broadcasts:get": ["/api/broadcasts.get"],
    "broadcasts:create": ["/api/broadcasts.create"],
    "broadcasts:cancel": ["/api/broadcasts.cancel"],
    "broadcasts:pause": ["/api/broadcasts.pause"],
    "broadcasts:resume": ["/api/broadcasts.resume"],
    "broadcasts:delete": ["/api/broadcasts.delete"],
    "broadcasts:schedule": ["/api/broadcasts.schedule"],
    "broadcasts:send-test": ["/api/broadcasts.sendToIndividual"],
    "templates:list": ["/api/templates.list"],
    "templates:get": ["/api/templates.get"],
    "templates:create": ["/api/templates.create"],
    "templates:delete": ["/api/templates.delete"],
    "templates:compile": ["/api/templates.compile"],
    "contacts:list": ["/api/contacts.list"],
    "contacts:count": ["/api/contacts.count"],
    "contacts:get": ["/api/contacts.getByEmail"],
    "contacts:upsert": ["/api/contacts.upsert"],
    "contacts:import": ["/api/contacts.import"],
    "contacts:delete": ["/api/contacts.delete"],
    "lists:subscribe": ["/api/lists.subscribe"],
    "lists:list": ["/api/lists.list"],
    "lists:get": ["/api/lists.get"],
    "lists:create": ["/api/lists.create"],
    "lists:update": ["/api/lists.update"],
    "lists:delete": ["/api/lists.delete"],
    "lists:stats": ["/api/lists.stats"],
    "segments:list": ["/api/segments.list"],
    "segments:get": ["/api/segments.get"],
    "segments:create": ["/api/segments.create"],
    "segments:delete": ["/api/segments.delete"],
    "segments:contacts": ["/api/segments.contacts"],
    "segments:rebuild": ["/api/segments.rebuild"],
    "members:list": ["/api/workspaces.members"],
    "members:invite": ["/api/workspaces.inviteMember"],
    "members:remove": ["/api/workspaces.removeMember"],
    "members:permissions": ["/api/workspaces.setUserPermissions"],
    "magic-link": ["/api/workspaces.generateMagicLink"],
    "events:list": ["/api/customEvents.list"],
    "events:get": ["/api/customEvents.get"],
    "events:upsert": ["/api/customEvents.upsert"],
    "messages:list": ["/api/messages.list"],
    "transactional:list": ["/api/transactional.list"],
    "transactional:get": ["/api/transactional.get"],
    "transactional:create": ["/api/transactional.create"],
    "transactional:delete": ["/api/transactional.delete"],
    "transactional:send": ["/api/transactional.send"],
    "webhooks:list": ["/api/webhookSubscriptions.list"],
    "webhooks:get": ["/api/webhookSubscriptions.get"],
    "webhooks:create": ["/api/webhookSubscriptions.create"],
    "webhooks:delete": ["/api/webhookSubscriptions.delete"],
    "webhooks:test": ["/api/webhookSubscriptions.test"],
    "webhooks:toggle": ["/api/webhookSubscriptions.toggle"],
    "webhooks:regenerateSecret": ["/api/webhookSubscriptions.regenerateSecret"],
    "webhooks:deliveries": ["/api/webhookSubscriptions.deliveries"],
    "keys:provision": ["/api/workspaces.createAPIKey"],
    # Mission 2026-10-04 (audit CLI, coverage Go=199 vs CLI=195) : keys:list/
    # keys:revoke appelaient workspaces.members/removeMember (generique,
    # couverts par ailleurs via members:list/members:remove) -- bascules sur
    # les routes DEDIEES posees par la mission "API & agents" (page console
    # "API & agents"), plus sures (revokeAPIKey refuse un user qui n'est pas
    # une api_key, removeMember etait generique a tout type de membre).
    "keys:list": ["/api/workspaces.listAPIKeys"],
    "keys:revoke": ["/api/workspaces.revokeAPIKey"],
    "integrations:create-smtp": ["/api/workspaces.createIntegration"],
    "integrations:delete": ["/api/workspaces.deleteIntegration"],
    "integrations:update": ["/api/workspaces.updateIntegration"],
    "integrations:list": ["/api/workspaces.get"],
    "settings:set": ["/api/workspaces.update"],
    "provision": ["/api/tenants/provision"],
    "wipe": ["/api/veridian/admin/wipe-test-tenants"],
    "status": ["/api/tenants/{id}/status"],
    "breakdown": ["/api/veridian/contacts.providerBreakdown"],
    "admin:tenants": ["/api/veridian/admin/tenants"],
    "admin:grant-unlimited": ["/api/veridian/admin/grant-unlimited"],
    "admin:gc-orphans": ["/api/veridian/admin/gc-orphan-workspace-dbs"],
    "admin:stats": ["/api/veridian/admin/test-tenants-stats"],
    "admin:cold-simulate": ["/api/veridian/admin/cold-simulate"],
}

# ---------------------------------------------------------------------------
# SPLIT ADMIN / USER (mission Robert 03/10). Liste EXHAUSTIVE, tenue à la
# main, des commandes exposées par le point d'entrée `notifuse` (CLI user,
# scopé à UN workspace via sa propre api_key). Tout ce qui n'est PAS ici
# (HMAC Hub, admin veridian, owner cross-workspace, mint/révocation de clés,
# intégrations, settings, membres, magic-link, long tail générique) reste
# réservé à `notifuse-admin`, qui lui couvre tout (superset, super clé).
# coverage_test.sh vérifie que rien d'admin/HMAC/owner n'a fuité ici.
WORKSPACE_COMMANDS = frozenset({
    # lists
    "lists:list", "lists:get", "lists:create", "lists:update", "lists:delete",
    "lists:stats", "lists:subscribe",
    # contacts
    "contacts:list", "contacts:count", "contacts:get", "contacts:upsert",
    "contacts:import", "contacts:delete", "contacts:get-by-external-id",
    # segments
    "segments:list", "segments:get", "segments:create", "segments:delete",
    "segments:contacts", "segments:rebuild", "segments:update", "segments:preview",
    # templates
    "templates:list", "templates:get", "templates:create", "templates:delete",
    "templates:push", "templates:compile", "templates:update",
    # automations (dont exit/reset d'un contact)
    "automations:list", "automations:get", "automations:create",
    "automations:activate", "automations:pause", "automations:delete",
    "automations:update", "automations:enroll", "automations:exit-contact",
    "automations:reset-contact", "automations:nodeExecutions",
    # broadcasts
    "broadcasts:list", "broadcasts:get", "broadcasts:create", "broadcasts:update",
    "broadcasts:delete", "broadcasts:schedule", "broadcasts:cancel",
    "broadcasts:pause", "broadcasts:resume", "broadcasts:send-test",
    "broadcasts:getTestResults", "broadcasts:selectWinner",
    "broadcasts:refreshGlobalFeed", "broadcasts:testRecipientFeed",
    # transactional
    "transactional:list", "transactional:get", "transactional:create",
    "transactional:delete", "transactional:send", "transactional:update",
    "transactional:testTemplate",
    # webhooks
    "webhooks:list", "webhooks:get", "webhooks:create", "webhooks:delete",
    "webhooks:test", "webhooks:toggle", "webhooks:deliveries",
    "webhooks:update", "webhooks:eventTypes", "webhooks:regenerateSecret",
    # messages / analytics
    "messages:list", "messages:broadcastStats",
    "analytics:query", "analytics:schemas",
    # KPI reponses (GET /api/veridian/messages.replyStats, auth JWT/api_key du
    # workspace + permission contacts:read cote service : aucune route admin/HMAC).
    # Ajoute le 06/10/2026 : la commande etait refusee par le CLI scope.
    "veridian:reply-stats",
    # Fusible de reputation PAR COUPLE (domaine emetteur x classe destinataire),
    # GET /api/veridian/messages.reputationStatus (JWT workspace + message_history:read).
    "veridian:reputation-status",
    # Lot 2 (08/10/2026) : vérité d'un profil, GET /api/veridian/emailProfiles.overview
    # (JWT / clé API du workspace + message_history:read côté service). Lecture seule.
    "profiles:overview",
    # Lot 4 (08/10/2026) : usage et pause d'un profil par l'API dédiée
    # POST /api/veridian/emailProfiles.setUsage|pause|resume (JWT / clé API du
    # workspace + workspace:write côté service). Exclusivité validée par le serveur.
    "profiles:set-usage", "profiles:pause", "profiles:resume",
    # Lot 5 (08/10/2026) : création d'un profil par l'API dédiée (propriétaire ; secret
    # sur stdin ou fichier) et agrégats du tableau de bord de prospection (lecture).
    "profiles:create", "prospection:stats",
    # Lot 1 (10/10/2026) : pourquoi un mail n'est pas parti. queue:explain et logs:decisions en
    # lecture (automations:read) ; queue:recompute en écriture (automations:write, --yes requis).
    "queue:explain", "logs:decisions", "queue:recompute",
    # pixel / scripting (versions sans HMAC/admin, cf cmd_config/_SCOPED_MODE)
    "config", "env",
})

# Routes dont la nature est OWNER/CROSS-WORKSPACE/ADMIN même quand on y
# arrive via le chemin générique (cmd_generic_rv/cmd_raw_generic). Utilisé
# par coverage_test.sh pour prouver qu'aucune route de cette nature n'est
# jamais atteignable par une commande de WORKSPACE_COMMANDS.
ADMIN_ONLY_ROUTE_MARKERS = (
    "/api/tenants/", "/api/veridian/admin/", "/api/sso/", "/api/users/by-email",
    "/api/workspaces.createIntegration", "/api/workspaces.updateIntegration",
    "/api/workspaces.deleteIntegration", "/api/workspaces.update",
    "/api/workspaces.createAPIKey", "/api/workspaces.create", "/api/workspaces.delete",
    "/api/workspaces.inviteMember", "/api/workspaces.removeMember",
    "/api/workspaces.setUserPermissions", "/api/workspaces.generateMagicLink",
    "/api/settings.", "/api/setup.",
)


def die(msg, code=1):
    print(f"✗ {msg}", file=sys.stderr)
    sys.exit(code)


# ---------------------------------------------------------------- env parsing
def load_env():
    if not ENV_PATH.exists():
        die(f"Fichier credentials introuvable : {ENV_PATH}")
    creds = {}
    for line in ENV_PATH.read_text().splitlines():
        s = line.strip()
        if not s or s.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        v = v.strip()
        if len(v) >= 2 and v[0] == v[-1] and v[0] in ("'", '"'):
            v = v[1:-1]
        creds[k.strip()] = v
    return creds


def get_key(var, required=True):
    creds = load_env()
    key = creds.get(var)
    if not key and required:
        die(f"Clé absente : {var} introuvable dans {ENV_PATH}.")
    return key


def secret_from_args(args):
    """Resolve a CLI secret without forcing it through argv/process listings."""
    env_var = getattr(args, "password_env", None)
    if env_var:
        return get_key(env_var)
    password = getattr(args, "password", None)
    if password:
        return password
    die("--password-env (recommandé) ou --password est requis.")


# ---------------------------------------------------------------- HTTP core
def _request(url, method, headers, body):
    data = body.encode() if isinstance(body, str) else body
    req = urllib.request.Request(url, data=data, method=method)
    for h, v in headers.items():
        req.add_header(h, v)
    try:
        with urllib.request.urlopen(req, timeout=40) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            payload = json.loads(raw)
        except Exception:
            payload = {"error": raw[:500] if raw else f"HTTP {e.code}"}
        return e.code, payload
    except urllib.error.URLError as e:
        die(f"Notifuse injoignable ({url}) : {e.reason}. "
            f"{'Staging est derrière Tailscale/DNS — vérifier la connexion.' if 'staging' in url else ''}")
    except Exception as e:
        die(f"Erreur réseau ({url}) : {e}")


def hmac_headers(secret, raw_body):
    ts = str(int(time.time() * 1000))
    mac = hmaclib.new(secret.encode(), f"{ts}.{raw_body}".encode(), hashlib.sha256).hexdigest()
    return {
        "x-veridian-app": "notifuse",
        "x-veridian-timestamp": ts,
        "X-Veridian-Hub-Signature": mac,
    }


def call_hmac(env, method, route, body=None):
    """Tape une route HMAC Hub (provision/admin/cold-simulate)."""
    secret = get_key(HMAC_SECRET_VAR)
    raw = json.dumps(body) if body is not None else ""
    url = BASES[env] + route
    headers = {"Content-Type": "application/json"}
    headers.update(hmac_headers(secret, raw))
    return _request(url, method, headers, raw if body is not None else None)


def call_jwt(env, method, route, jwt, body=None, params=None):
    """Tape une route applicative avec un Bearer JWT (api_key ou owner)."""
    url = BASES[env] + route
    if params:
        qs = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
        url = f"{url}?{qs}"
    headers = {"Authorization": f"Bearer {jwt}", "Content-Type": "application/json"}
    raw = json.dumps(body) if body is not None else None
    return _request(url, method, headers, raw)


def call_anon(env, method, route, body=None, params=None):
    """Tape une route SANS auth (ex workspaces.acceptInvitation, détection favicon)."""
    url = BASES[env] + route
    if params:
        qs = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
        url = f"{url}?{qs}"
    headers = {"Content-Type": "application/json"}
    raw = json.dumps(body) if body is not None else None
    return _request(url, method, headers, raw)


def health(env):
    try:
        with urllib.request.urlopen(BASES[env] + "/api/health", timeout=15) as r:
            return r.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception:
        return None


def out(status, payload, ok_codes=(200, 201)):
    print(json.dumps(payload, indent=2, ensure_ascii=False))
    if status not in ok_codes:
        sys.exit(2)


# ---------------------------------------------------------------- owner session
_OWNER_CACHE = {}
DEFAULT_OWNER_EMAIL = os.environ.get("NOTIFUSE_OWNER_EMAIL", "robert.brunon@veridian.site")

# ---------------------------------------------------------------------------
# MODE SCOPÉ (mission Robert 03/10 "CLI admin root + CLI user scopé") : quand
# le point d'entrée `notifuse` (user) tourne, il n'a PAS NOTIFUSE_HUB_API_SECRET
# et ne doit JAMAIS en dépendre. On bascule owner_jwt()/apikey_for() pour
# renvoyer DIRECTEMENT la clé API scopée fournie par le client comme Bearer —
# le serveur fait le reste : si la route est VRAIMENT owner-only ou cross-
# workspace, il répond 401/403 (c'est la preuve attendue, pas un bug du CLI).
# `notifuse-admin` (mode="admin") ne touche jamais à ce chemin : il garde le
# comportement HISTORIQUE (HMAC → owner JWT auto-dérivé via auto-login).
_SCOPED_MODE = False
_SCOPED_JWT = None
_SCOPED_OPERATOR_WARNED = False


def _scoped_jwt_or_fallback(env, tenant_id, want_owner):
    """Dans le CLI user : utilise la clé API scopée comme Bearer direct.
    Fallback OPÉRATEUR (bastion, super clé dispo) seulement si NOTIFUSE_STRICT
    n'est pas posé — alors on retombe sur le comportement admin historique."""
    global _SCOPED_OPERATOR_WARNED
    if _SCOPED_JWT:
        return _SCOPED_JWT
    if os.environ.get("NOTIFUSE_STRICT") == "1":
        die("NOTIFUSE_STRICT=1 : aucune clé scopée (NOTIFUSE_API_KEY / --key) fournie, "
            "repli super-clé interdit. Mint une clé avec `notifuse-admin keys:mint`.")
    # Repli opérateur : seulement possible si le secret HMAC est dispo (bastion).
    if not _SCOPED_OPERATOR_WARNED:
        print("⚠️  mode opérateur (super clé) : aucune NOTIFUSE_API_KEY scopée fournie, "
              "retombe sur la super clé Hub (NOTIFUSE_STRICT=1 pour l'interdire).",
              file=sys.stderr)
        _SCOPED_OPERATOR_WARNED = True
    if want_owner:
        return _owner_jwt_impl(env, tenant_id)
    return _apikey_for_impl(env, tenant_id)


def provision_idempotent(env, tenant_id, email=DEFAULT_OWNER_EMAIL, name=None):
    """Provision HMAC idempotente. Renvoie la réponse (ws, api_key, auto_login_url)."""
    body = {"tenant_id": tenant_id, "owner_email": email}
    if name:
        body["workspace_name"] = name
    st, p = call_hmac(env, "POST", "/api/tenants/provision", body)
    if st not in (200, 201):
        die(f"provision({tenant_id}) HMAC échouée (HTTP {st}) : {p}. "
            f"Vérifier {HMAC_SECRET_VAR} pour --env {env}.")
    return p


def owner_jwt(env, tenant_id, email=DEFAULT_OWNER_EMAIL):
    """JWT pour une route owner-only. En mode scopé (CLI `notifuse`), renvoie
    la clé API du client (ou refuse/opère en fallback, cf _scoped_jwt_or_fallback) —
    jamais le secret Hub. En mode admin (défaut), comportement historique."""
    if _SCOPED_MODE:
        return _scoped_jwt_or_fallback(env, tenant_id, want_owner=True)
    return _owner_jwt_impl(env, tenant_id, email)


def _owner_jwt_impl(env, tenant_id, email=DEFAULT_OWNER_EMAIL):
    """JWT owner via provision idempotente → auto_login_url → auth_token du HTML.

    Pattern validé (scripts/iac/notifuse-iac.sh, memory reference_jwt_owner_via_autologin).
    L'auto_login_url a un TTL court (~60s) ; on l'échange immédiatement.
    """
    cache_key = (env, tenant_id)
    if cache_key in _OWNER_CACHE:
        return _OWNER_CACHE[cache_key]
    prov = provision_idempotent(env, tenant_id, email)
    auto_url = prov.get("auto_login_url")
    if not auto_url:
        die(f"auto_login_url absent de la provision({tenant_id}).")
    try:
        with urllib.request.urlopen(auto_url, timeout=20) as r:
            html = r.read().decode()
    except Exception as e:
        die(f"auto-login injoignable : {e}")
    m = re.search(r"setItem\('auth_token',\s*\"([^\"]+)\"", html)
    if not m:
        die("extraction JWT owner échouée (auth_token absent du HTML auto-login).")
    jwt = m.group(1)
    _OWNER_CACHE[cache_key] = jwt
    return jwt


def apikey_for(env, tenant_id):
    """JWT pour une route applicative non-owner. En mode scopé, la clé du
    client (ou fallback opérateur) ; en mode admin, comportement historique."""
    if _SCOPED_MODE:
        return _scoped_jwt_or_fallback(env, tenant_id, want_owner=False)
    return _apikey_for_impl(env, tenant_id)


def _apikey_for_impl(env, tenant_id):
    """Priorité : NOTIFUSE_API_KEY (machine, fournie) → api_key d'une provision FRAÎCHE
    (created:true) → sinon owner JWT (toujours obtenable via auto_login_url ; l'owner
    a tous les droits). La provision idempotente d'un tenant EXISTANT ne re-renvoie
    PAS l'api_key (created:false), donc on retombe proprement sur l'owner JWT.
    """
    k = get_key(APIKEY_VAR, required=False)
    if k:
        return k
    prov = provision_idempotent(env, tenant_id)
    ak = prov.get("api_key")
    if ak:
        return ak
    return owner_jwt(env, tenant_id)


def jwt_for_route(env, resource_verb, workspace, prefer_owner=False):
    """Choisit le bon JWT pour une route applicative selon OWNER_ONLY."""
    if prefer_owner or resource_verb in OWNER_ONLY:
        return owner_jwt(env, workspace)
    return apikey_for(env, workspace)


# ---------------------------------------------------------------- generic app call
def app_call(env, resource_verb, workspace, body=None, params=None,
             method=None, prefer_owner=False):
    """Tape /api/<resource.verb> en choisissant méthode + auth automatiquement."""
    route = f"/api/{resource_verb}"
    is_get = method == "GET" or (method is None and resource_verb in GET_VERBS)
    jwt = jwt_for_route(env, resource_verb, workspace, prefer_owner)
    if is_get:
        q = dict(params or {})
        q.setdefault("workspace_id", workspace)
        return call_jwt(env, "GET", route, jwt, params=q)
    b = dict(body or {})
    b.setdefault("workspace_id", workspace)
    return call_jwt(env, "POST", route, jwt, body=b)


# ================================================================ COMMANDS
def _dns_txt(name):
    """Enregistrements TXT, via l'outil systeme pour ne dependre d'aucun paquet."""
    import subprocess
    try:
        r = subprocess.run(["dig", "+short", "TXT", name], capture_output=True, text=True, timeout=12)
        return [l.strip().strip('"') for l in r.stdout.splitlines() if l.strip()]
    except Exception:
        return []


def _dns_one(name, typ):
    import subprocess
    try:
        r = subprocess.run(["dig", "+short", typ, name], capture_output=True, text=True, timeout=12)
        return [l.strip().rstrip(".") for l in r.stdout.splitlines() if l.strip()]
    except Exception:
        return []


def _reverse(ip):
    """Reverse vu par plusieurs resolveurs.

    Un seul resolveur ment pendant la propagation : juste apres un changement,
    Google peut servir l'ancien nom quand Cloudflare sert deja le nouveau. Crier
    a la regression sur un cache perime apprend a ignorer l'outil, donc on
    interroge plusieurs sources et on signale la divergence pour ce qu'elle est.
    """
    import subprocess
    seen = []
    for resolver in ("@1.1.1.1", "@8.8.8.8", ""):
        cmd = ["dig", "+short", "-x", ip] + ([resolver] if resolver else [])
        try:
            r = subprocess.run(cmd, capture_output=True, text=True, timeout=12)
            v = r.stdout.strip().rstrip(".")
            if v:
                seen.append(v)
        except Exception:
            continue
    if not seen:
        return None
    # On retient la valeur majoritaire ; en cas d'egalite, la premiere vue.
    best = max(set(seen), key=seen.count)
    if len(set(seen)) > 1:
        return best + "  (propagation en cours, resolveurs divergents)"
    return best


def _shingles(text, size=4):
    import re
    t = re.sub(r"<[^>]+>", " ", text or "")
    t = re.sub(r"&[a-z]+;", " ", t)
    t = re.sub(r"[^\w\s'-]", " ", t)
    words = re.sub(r"\s+", " ", t).strip().lower().split()
    if len(words) < size:
        return {" ".join(words)} if words else set()
    return {" ".join(words[i:i + size]) for i in range(len(words) - size + 1)}


def _overlap(a, b):
    sa, sb = _shingles(a), _shingles(b)
    if not sa or not sb:
        return 0.0
    return 100.0 * len(sa & sb) / len(sa | sb)


def _overlap_severity(value):
    """Ignore shared scaffolding that is normal in short plain-text emails."""
    if value >= 80:
        return "blocking"
    if value >= 70:
        return "warning"
    return None


def _smtp_integrations(wobj):
    """Return SMTP integrations only; IMAP reply collectors are not senders."""
    return [
        integration for integration in (wobj.get("integrations") or [])
        if (((integration.get("email_provider") or {}).get("smtp") or {}).get("host"))
    ]


def _active_campaign_integrations(wobj):
    """Return the provider that Notifuse actually uses for marketing sends.

    A workspace can retain historical SMTP profiles. Counting all of them made
    campaign:audit advertise fictitious capacity and inspect dormant IPs.
    """
    configured = _smtp_integrations(wobj)
    settings = wobj.get("settings") or {}
    active_id = settings.get("marketing_email_provider_id") or wobj.get("marketing_email_provider_id")
    if not active_id:
        return []
    return [integration for integration in configured if integration.get("id") == active_id]


def _integration_daily_cap(integration):
    ep = integration.get("email_provider") or {}
    caps = ep.get("veridian_provider_class_daily_cap") or {}
    provider_total = sum(v for v in caps.values() if isinstance(v, (int, float)) and v > 0)
    profile_cap = ep.get("veridian_profile_daily_cap")
    if isinstance(profile_cap, (int, float)) and profile_cap > 0:
        return min(provider_total, profile_cap) if provider_total else profile_cap
    return provider_total


def cmd_campaign_audit(a):
    """Verdict AVANT envoi : est-ce qu'on peut lancer sans se cramer ?

    Chaque controle repond a une question qu'on paierait cher a decouvrir apres
    coup. Un point BLOQUANT signifie qu'envoyer degradera durablement la
    reputation d'un domaine — ce qui se repare en semaines, pas en heures.
    """
    ws = a.workspace
    blocking, warnings, notes = [], [], []
    P = lambda s: print(s, file=sys.stderr)

    P(f"╔══ AUDIT CAMPAGNE — workspace {ws} ══╗")

    # ---- 1. Emetteurs : domaines, IP, authentification ----
    # Le detail complet des providers (senders, plafonds, pixel) vit dans le
    # workspace ; la route de liste renvoie des senders a null. Se fier a la
    # liste ferait conclure "aucun expediteur" sur un workspace bien configure,
    # et un faux NO-GO ferait ignorer l'outil aussi surement qu'un faux GO.
    jwt = owner_jwt(a.env, ws)
    _, wsd = call_jwt(a.env, "GET", "/api/workspaces.get", jwt, params={"id": ws})
    wobj = (wsd or {}).get("workspace", wsd) or {}
    configured_integrations = _smtp_integrations(wobj)
    integrations = _active_campaign_integrations(wobj)
    settings = wobj.get("settings") or {}
    active_provider_id = settings.get("marketing_email_provider_id") or wobj.get("marketing_email_provider_id")
    senders, hosts = [], set()
    for i in integrations:
        ep = i.get("email_provider") or {}
        for s in ep.get("senders") or []:
            addr = s.get("email") if isinstance(s, dict) else s
            if addr and "@" in addr:
                senders.append((i.get("name"), addr, ep))
        smtp = ep.get("smtp") or {}
        if smtp.get("host"):
            hosts.add(smtp["host"])
    domains = sorted({addr.split("@")[-1] for _, addr, _ in senders})

    P("\n── Emetteurs ──")
    P(f"  {len(integrations)} profil SMTP actif ({len(configured_integrations)} configure(s))")
    P(f"  {len(senders)} expediteur(s) sur {len(domains)} domaine(s) : {', '.join(domains) or 'AUCUN'}")
    if not active_provider_id:
        blocking.append("aucun provider marketing actif selectionne dans le workspace")
    elif not integrations:
        blocking.append(f"provider marketing actif {active_provider_id} introuvable ou sans SMTP")
    if not senders:
        blocking.append("aucun expediteur configure : rien ne peut partir")
    for _, addr, ep in senders:
        sid = None
        for s in ep.get("senders") or []:
            if isinstance(s, dict) and s.get("email") == addr:
                sid = s.get("id")
        if not sid:
            blocking.append(f"expediteur {addr} sans identifiant : aucun template ne peut le cibler")

    # ---- 2. Authentification de chaque domaine ----
    P("\n── Authentification des domaines ──")
    for d in domains:
        # DBL : un domaine liste est rejete quelle que soit l'IP qui l'expedie,
        # et le rejet cite le domaine trouve DANS le message. Mesure du 0807 :
        # agences-veridian.fr a ete liste alors que les trois IP restaient propres.
        dbl = _dns_one(f"{d}.dbl.spamhaus.org", "A")
        if dbl:
            blocking.append(f"{d} liste sur la DBL Spamhaus ({dbl[0]}) — tout envoi "
                            "portant ce domaine sera rejete, delistage a demander")
        spf = [t for t in _dns_txt(d) if t.startswith("v=spf1")]
        dkim = _dns_txt(f"mail._domainkey.{d}")
        dmarc = [t for t in _dns_txt(f"_dmarc.{d}") if t.startswith("v=DMARC1")]
        ok = lambda b: "✓" if b else "✗"
        P(f"  {d:<34} SPF {ok(spf)}  DKIM {ok(dkim)}  DMARC {ok(dmarc)}")
        if not spf:
            blocking.append(f"{d} sans SPF : le destinataire ne peut pas verifier l'origine")
        if not dkim:
            blocking.append(f"{d} sans cle DKIM publiee : signature invalide ou absente")
        if not dmarc:
            warnings.append(f"{d} sans DMARC : aucune politique annoncee")

    # ---- 3. Reverse DNS des serveurs d'envoi, boucle fermee ----
    P("\n── Reverse DNS des serveurs SMTP ──")
    for h in sorted(hosts):
        ips = _dns_one(h, "A") + _dns_one(h, "AAAA")
        if not ips:
            warnings.append(f"{h} ne resout vers aucune adresse")
            continue
        for ip in ips:
            rev = _reverse(ip)
            clean = rev.split("  (")[0] if rev else None
            back = _dns_one(clean, "A") + _dns_one(clean, "AAAA") if clean else []
            closed = rev and ip in back
            generic = clean and any(k in clean for k in ("vps.ovh.net", "contaboserver", "amazonaws", "compute."))
            P(f"  {ip:<42} -> {rev or 'AUCUN'} {'✓ boucle fermee' if closed else '✗ boucle ouverte'}")
            if not rev:
                blocking.append(f"{ip} sans reverse DNS : rejet quasi systematique")
            elif generic:
                warnings.append(f"{ip} a un reverse generique d'hebergeur ({rev})")
            elif not closed:
                warnings.append(f"{ip} : le reverse {rev} ne renvoie pas vers cette adresse")

    # ---- 4. Plafonds et tracking ----
    P("\n── Cadence et discretion ──")
    total_day = 0
    for i in integrations:
        ep = i.get("email_provider") or {}
        caps = ep.get("veridian_provider_class_daily_cap") or {}
        pixels = ep.get("veridian_tracking_pixel") or {}
        total_day += _integration_daily_cap(i)
        if not caps:
            warnings.append(f"integration {i.get('name')} sans plafond journalier")
        if any(bool(v) for v in pixels.values()):
            warnings.append(f"integration {i.get('name')} avec pixel de suivi actif")
    P(f"  plafond cumule : ~{int(total_day)} envois/jour sur {len(integrations)} emetteur(s)")
    # Les plafonds sont poses PAR EMETTEUR, mais le serveur d'en face ne voit que
    # l'IP du relai. Trois emetteurs derriere une meme IP, c'est trois fois la
    # pression annoncee sur cette IP, et le destinataire limite au tiers du debit
    # attendu. Mesure du 0807 : OVH a repondu « 450 rate limit » a partir de sept
    # messages en vingt minutes depuis une seule IP.
    par_ip = {}
    for h in sorted(hosts):
        for ip in _dns_one(h, "A"):
            par_ip[ip] = par_ip.get(ip, 0) + sum(
                1 for i in integrations
                if ((i.get("email_provider") or {}).get("smtp") or {}).get("host") == h
            )
    for ip, n in par_ip.items():
        if n > 1:
            P(f"  {n} emetteur(s) partagent l'IP {ip} : la pression reelle y est {n}x l'annonce")
            warnings.append(f"{n} emetteurs partagent l'IP {ip} — les plafonds par emetteur "
                            "ne protegent pas cette IP, calibrer la cadence en consequence")
    if total_day == 0:
        warnings.append("aucun plafond journalier : rien ne freine un envoi qui derape")

    # ---- 5. Recouvrement de contenu entre templates ----
    P("\n── Recouvrement de contenu (empreintes collaboratives) ──")
    st, payload = app_call(a.env, "templates.list", ws, method="GET")
    templates = (payload or {}).get("templates") or []
    bodies = []
    for t in templates:
        em = t.get("email") or {}
        src = em.get("mjml_source") or em.get("compiled_preview") or ""
        if src:
            bodies.append((t.get("id"), src))
    pairs = 0
    worst = (None, None, 0.0)
    for x in range(len(bodies)):
        for y in range(x + 1, len(bodies)):
            ov = _overlap(bodies[x][1], bodies[y][1])
            pairs += 1
            if ov > worst[2]:
                worst = (bodies[x][0], bodies[y][0], ov)
    if pairs:
        P(f"  {len(bodies)} template(s), pire recouvrement : {worst[0]} / {worst[1]} = {worst[2]:.1f} %")
        overlap_severity = _overlap_severity(worst[2])
        if overlap_severity == "blocking":
            blocking.append(f"templates {worst[0]} et {worst[1]} identiques a {worst[2]:.0f} % : "
                            "une seule empreinte pour toute la campagne")
        elif overlap_severity == "warning":
            warnings.append(f"templates {worst[0]} et {worst[1]} proches a {worst[2]:.0f} % : "
                            "rediger de vraies variantes, la personnalisation ne suffit pas")
    else:
        notes.append("moins de deux templates : rien a comparer")

    # ---- 6. Le vivier ----
    P("\n── Vivier ──")
    st, payload = app_call(a.env, "contacts.count", ws, method="GET")
    contacts = (payload or {}).get("total_contacts", 0)
    st, payload = app_call(a.env, "segments.list", ws, method="GET")
    segments = (payload or {}).get("segments") or []
    P(f"  {contacts} contact(s) · {len(segments)} segment(s)")
    if contacts == 0:
        blocking.append("aucun contact charge")
    if total_day and contacts:
        jours = contacts / max(total_day, 1)
        P(f"  a plafond constant, epuiser le vivier prendrait ~{jours:.0f} jour(s)")

    # ---- Verdict ----
    P("\n" + "═" * 64)
    for b in blocking:
        P(f"  🛑 BLOQUANT   {b}")
    for w in warnings:
        P(f"  ⚠️  ATTENTION  {w}")
    for n in notes:
        P(f"  ·  {n}")
    verdict = "NO-GO" if blocking else ("GO SOUS RESERVE" if warnings else "GO")
    P(f"\n  VERDICT : {verdict}")
    print(json.dumps({"workspace": ws, "verdict": verdict, "blocking": blocking,
                      "warnings": warnings, "senders": len(senders), "domains": domains,
                      "active_provider_id": active_provider_id,
                      "active_integrations": len(integrations),
                      "configured_smtp_integrations": len(configured_integrations),
                      "contacts": contacts, "daily_cap": int(total_day)},
                     indent=2, ensure_ascii=False))
    sys.exit(2 if blocking else 0)


def cmd_doctor(a):
    env = a.env
    print(f"# notifuse doctor — env={env} ({BASES[env]})", file=sys.stderr)
    hc = health(env)
    print(f"  health        : {hc} {'✓' if hc == 200 else '✗'}", file=sys.stderr)
    secret_present = bool(get_key(HMAC_SECRET_VAR, required=False))
    print(f"  HUB secret    : {'présent ✓' if secret_present else 'ABSENT ✗'}", file=sys.stderr)

    # HMAC round-trip réel : un cold-simulate (staging) ou un tenants list (prod).
    # 401/503 = secret rejeté/non câblé ; 200/400 = auth OK.
    hmac_ok = False
    hmac_detail = ""
    if secret_present:
        st, p = call_hmac(env, "GET", "/api/veridian/admin/tenants?limit=1", None)
        hmac_ok = st in (200, 400)
        hmac_detail = f"HTTP {st}"
    print(f"  HMAC auth     : {hmac_detail} {'✓ (secret accepté)' if hmac_ok else '✗'}", file=sys.stderr)

    # cold-simulate (staging-only) : prouve le chemin verify/dry-run.
    cold_ok = None
    if env == "staging" and secret_present:
        st, p = call_hmac(env, "POST", "/api/veridian/admin/cold-simulate",
                          {"mode": "sending_window_decision", "workspace_id": "__doctor__"})
        cold_ok = st in (200, 400)
        print(f"  cold-simulate : HTTP {st} {'✓' if cold_ok else '✗ (staging-only ?)'}", file=sys.stderr)

    verdict = "ok" if (hc == 200 and (hmac_ok or not secret_present)) else "degraded"
    result = {
        "env": env, "base": BASES[env],
        "health": hc, "health_ok": hc == 200,
        "hub_secret_present": secret_present, "hmac_auth_ok": hmac_ok,
        "cold_simulate_ok": cold_ok,
        "verdict": verdict,
    }
    print(json.dumps(result, indent=2, ensure_ascii=False))
    sys.exit(0 if verdict == "ok" else 2)


def cmd_provision(a):
    body = {"tenant_id": a.tenant_id, "owner_email": a.email}
    if a.name:
        body["workspace_name"] = a.name
    if a.plan:
        body["plan"] = a.plan
    st, p = call_hmac(a.env, "POST", "/api/tenants/provision", body)
    if st in (200, 201):
        print("# ⚠  api_key + auto_login_url renvoyés — persiste l'api_key (Hub). "
              "auto_login_url TTL ~60s.", file=sys.stderr)
    out(st, p)


def cmd_wipe(a):
    if not a.prefix and not a.ids:
        die("--prefix P OU --ids a,b requis")
    body = {}
    if a.prefix:
        body["prefix"] = a.prefix
    if a.ids:
        body["tenant_ids"] = [t.strip() for t in a.ids.split(",") if t.strip()]
    out(*call_hmac(a.env, "POST", "/api/veridian/admin/wipe-test-tenants", body))


# ---- verify / dry-run : batterie de prédicats cold, ZÉRO mail -----
COLD_MODES = [
    ("sending_window_decision", "fenêtre d'envoi (horaires ouvrables)"),
    ("daily_cap_decision", "cap journalier par destinataire"),
    ("class_cap_decision", "cap journalier par classe de provider"),
    ("per_sender_cap_decision", "cap journalier par sender (warmup IP)"),
    ("warmup_cap_decision", "cap total warmup progressif par infra"),
]


def cmd_verify(a):
    """DRY-RUN cold : frappe les PRÉDICATS EXACTS des gates worker, sans envoi."""
    if a.env != "staging":
        die("verify/dry-run = cold-simulate, STAGING-ONLY (503 ailleurs). "
            "Relance avec --env staging.")
    ws = a.workspace
    results = {}
    # 1. existence + plan via status léger
    st0, _ = call_hmac(a.env, "GET", f"/api/veridian/admin/tenants?prefix={urllib.parse.quote(ws)}", None)
    # 2. chaque gate
    for mode, label in COLD_MODES:
        body = {"mode": mode, "workspace_id": ws}
        if mode == "daily_cap_decision":
            body.update({"contact_email": a.contact or "probe@gmail.com",
                         "per_recipient_cap": a.cap or 1})
        elif mode == "class_cap_decision":
            body.update({"provider_class": a.cls or "google", "class_cap": a.cap or 1})
        elif mode == "per_sender_cap_decision":
            body.update({"sender_email": a.sender or "send@agences-veridian.fr",
                         "per_sender_cap": a.cap or 1})
        elif mode == "warmup_cap_decision":
            body.update({"sender_domain": a.sender_domain or "agences-veridian.fr",
                         "warmup_cap": a.cap or 1})
        elif mode == "sending_window_decision":
            # le gate exige une fenêtre à évaluer : défaut cold lun-ven 9h-18h.
            body["sending_window"] = {
                "days": [1, 2, 3, 4, 5], "start_hour": 9, "end_hour": 18,
                "timezone": a.tz or "Europe/Paris",
            }
            body["fallback_tz"] = a.tz or "Europe/Paris"
        st, p = call_hmac(a.env, "POST", "/api/veridian/admin/cold-simulate", body)
        results[mode] = {"http": st, "label": label, "result": p}
    summary = {
        "workspace": ws, "env": a.env, "mode": "DRY-RUN (zéro mail réel)",
        "gates": results,
        "note": "would_be_capped/would_be_skipped = le gate BLOQUERAIT cet envoi. "
                "C'est le prédicat exact du worker, pas un mock.",
    }
    print(json.dumps(summary, indent=2, ensure_ascii=False))
    bad = [m for m, r in results.items() if r["http"] not in (200, 400)]
    sys.exit(2 if bad else 0)


def cmd_status(a):
    """État consolidé : plan/quota (HMAC) + intégrations + breakdown (owner JWT)."""
    ws = a.workspace
    consolidated = {"workspace": ws, "env": a.env}

    # plan/quota via tenant status (HMAC, ne pollue pas)
    st, p = call_hmac(a.env, "GET", f"/api/tenants/{urllib.parse.quote(ws)}/status", None)
    consolidated["tenant_status"] = {"http": st, "data": p}

    # owner JWT pour les reads riches
    try:
        jwt = owner_jwt(a.env, ws)
    except SystemExit:
        consolidated["owner_session"] = "indisponible (provision/owner JWT échoué)"
        print(json.dumps(consolidated, indent=2, ensure_ascii=False))
        sys.exit(2)

    # workspace.get (GET ?id=)
    _, wsd = call_jwt(a.env, "GET", "/api/workspaces.get", jwt, params={"id": ws})
    consolidated["workspace"] = wsd.get("workspace", wsd)

    # contacts count
    _, cc = call_jwt(a.env, "GET", "/api/contacts.count", jwt, params={"workspace_id": ws})
    consolidated["contacts_count"] = cc

    # breakdown par classe
    _, bd = call_jwt(a.env, "GET", "/api/veridian/contacts.providerBreakdown", jwt,
                     params={"workspace_id": ws})
    consolidated["provider_breakdown"] = bd

    print(json.dumps(consolidated, indent=2, ensure_ascii=False))


def cmd_breakdown(a):
    params = {"workspace_id": a.workspace}
    if a.list:
        params["list_id"] = a.list
    jwt = jwt_for_route(a.env, "veridian/contacts.providerBreakdown", a.workspace)
    out(*call_jwt(a.env, "GET", "/api/veridian/contacts.providerBreakdown", jwt, params=params))


# ---- keys ----
def cmd_keys_list(a):
    # Mission 2026-10-04 : route dediee (listAPIKeys) plutot que de filtrer
    # workspaces.members a la main -- meme info, moins de bruit (pas les
    # membres humains), et couvre la route posee par la page console
    # "API & agents" au lieu de la laisser sans commande CLI.
    jwt = owner_jwt(a.env, a.workspace)
    out(*call_jwt(a.env, "GET", "/api/workspaces.listAPIKeys", jwt, params={"workspace_id": a.workspace}))


def cmd_keys_provision(a):
    # createAPIKey attend `email_prefix` (PAS `email`) : le serveur forge
    # <prefix>@api.<workspace>. Renvoie {token, email}.
    jwt = owner_jwt(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "email_prefix": a.prefix or "cli"}
    out(*call_jwt(a.env, "POST", "/api/workspaces.createAPIKey", jwt, body=body))


def _slugify_ws(ws):
    return re.sub(r"[^A-Z0-9]+", "_", ws.upper()).strip("_")


def _find_member(env, workspace, jwt, *, email=None, user_id=None):
    _, data = call_jwt(env, "GET", "/api/workspaces.members", jwt, params={"id": workspace})
    rows = data.get("members") or data.get("data") or [] if isinstance(data, dict) else []
    for m in rows:
        if not isinstance(m, dict):
            continue
        if email and m.get("email") == email:
            return m
        if user_id and (m.get("user_id") or m.get("id")) == user_id:
            return m
    return None


def _store_scoped_key(workspace, token, email):
    """Range la clé scopée dans ~/credentials/.all-creds.env sous une
    variable PAR WORKSPACE (jamais écrasée pour un autre workspace). N'écrit
    JAMAIS le secret dans un dépôt ni dans un log — ce fichier n'est pas versionné."""
    var = f"NOTIFUSE_API_KEY_{_slugify_ws(workspace)}"
    lines = ENV_PATH.read_text().splitlines() if ENV_PATH.exists() else []
    out_lines = []
    replaced = False
    for line in lines:
        if line.strip().startswith(f"{var}="):
            out_lines.append(f'{var}="{token}"')
            replaced = True
        else:
            out_lines.append(line)
    if not replaced:
        out_lines.append(f'{var}="{token}"')
    ENV_PATH.write_text("\n".join(out_lines) + "\n")
    return var


def cmd_keys_mint(a):
    """keys:mint — crée une clé API SCOPÉE à UN SEUL workspace (réservé à
    notifuse-admin, super clé). Repose sur workspaces.createAPIKey, déjà
    scopé CÔTÉ SERVEUR : le user créé n'est membre QUE de ce workspace
    (AuthenticateUserForWorkspace refuse toute autre workspace_id — preuve
    faite par la mission, cf §4 du brief). --role readonly pose en plus des
    permissions read-only (setUserPermissions) ; --role member (défaut) =
    permissions complètes MAIS confinées à ce workspace."""
    if a.role not in ("member", "readonly"):
        die("--role : seuls 'member' (défaut, complet sur CE workspace) et "
            "'readonly' sont supportés — pas de route serveur pour promouvoir "
            "une api_key en 'owner'.")
    jwt = owner_jwt(a.env, a.workspace)
    prefix = re.sub(r"[^a-z0-9-]+", "-", (a.name or f"client-{int(time.time())}").lower()).strip("-")
    code, resp = call_jwt(a.env, "POST", "/api/workspaces.createAPIKey", jwt,
                           body={"workspace_id": a.workspace, "email_prefix": prefix})
    if code not in (200, 201):
        out(code, resp)
        return
    token = resp.get("token") or resp.get("api_key")
    email = resp.get("email")
    if not token:
        die(f"createAPIKey n'a pas renvoyé de token : {resp}")
    if a.role == "readonly":
        m = _find_member(a.env, a.workspace, jwt, email=email)
        if not m:
            die("clé créée mais introuvable dans workspaces.members pour poser le read-only "
                "(relancer keys:revoke si besoin de nettoyer).")
        uid = m.get("user_id") or m.get("id")
        rc, rp = call_jwt(a.env, "POST", "/api/workspaces.setUserPermissions", jwt,
                           body={"workspace_id": a.workspace, "user_id": uid,
                                 "permissions": {r: {"read": True, "write": False}
                                                 for r in PERMISSION_RESOURCES}})
        if rc not in (200, 201):
            die(f"clé créée mais pose du read-only refusée (HTTP {rc}) : {rp}")
    masked = _mask_secret(token)
    if a.store:
        var = _store_scoped_key(a.workspace, token, email)
        out(200, {"workspace": a.workspace, "email": email, "role": a.role,
                  "stored_as": var, "env_file": str(ENV_PATH), "token_masked": masked})
    else:
        print(f"✓ clé scopée workspace={a.workspace} email={email} role={a.role}")
        print(f"  NOTIFUSE_API_KEY={token}")
        print("  (imprimée UNE fois — note-la ; --store la range dans "
              "~/credentials/.all-creds.env à la place)")


def cmd_keys_revoke(a):
    """keys:revoke — révoque une api_key via la route DEDIEE revokeAPIKey
    (mission 2026-10-04 : coverage Go=199 vs CLI=195, cette route posee par
    la page console "API & agents" n'avait pas de commande). Plus sur que
    l'ancien removeMember generique : le serveur refuse explicitement
    ("target user is not an API key") si la cible n'est pas une clé API."""
    jwt = owner_jwt(a.env, a.workspace)
    user_id = a.user_id
    if not user_id:
        if not a.email:
            die("--user-id ou --email requis (voir `keys:list` pour les retrouver).")
        m = _find_member(a.env, a.workspace, jwt, email=a.email)
        if not m:
            die(f"aucun membre avec l'email {a.email} dans {a.workspace}.")
        user_id = m.get("user_id") or m.get("id")
    out(*call_jwt(a.env, "POST", "/api/workspaces.revokeAPIKey", jwt,
                   body={"workspace_id": a.workspace, "user_id": user_id}))


# ---- lists ----
def cmd_lists_create(a):
    body = _json_arg(a.data) if a.data else {
        "id": a.id, "name": a.name,
        "is_double_optin": bool(a.double_optin), "is_public": bool(a.public),
    }
    if a.description and "description" not in body:
        body["description"] = a.description
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "lists.create", a.workspace, body=body, method="POST"))


def cmd_lists_update(a):
    body = _json_arg(a.data) if a.data else {"id": a.id}
    if a.name:
        body["name"] = a.name
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "lists.update", a.workspace, body=body, method="POST"))


def cmd_lists_stats(a):
    # le handler attend le query param `list_id` (pas `id`).
    jwt = jwt_for_route(a.env, "lists.stats", a.workspace)
    out(*call_jwt(a.env, "GET", "/api/lists.stats", jwt,
                  params={"workspace_id": a.workspace, "list_id": a.id}))


# ---- segments ----
def cmd_segments_create(a):
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "segments.create", a.workspace, body=body, method="POST"))


def cmd_segments_contacts(a):
    out(*app_call(a.env, "segments.contacts", a.workspace,
                  params={"workspace_id": a.workspace, "segment_id": a.id}, method="GET"))


def cmd_segments_rebuild(a):
    # Le handler rebuild utilise explicitement `segment_id`, contrairement à
    # segments.get/delete qui utilisent `id`.
    body = {"workspace_id": a.workspace, "segment_id": a.id}
    out(*app_call(a.env, "segments.rebuild", a.workspace, body=body, method="POST"))


# ---- members / team ----
def cmd_members_list(a):
    jwt = owner_jwt(a.env, a.workspace)
    out(*call_jwt(a.env, "GET", "/api/workspaces.members", jwt, params={"id": a.workspace}))


# Les 10 ressources Notifuse (cf internal/domain/workspace.go PermissionResource).
# ⚠ La route inviteMember NE POSE PAS de permissions par défaut : un member invité
# sans `permissions` arrive avec des droits VIDES → "Insufficient permissions: read
# access to contacts required" sur le dashboard. On pose donc les pleins droits par
# défaut (= domain.FullPermissions côté serveur), sauf restriction explicite.
PERMISSION_RESOURCES = ["contacts", "lists", "templates", "broadcasts", "transactional",
                        "workspace", "message_history", "blog", "automations", "llm"]


def full_permissions():
    return {r: {"read": True, "write": True} for r in PERMISSION_RESOURCES}


def _resolve_permissions(a):
    """Résout les permissions à poser depuis les flags.
    --permissions '<json>' : objet complet (prioritaire).
    --read-only            : les 10 ressources en read seul.
    --resources a,b,c      : restreint aux ressources listées (read+write).
    défaut                 : FullPermissions (les 10, read+write)."""
    if getattr(a, "permissions", None):
        return _json_arg(a.permissions)
    read_only = getattr(a, "read_only", False)
    rw = {"read": True, "write": not read_only}
    only = getattr(a, "resources", None)
    if only:
        wanted = [r.strip() for r in only.split(",") if r.strip()]
        bad = [r for r in wanted if r not in PERMISSION_RESOURCES]
        if bad:
            die(f"ressource(s) inconnue(s): {', '.join(bad)}. Valides: {', '.join(PERMISSION_RESOURCES)}")
        return {r: dict(rw) for r in wanted}
    return {r: dict(rw) for r in PERMISSION_RESOURCES}


def cmd_members_invite(a):
    jwt = owner_jwt(a.env, a.workspace)
    perms = _resolve_permissions(a)
    body = {"workspace_id": a.workspace, "email": a.email, "permissions": perms}
    out(*call_jwt(a.env, "POST", "/api/workspaces.inviteMember", jwt, body=body))


def cmd_members_remove(a):
    jwt = owner_jwt(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "user_id": a.user_id}
    out(*call_jwt(a.env, "POST", "/api/workspaces.removeMember", jwt, body=body))


def cmd_members_permissions(a):
    jwt = owner_jwt(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "user_id": a.user_id,
            "permissions": _resolve_permissions(a)}
    out(*call_jwt(a.env, "POST", "/api/workspaces.setUserPermissions", jwt, body=body))


def cmd_members_get_permissions(a):
    """Lit les permissions des membres (les members:list les porte déjà). Résumé
    lisible : par membre, les ressources avec r/w. Filtrable par user_id/email."""
    code, data = call_jwt(a.env, "GET", "/api/workspaces.members", owner_jwt(a.env, a.workspace),
                          params={"id": a.workspace})
    if code >= 300 or not isinstance(data, dict):
        out(code, data); return
    members = data.get("members") or data.get("data") or []
    want_uid = getattr(a, "user_id", None)
    want_email = getattr(a, "email", None)
    result = []
    for m in members:
        # members:list renvoie des userWorkspace (user_id + role + permissions).
        # Les api_key portent type=="api_key" ; les vrais users n'ont pas de type.
        if m.get("type") == "api_key":
            continue
        uid = m.get("user_id") or m.get("id")
        email = m.get("email")
        if want_uid and uid != want_uid:
            continue
        if want_email and email and email != want_email:
            continue
        perms = m.get("permissions") or {}
        granted = {r: ("rw" if p.get("write") else "r") if p.get("read") else "-"
                   for r, p in perms.items()}
        missing = [r for r in PERMISSION_RESOURCES if r not in perms or not perms[r].get("read")]
        result.append({"user_id": uid, "email": email, "role": m.get("role"),
                       "permissions": granted,
                       "full_access": not missing,
                       "missing_read": missing})
    out(200, {"workspace": a.workspace, "members": result})


MAGICLINK_PREFIX = "magiclink"
MAGICLINK_STALE_AFTER = 600  # secondes : au-delà, une clé "magiclink*" est une fuite à balayer


def _revoke_api_key_by_email(env, workspace, owner_token, email):
    """Révoque une clé API par son email technique. Renvoie (ok, détail)."""
    m = _find_member(env, workspace, owner_token, email=email)
    if not m:
        return False, "membre introuvable dans workspaces.members"
    uid = m.get("user_id") or m.get("id")
    st, p = call_jwt(env, "POST", "/api/workspaces.revokeAPIKey", owner_token,
                     body={"workspace_id": workspace, "user_id": uid})
    return st in (200, 201), f"HTTP {st}"


def _sweep_stale_magiclink_keys(env, workspace, owner_token, now=None):
    """Balaye les clés "magiclink*" laissées par d'anciens appels (avant le
    correctif du 08/10/2026 chaque magic-link en laissait une, 15 à la main le 07/10).
    Ne touche que celles de plus de MAGICLINK_STALE_AFTER secondes : un appel
    concurrent en cours garde sa clé. Renvoie le nombre révoqué."""
    st, data = call_jwt(env, "GET", "/api/workspaces.listAPIKeys", owner_token, params={"workspace_id": workspace})
    if st != 200 or not isinstance(data, dict):
        return 0
    rows = data.get("api_keys") or data.get("keys") or data.get("data") or []
    now = now or datetime.now(timezone.utc)
    revoked = 0
    for k in rows:
        if not isinstance(k, dict) or not str(k.get("name", "")).startswith(MAGICLINK_PREFIX):
            continue
        try:
            created = datetime.fromisoformat(str(k.get("created_at", "")).replace("Z", "+00:00"))
        except ValueError:
            continue
        if (now - created).total_seconds() < MAGICLINK_STALE_AFTER:
            continue
        rst, _ = call_jwt(env, "POST", "/api/workspaces.revokeAPIKey", owner_token,
                          body={"workspace_id": workspace, "user_id": k.get("user_id")})
        if rst in (200, 201):
            revoked += 1
    return revoked


def cmd_magic_link(a):
    """workspaces.generateMagicLink EXIGE un token API KEY (pas owner JWT, pas
    HMAC) et infère le workspace depuis cette clé ; le body ne porte que
    user_email. Si NOTIFUSE_API_KEY n'est pas fournie, on mint une clé
    ÉPHÉMÈRE via createAPIKey (owner), on l'utilise comme Bearer, puis on la
    RÉVOQUE dans tous les cas (finally) : le lien magique ne laisse plus aucun
    membre api_key persistant. Bug corrigé le 08/10/2026 : chaque appel laissait
    un membre "magiclink…" dans le workspace (15 révoqués à la main le 07/10).
    Les restes d'anciens appels sont balayés au passage."""
    email = a.email or "robert@veridian.site"
    api_key = get_key(APIKEY_VAR, required=False)
    minted_email = None
    owner = None
    result = None
    try:
        if not api_key:
            owner = owner_jwt(a.env, a.workspace)
            try:
                swept = _sweep_stale_magiclink_keys(a.env, a.workspace, owner)
                if swept:
                    print(f"ℹ️  {swept} ancienne(s) clé(s) magiclink* balayée(s).", file=sys.stderr)
            except Exception as e:  # le balayage ne doit jamais empêcher le lien
                print(f"⚠️  balayage des anciennes clés magiclink* ignoré : {e}", file=sys.stderr)
            st, p = call_jwt(a.env, "POST", "/api/workspaces.createAPIKey", owner,
                             body={"workspace_id": a.workspace,
                                   "email_prefix": MAGICLINK_PREFIX + hashlib.sha256(
                                       f"{time.time_ns()}:{os.getpid()}".encode()
                                   ).hexdigest()[:10]})
            if st not in (200, 201):
                die(f"impossible de minter une api_key pour magic-link (HTTP {st}) : {p}")
            api_key = p.get("token") or p.get("api_key")
            minted_email = p.get("email")
            if not api_key:
                die("createAPIKey n'a pas renvoyé de token api_key.")
        result = call_jwt(a.env, "POST", "/api/workspaces.generateMagicLink", api_key,
                          body={"user_email": email})
    finally:
        if minted_email:
            try:
                ok, detail = _revoke_api_key_by_email(a.env, a.workspace, owner, minted_email)
            except BaseException as e:  # noqa: BLE001 — jamais masquer l'erreur d'origine
                ok, detail = False, f"{type(e).__name__}: {e}"
            if not ok:
                print(f"⚠️  clé éphémère {minted_email} NON révoquée ({detail}). "
                      f"À la main : notifuse-admin keys:revoke {a.workspace} --email {minted_email}",
                      file=sys.stderr)
    out(*result)


# ---- customEvents ----
def cmd_events_upsert(a):
    # UpsertCustomEventRequest = champs AU TOP-LEVEL (email, event_name,
    # external_id, properties…), PAS d'enveloppe {event:...}. external_id requis.
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "customEvents.upsert", a.workspace, body=body, method="POST"))


# ---- analytics / messages ----
def cmd_analytics_query(a):
    jwt = jwt_for_route(a.env, "analytics.query", a.workspace)
    body = {"workspace_id": a.workspace, "query": _json_arg(a.query)}
    out(*call_jwt(a.env, "POST", "/api/analytics.query", jwt, body=body))


def cmd_analytics_schemas(a):
    jwt = jwt_for_route(a.env, "analytics.schemas", a.workspace)
    out(*call_jwt(a.env, "POST", "/api/analytics.schemas", jwt,
                  body={"workspace_id": a.workspace}))


def cmd_messages_list(a):
    params = {"workspace_id": a.workspace}
    for kv in (a.param or []):
        k, _, v = kv.partition("=")
        params[k] = v
    out(*app_call(a.env, "messages.list", a.workspace, params=params, method="GET"))


# ---- integrations ----
def cmd_integrations_list(a):
    jwt = owner_jwt(a.env, a.workspace)
    _, wsd = call_jwt(a.env, "GET", "/api/workspaces.get", jwt, params={"id": a.workspace})
    w = wsd.get("workspace", wsd)
    integs = (w.get("integrations") or [])
    # surface la config cold par infra
    view = []
    for i in integs:
        prov = i.get("email_provider") or i.get("provider") or {}
        view.append({
            "id": i.get("id"), "name": i.get("name"), "type": i.get("type"),
            "kind": prov.get("kind"),
            "senders": [s.get("email") for s in (prov.get("senders") or [])],
            "rate_limit_per_minute": prov.get("rate_limit_per_minute"),
            "veridian_provider_class_rates": prov.get("veridian_provider_class_rates"),
            "veridian_provider_class_daily_cap": prov.get("veridian_provider_class_daily_cap"),
            "veridian_per_recipient_daily_cap": prov.get("veridian_per_recipient_daily_cap"),
            "veridian_tracking_domain": prov.get("veridian_tracking_domain"),
            "veridian_excluded_provider_classes": prov.get("veridian_excluded_provider_classes"),
        })
    print(json.dumps({"workspace": a.workspace, "integrations": view}, indent=2, ensure_ascii=False))


def _run_iac(cmd, env, extra=None):
    if not IAC_SCRIPT.exists():
        die(f"IAC introuvable : {IAC_SCRIPT} (repo notifuse non monté ?)")
    args = ["bash", str(IAC_SCRIPT), cmd, "--env", env]
    if extra:
        args += extra
    try:
        rc = subprocess.call(args)
    except Exception as e:
        die(f"IAC {cmd} a échoué : {e}")
    sys.exit(rc)


def cmd_integrations_plan(a):
    extra = ["--manifest", a.manifest] if a.manifest else None
    _run_iac("plan", a.env, extra)


def cmd_integrations_apply(a):
    extra = ["--manifest", a.manifest] if a.manifest else []
    if a.yes:
        extra.append("--yes")
    _run_iac("apply", a.env, extra)


def cmd_integrations_cold(a):
    """Pose la config cold (rates/caps/exclusion/window/pixel/tracking) sur une infra.

    updateIntegration renvoie le provider COMPLET sinon on écrase senders/rate :
    on lit l'intégration, on patche les champs cold, on renvoie le tout. OWNER-ONLY.
    """
    jwt = owner_jwt(a.env, a.workspace)
    _, wsd = call_jwt(a.env, "GET", "/api/workspaces.get", jwt, params={"id": a.workspace})
    w = wsd.get("workspace", wsd)
    integ = next((i for i in (w.get("integrations") or []) if i.get("id") == a.id), None)
    if not integ:
        die(f"intégration {a.id} introuvable dans {a.workspace}")
    prov = dict(integ.get("email_provider") or integ.get("provider") or {})
    if a.rates:
        prov["veridian_provider_class_rates"] = json.loads(a.rates)
    if a.daily_cap:
        prov["veridian_provider_class_daily_cap"] = json.loads(a.daily_cap)
    if a.per_recipient_cap is not None:
        prov["veridian_per_recipient_daily_cap"] = a.per_recipient_cap
    if a.exclude:
        prov["veridian_excluded_provider_classes"] = [c.strip() for c in a.exclude.split(",") if c.strip()]
    if a.tracking_domain:
        prov["veridian_tracking_domain"] = a.tracking_domain
    if a.pixel:
        prov["veridian_open_pixel_by_class"] = json.loads(a.pixel)
    thr = getattr(a, "bounce_freeze_threshold", None)
    if thr is not None:
        # Seuil du fusible de réputation (bounce dur 7 j) PAR PROFIL. 0 = retour au défaut 0.03.
        if thr != 0 and not (0.01 <= thr <= 0.15):
            die("--bounce-freeze-threshold doit être 0 (défaut 0.03) ou entre 0.01 et 0.15 (proportion : 0.08 = 8 %).")
        if thr == 0:
            prov.pop("veridian_hard_bounce_freeze_threshold", None)
        else:
            prov["veridian_hard_bounce_freeze_threshold"] = thr
    body = {"workspace_id": a.workspace, "integration_id": a.id,
            "name": integ.get("name"), "provider": prov}
    out(*call_jwt(a.env, "POST", "/api/workspaces.updateIntegration", jwt, body=body))


def cmd_integrations_create_smtp(a):
    jwt = owner_jwt(a.env, a.workspace)
    body = {
        "workspace_id": a.workspace, "name": a.name, "type": "email",
        "provider": {
            "kind": "smtp",
            "rate_limit_per_minute": a.rate or 60,
            # Un sender SANS id est inréférençable : aucun template ne peut le cibler et
            # tout envoi echoue ("Failed to send"). Le serveur ne comble pas l'id manquant
            # quand les senders arrivent bruts par l'API — c'est au client de le fournir.
            "senders": [{"id": str(uuid.uuid4()), "email": a.from_email,
                         "name": a.from_name or a.from_email, "is_default": True}],
            "smtp": {"host": a.host, "port": a.port, "username": a.user,
                     "password": secret_from_args(a), "use_tls": not a.no_tls},
        },
    }
    out(*call_jwt(a.env, "POST", "/api/workspaces.createIntegration", jwt, body=body))


def cmd_integrations_create_imap(a):
    jwt = owner_jwt(a.env, a.workspace)
    body = {
        "workspace_id": a.workspace, "name": a.name, "type": "imap",
        "imap_settings": {
            "host": a.host, "port": a.port, "use_tls": not a.no_tls,
            "username": a.user, "password": secret_from_args(a),
            "folder": a.folder or "INBOX",
            "polling_interval_seconds": a.interval or 60,
        },
    }
    if getattr(a, "tls_server_name", None):
        body["imap_settings"]["tls_server_name"] = a.tls_server_name
    out(*call_jwt(a.env, "POST", "/api/workspaces.createIntegration", jwt, body=body))


def cmd_integrations_create_supabase(a):
    """Crée une intégration Supabase (auth email hook + before-user-created hook).

    ⚠️ Le type Supabase de Notifuse N'EST PAS url+service_role_key : c'est le pont
    des hooks d'auth Supabase (Send Email Hook, Before User Created Hook). On
    configure les SIGNATURE KEYS de ces hooks (chiffrées au repos). Tous les champs
    sont optionnels côté validation (clés posées plus tard côté UI Supabase).
    """
    jwt = owner_jwt(a.env, a.workspace)
    settings = {"auth_email_hook": {}, "before_user_created_hook": {}}
    if a.email_hook_key:
        settings["auth_email_hook"]["signature_key"] = a.email_hook_key
    bu = settings["before_user_created_hook"]
    if a.user_hook_key:
        bu["signature_key"] = a.user_hook_key
    if a.add_to_lists:
        bu["add_user_to_lists"] = [l.strip() for l in a.add_to_lists.split(",") if l.strip()]
    if a.custom_json_field:
        bu["custom_json_field"] = a.custom_json_field
    if a.reject_disposable:
        bu["reject_disposable_email"] = True
    body = {"workspace_id": a.workspace, "name": a.name, "type": "supabase",
            "supabase_settings": settings}
    out(*call_jwt(a.env, "POST", "/api/workspaces.createIntegration", jwt, body=body))


def cmd_integrations_create_firecrawl(a):
    jwt = owner_jwt(a.env, a.workspace)
    fc = {"api_key": a.api_key}
    if a.base_url:
        fc["base_url"] = a.base_url
    body = {"workspace_id": a.workspace, "name": a.name, "type": "firecrawl",
            "firecrawl_settings": fc}
    out(*call_jwt(a.env, "POST", "/api/workspaces.createIntegration", jwt, body=body))


def cmd_integrations_create_llm(a):
    """Crée une intégration LLM (anthropic|openai). model REQUIS par l'API."""
    jwt = owner_jwt(a.env, a.workspace)
    kind = a.provider
    if kind not in ("anthropic", "openai"):
        die("--provider doit être 'anthropic' ou 'openai'.")
    default_model = "claude-sonnet-4-20250514" if kind == "anthropic" else "gpt-4o"
    sub = {"api_key": a.api_key, "model": a.model or default_model}
    if kind == "openai" and a.base_url:
        sub["base_url"] = a.base_url
    llm = {"kind": kind, kind: sub}
    body = {"workspace_id": a.workspace, "name": a.name, "type": "llm",
            "llm_provider": llm}
    out(*call_jwt(a.env, "POST", "/api/workspaces.createIntegration", jwt, body=body))


def _find_integration(env, workspace, integration_id, jwt=None):
    jwt = jwt or owner_jwt(env, workspace)
    _, wsd = call_jwt(env, "GET", "/api/workspaces.get", jwt, params={"id": workspace})
    w = wsd.get("workspace", wsd)
    integ = next((i for i in (w.get("integrations") or []) if i.get("id") == integration_id), None)
    return integ, jwt


def cmd_integrations_get(a):
    integ, _ = _find_integration(a.env, a.workspace, a.id)
    if not integ:
        die(f"intégration {a.id} introuvable dans {a.workspace}")
    print(json.dumps(integ, indent=2, ensure_ascii=False))


def cmd_integrations_delete(a):
    jwt = owner_jwt(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "integration_id": a.id}
    out(*call_jwt(a.env, "POST", "/api/workspaces.deleteIntegration", jwt, body=body))


def cmd_integrations_update(a):
    """Update générique d'une intégration par type.

    ⚠️ Deux niveaux de validation côté serveur :
      1. UpdateIntegrationRequest.Validate (AVANT la préservation) — exige par ex.
         que firecrawl ait api_key OU encrypted_api_key DANS LA REQUÊTE.
      2. service.UpdateIntegration — préserve l'encrypted existant si le clair n'est
         pas re-fourni (Supabase/LLM/Firecrawl/IMAP).
    Conséquence : pour changer un champ NON-secret sans toucher la clé, il faut
    quand même renvoyer l'encrypted_* existant (sinon validate #1 rejette). On part
    donc des settings EXISTANTS comme base, et le clair fourni en flag override.
    --settings @file.json passe le bloc type-spécifique brut (échappatoire). OWNER-ONLY.
    """
    integ, jwt = _find_integration(a.env, a.workspace, a.id)
    if not integ:
        die(f"intégration {a.id} introuvable dans {a.workspace}")
    itype = integ.get("type")
    body = {"workspace_id": a.workspace, "integration_id": a.id,
            "name": a.name or integ.get("name")}
    raw = _json_arg(a.settings) if a.settings else None
    if itype == "email":
        prov = dict(integ.get("email_provider") or {})
        if raw:
            prov.update(raw)
        body["provider"] = prov
    elif itype == "supabase":
        # base = settings existants (porte les encrypted_signature_key) ; le clair override.
        settings = raw or dict(integ.get("supabase_settings") or
                               {"auth_email_hook": {}, "before_user_created_hook": {}})
        if a.email_hook_key:
            settings.setdefault("auth_email_hook", {})["signature_key"] = a.email_hook_key
        if a.user_hook_key:
            settings.setdefault("before_user_created_hook", {})["signature_key"] = a.user_hook_key
        body["supabase_settings"] = settings
    elif itype == "firecrawl":
        fc = raw or dict(integ.get("firecrawl_settings") or {})
        fc.pop("api_key", None)  # ne JAMAIS renvoyer le clair décrypté tel quel
        if a.api_key:
            fc["api_key"] = a.api_key
        if a.base_url:
            fc["base_url"] = a.base_url
        body["firecrawl_settings"] = fc
    elif itype == "llm":
        if raw:
            body["llm_provider"] = raw
        else:
            kind = a.provider or (integ.get("llm_provider") or {}).get("kind")
            if not kind:
                die("--provider requis (ou --settings @file.json) pour update LLM.")
            existing_sub = dict((integ.get("llm_provider") or {}).get(kind) or {})
            existing_sub.pop("api_key", None)  # garde encrypted_api_key, pas le clair
            sub = existing_sub
            sub["model"] = a.model or existing_sub.get("model")
            if a.api_key:
                sub["api_key"] = a.api_key
            if kind == "openai" and a.base_url:
                sub["base_url"] = a.base_url
            body["llm_provider"] = {"kind": kind, kind: sub}
    elif itype == "imap":
        imap = raw or dict(integ.get("imap_settings") or {})
        imap.pop("password", None)
        body["imap_settings"] = imap
    else:
        die(f"type d'intégration inconnu/non géré : {itype}. Utilise --settings @file.json.")
    out(*call_jwt(a.env, "POST", "/api/workspaces.updateIntegration", jwt, body=body))


# ---- profils d'envoi : vérité (lot 2, 08/10/2026) ----
def _fmt_classes_line(c):
    parts = [f"{c['class']:<18}"]
    if c.get("excluded"):
        parts.append("EXCLUE")
    if c.get("stopped"):
        parts.append("ARRÊTÉE (le fournisseur refuse en bloc)")
    if c.get("slowdown_factor", 1) > 1:
        parts.append(f"ralentie ÷{c['slowdown_factor']} ({c.get('slowdown_reason') or '?'})")
    if c.get("rate_configured"):
        parts.append(f"débit {c['rate_per_min']:g}/min" + (f" (configuré {c['rate_configured']:g})" if c['rate_per_min'] != c['rate_configured'] else ""))
    if c.get("daily_cap") is not None:
        cap = c["daily_cap"]
        base = c.get("daily_cap_configured")
        parts.append(f"plafond {cap}" + (f" (configuré {base})" if base != cap else "")
                     + f", envoyés {c.get('sent_today', 0)}"
                     + (f", reste {c['remaining']}" if c.get("remaining") is not None else ""))
    elif c.get("sent_today"):
        parts.append(f"envoyés {c['sent_today']}")
    if not c.get("sendable_now") and c.get("blocked_by") and not c.get("excluded") and not c.get("stopped"):
        parts.append(f"BLOQUÉE : {c['blocked_by']}")
    return "    " + " · ".join(parts)


def _class_is_notable(c):
    return bool(c.get("excluded") or c.get("stopped") or c.get("slowdown_factor", 1) > 1
                or c.get("rate_configured") or c.get("daily_cap") is not None or c.get("sent_today"))


BLOCK_LABELS = {
    "paused": "profil en pause", "not_in_rotation": "hors rotation", "unverified": "transport non vérifié",
    "window_closed": "fenêtre d'envoi fermée", "excluded_class": "classe exclue",
    "reputation_stopped": "fournisseur arrête (refus en bloc)", "warmup": "plafond de chauffe atteint",
    "profile_cap": "plafond du profil atteint", "per_sender": "plafond par expéditeur atteint",
    "class_cap": "plafond de la classe atteint",
}
GATE_LABELS = {"warmup": "chauffe", "profile_cap": "plafond du profil", "per_sender": "plafond par expéditeur",
               "class_cap": "plafonds par classe", "none": "aucune"}


def format_profiles_overview(d):
    """Rendu lisible de /api/veridian/emailProfiles.overview (fonction pure, testée)."""
    L = []
    t = d.get("totals") or {}
    cap = t.get("commercial_capacity_today")
    L.append(f"Profils d'envoi, jour {d.get('date')} (UTC), fuseau {d.get('timezone') or 'non défini'}")
    L.append(f"  commercial : {t.get('commercial_sent_today', 0)} envoyés"
             + (f" sur {cap} de capacité" if cap is not None else " (capacité totale inconnue : un profil actif n'a aucun plafond)")
             + f", {t.get('active_commercial_profiles', 0)} profil(s) actif(s), {t.get('paused_profiles', 0)} en pause"
             + f" · transactionnel : {t.get('transactional_sent_today', 0)} envoyés")
    for c in d.get("usage_conflicts") or []:
        L.append(f"  ⚠️  VIOLATION : le profil {c} est à la fois dans la rotation commerciale ET transactionnel")
    for p in d.get("profiles") or []:
        pl = p.get("plan") or {}
        usage = {"commercial": "commercial", "transactional": "transactionnel"}.get(p.get("usage"), "non assigné")
        flags = [usage, p.get("type") or p.get("kind")]
        if p.get("usage") == "commercial":
            flags.append("en rotation" if p.get("in_rotation") else "HORS rotation")
        flags.append(f"vérifié {str(p.get('verified_at'))[:10]}" if p.get("verified") else "NON vérifié")
        if p.get("paused"):
            flags.append("EN PAUSE")
        L.append("")
        L.append(f"● {p.get('name')}  [{' · '.join(str(x) for x in flags)}]  id {p.get('integration_id')}")
        senders = ", ".join(x["email"] + (" (défaut)" if x.get("is_default") else "") for x in p.get("senders") or [])
        L.append(f"    expéditeurs : {senders or 'aucun'}")
        ib = p.get("return_inbox")
        L.append("    IMAP lié    : " + (f"{ib['name']} ({ib['address']}, id {ib['integration_id']})"
                                          + (f", partagé avec {len(ib['linked_profiles']) - 1} autre(s) profil(s)" if len(ib.get('linked_profiles') or []) > 1 else "")
                                          if ib else "aucun (retours détectés par les boîtes globales du workspace)"))
        if not pl.get("applicable"):
            L.append(f"    envoyés aujourd'hui : {pl.get('sent_today', 0)}"
                     + ("  (transactionnel : aucune porte commerciale, jamais plafonné par le worker)" if p.get("usage") == "transactional" else ""))
            continue
        dc = pl.get("daily_cap_today")
        L.append("    plafond du jour : " + (f"{dc}  (porte limitante : {GATE_LABELS.get(pl.get('limiting_gate'), pl.get('limiting_gate'))}"
                                            + (f", {pl['limiting_detail']}" if pl.get("limiting_detail") else "") + ")"
                                            if dc is not None else "aucun plafond configuré"))
        rem = pl.get("remaining_today")
        L.append(f"    envoyés {pl.get('sent_today', 0)} · réservés {pl.get('reserved_today', 0)}"
                 + (f" · reste {rem} (porte la plus proche : {GATE_LABELS.get(pl.get('remaining_gate'), pl.get('remaining_gate'))})" if rem is not None else ""))
        for g in pl.get("gates") or []:
            L.append(f"      - {GATE_LABELS.get(g['name'], g['name'])}: {g['used']}/{g['cap']} ({g.get('detail', '')})")
        w = pl.get("window") or {}
        if w.get("configured"):
            days = ",".join(str(x) for x in (w.get("days") or []))
            L.append(f"    fenêtre : {'ouverte' if w.get('open_now') else 'FERMÉE'} (jours {days}, {w.get('start_hour')}h-{w.get('end_hour')}h {w.get('timezone')}, source {w.get('source')})"
                     + ("" if w.get("open_now") else f", réouverture {w.get('next_open_at')}"))
        else:
            L.append("    fenêtre : aucune (envoi 24/7)")
        wu = pl.get("warmup") or {}
        if wu.get("active"):
            L.append(f"    chauffe : jour {wu.get('day')}/{wu.get('of')}, plafond {wu.get('cap_today')}")
        if pl.get("excluded_classes"):
            L.append(f"    classes exclues : {', '.join(pl['excluded_classes'])}")
        if pl.get("complaints_7d"):
            L.append(f"    fusible : {pl['complaints_7d']} plainte(s) sur 7 j, domaine ralenti ÷{pl.get('domain_slowdown_factor')} (jamais arrêté)")
        L.append(f"    origine des tables : plafonds par classe = {pl.get('class_caps_source')}, débits par classe = {pl.get('class_rates_source')}; "
                 f"cadence technique {pl.get('native_rate_per_min')}/min")
        notable = [c for c in pl.get("classes") or [] if _class_is_notable(c)]
        if notable:
            L.append("    par classe :")
            L.extend(_fmt_classes_line(c) for c in notable)
        if pl.get("sendable_now"):
            ok = sum(1 for c in pl.get("classes") or [] if c.get("sendable_now"))
            L.append(f"    ▶ envoie maintenant : oui ({ok} classe(s) ouvertes)")
        else:
            why = ", ".join(BLOCK_LABELS.get(b, b) for b in pl.get("blocked_by") or []) or "toutes les classes bloquées"
            L.append(f"    ▶ envoie maintenant : NON ({why})")
    gi = d.get("global_inboxes") or []
    if gi:
        L.append("")
        L.append("Boîtes de retour globales du workspace (liées à aucun profil) :")
        for ib in gi:
            L.append(f"  - {ib['name']} ({ib['address']}, id {ib['integration_id']})")
    return "\n".join(L)


def _fetch_overview(a):
    jwt = apikey_for(a.env, a.workspace)
    return call_jwt(a.env, "GET", "/api/veridian/emailProfiles.overview", jwt, params={"workspace_id": a.workspace})


def cmd_profiles_overview(a):
    st, data = _fetch_overview(a)
    if st != 200 or not isinstance(data, dict):
        out(st, data)
        return
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    else:
        print(format_profiles_overview(data))


def _patch_email_profile(a, mutate):
    """Lit l'intégration email, applique mutate(provider), renvoie le provider COMPLET
    (updateIntegration remplace tout, comme integrations:cold). OWNER-ONLY."""
    jwt = owner_jwt(a.env, a.workspace)
    _, wsd = call_jwt(a.env, "GET", "/api/workspaces.get", jwt, params={"id": a.workspace})
    w = wsd.get("workspace", wsd)
    integ = next((i for i in (w.get("integrations") or []) if i.get("id") == a.id), None)
    if not integ:
        die(f"intégration {a.id} introuvable dans {a.workspace}")
    if integ.get("type") != "email":
        die(f"{a.id} n'est pas un profil d'envoi (type {integ.get('type')}).")
    prov = dict(integ.get("email_provider") or integ.get("provider") or {})
    mutate(prov, w)
    body = {"workspace_id": a.workspace, "integration_id": a.id, "name": integ.get("name"), "provider": prov}
    out(*call_jwt(a.env, "POST", "/api/workspaces.updateIntegration", jwt, body=body))


def cmd_profiles_link_imap(a):
    if bool(a.imap) == bool(a.none):
        die("donne --imap <id de l'intégration IMAP> OU --none pour retirer le lien.")

    def mutate(prov, w):
        if a.none:
            prov.pop("veridian_return_imap_integration_id", None)
            return
        target = next((i for i in (w.get("integrations") or []) if i.get("id") == a.imap), None)
        if not target or target.get("type") != "imap":
            die(f"{a.imap} n'est pas une intégration IMAP de {a.workspace} (le serveur refuserait en 400).")
        prov["veridian_return_imap_integration_id"] = a.imap
    _patch_email_profile(a, mutate)


def _profile_admin_call(a, route, extra=None):
    """Lot 4 : usage et pause passent par l'API dédiée (une écriture, règles côté
    serveur), plus par updateIntegration. Droit requis : workspace:write."""
    jwt = apikey_for(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "integration_id": a.id}
    body.update(extra or {})
    out(*call_jwt(a.env, "POST", route, jwt, body=body))


def cmd_profiles_set_usage(a):
    _profile_admin_call(a, "/api/veridian/emailProfiles.setUsage", {"usage": a.usage})


def cmd_profiles_pause(a):
    _profile_admin_call(a, "/api/veridian/emailProfiles.pause")


def cmd_profiles_resume(a):
    _profile_admin_call(a, "/api/veridian/emailProfiles.resume")


GMAIL_APP_PASSWORD_URL = "https://myaccount.google.com/apppasswords"


def read_secret_input(secret_file=None, from_stdin=False, label="secret"):
    """Lit un secret sur stdin ou dans un fichier. Jamais en argument de ligne de commande
    (visible dans `ps` et l'historique du shell). Retire seulement le saut de ligne final.
    Sur un terminal, la saisie est masquée (getpass). Le secret n'est ni affiché ni loggé."""
    if secret_file and from_stdin:
        die(f"{label} : --secret-stdin et --secret-file sont exclusifs.")
    if secret_file:
        path = Path(secret_file).expanduser()
        try:
            if os.name == "posix" and path.stat().st_mode & 0o077:
                print(f"⚠ {path} est lisible par d'autres utilisateurs (chmod 600 recommandé).", file=sys.stderr)
            value = path.read_text(encoding="utf-8")
        except OSError as exc:
            die(f"{label} : fichier illisible ({exc.strerror or 'erreur'}).")
    elif from_stdin:
        if sys.stdin.isatty():
            value = getpass.getpass(f"{label} (saisie masquée) : ")
        else:
            value = sys.stdin.read()
    else:
        die(f"{label} : --secret-stdin ou --secret-file est requis (jamais en argument).")
    value = value.rstrip("\r\n")
    if not value:
        die(f"{label} vide.")
    return value


def redact_secrets(payload, secrets):
    """Masque toute occurrence d'un secret dans une réponse avant affichage."""
    text = json.dumps(payload, ensure_ascii=False)
    for secret in secrets:
        if secret:
            text = text.replace(json.dumps(secret, ensure_ascii=False)[1:-1], "***")
    return json.loads(text)


def build_profile_create_body(a, secret, imap_secret=None):
    """Corps de POST /api/veridian/emailProfiles.create. Pur : testable sans réseau."""
    if a.type == "gmail-app-password":
        body = {
            "workspace_id": a.workspace, "type": "gmail_app_password", "name": a.name,
            "sender_email": a.from_email, "sender_name": a.from_name or a.from_email,
            # Google affiche le mot de passe d'application en quatre groupes séparés par des espaces.
            "app_password": "".join(secret.split()),
            "gmail_account_type": a.account_type or "personal",
        }
        if a.daily_cap:
            body["profile_daily_cap"] = a.daily_cap
        return body
    if not (a.host and a.user):
        die("--type smtp exige --host et --user (et --port, 587 par défaut).")
    body = {
        "workspace_id": a.workspace, "type": "smtp_imap", "name": a.name,
        "sender_email": a.from_email, "sender_name": a.from_name or a.from_email,
        "smtp": {"host": a.host, "port": a.port or 587, "use_tls": not a.no_tls,
                 "username": a.user, "password": secret},
    }
    if a.imap_host:
        body["imap"] = {"host": a.imap_host, "port": a.imap_port or 993, "use_tls": True,
                        "username": a.imap_user or a.user,
                        "password": imap_secret if imap_secret else secret,
                        "folder": a.imap_folder or "INBOX"}
    return body


def cmd_profiles_create(a):
    """Crée un profil d'envoi (et sa boîte IMAP de retour) en une seule écriture serveur."""
    secret = read_secret_input(a.secret_file, a.secret_stdin, "secret du profil")
    imap_secret = None
    if getattr(a, "imap_secret_file", None):
        imap_secret = read_secret_input(a.imap_secret_file, False, "secret IMAP")
    body = build_profile_create_body(a, secret, imap_secret)
    secrets = [secret, imap_secret, body.get("app_password")]
    if a.dry_run:
        print(json.dumps(redact_secrets(body, secrets), indent=2, ensure_ascii=False))
        return
    jwt = owner_jwt(a.env, a.workspace)
    status, payload = call_jwt(a.env, "POST", "/api/veridian/emailProfiles.create", jwt, body=body)
    out(status, redact_secrets(payload, secrets))


def cmd_prospection_stats(a):
    jwt = apikey_for(a.env, a.workspace)
    out(*call_jwt(a.env, "GET", "/api/veridian/prospection.stats", jwt,
                  params={"workspace_id": a.workspace, "start": a.start, "end": a.end}))


# ---- Lot 1 (10/10/2026) : « pourquoi ce mail n'est pas parti » -------------
# queue:explain  = GET  /api/veridian/queue.explain    (automations:read)
# logs:decisions = GET  /api/veridian/decisions.list   (automations:read)
# queue:recompute = POST /api/veridian/queue.recompute (automations:write, --yes requis)
_OUTCOMES = ("sent", "deferred", "failed", "discarded", "exited", "recomputed")
_GROUP_BY = ("automation", "node", "reason", "profile", "class")
_SINCE_DURATION = re.compile(r"^\d+[smhd]$")


def parse_since(value):
    """--since : durée (30m, 2h, 7d) ou date RFC3339. Renvoie la valeur validée, telle quelle
    pour le serveur (qui accepte les deux). Une valeur incompréhensible = erreur 1, pas d'appel."""
    if value is None:
        return None
    v = value.strip()
    if _SINCE_DURATION.match(v):
        return v
    try:
        datetime.fromisoformat(v.replace("Z", "+00:00"))
        return v
    except ValueError:
        die(f"--since '{value}' invalide : durée (30m, 2h, 7d) ou date RFC3339 (2026-10-10T08:00:00Z).")


def _ts(v):
    """Horodatage ISO -> 'AAAA-MM-JJ HH:MM' (UTC). Vide -> '-'."""
    if not v:
        return "-"
    return str(v).replace("T", " ")[:16]


def _fmt_delay(sec):
    if sec in (None, ""):
        return "-"
    try:
        s = int(sec)
    except (TypeError, ValueError):
        return str(sec)
    if s >= 86400:
        return f"{s // 86400}j{(s % 86400) // 3600:02d}h"
    if s >= 3600:
        return f"{s // 3600}h{(s % 3600) // 60:02d}"
    if s >= 60:
        return f"{s // 60}min"
    return f"{s}s"


def _j(v):
    return "-" if v is None else (v if isinstance(v, str) else json.dumps(v, ensure_ascii=False))


def _table(rows, header):
    widths = [max(len(str(x)) for x in col) for col in zip(header, *rows)] if rows else [len(h) for h in header]
    fmt = lambda r: "  ".join(str(c).ljust(w) for c, w in zip(r, widths)).rstrip()
    return [fmt(header)] + [fmt(r) for r in rows]


def format_trace(trace):
    """Trace de décision, un gate par ligne : gate  verdict  valeur  limite  délai (fonction pure, testée)."""
    if not trace:
        return ["(aucune trace : décision hors échantillon ou niveau réduit)"]
    L = [f"classe {trace.get('class') or '-'} · niveau {trace.get('level') or '-'}"]
    anc = trace.get("anchor")
    if anc:
        L.append(f"ancre : profil {anc.get('profile')} ({'disponible' if anc.get('available') else 'indisponible'})")
    for c in trace.get("candidates") or []:
        L.append(f"profil {c.get('profile_name') or c.get('profile')} <{c.get('from') or '-'}> : {c.get('outcome')}")
        rows = []
        for g in c.get("gates") or []:
            name = g.get("gate", "?") + (f"({g['name']})" if g.get("name") else "")
            rows.append((name, g.get("verdict", "-"), _j(g.get("value")), _j(g.get("limit")), _fmt_delay(g.get("delay_s"))))
        if rows:
            L += ["  " + ln for ln in _table(rows, ("gate", "verdict", "valeur", "limite", "délai"))]
    d = trace.get("decision") or {}
    if d:
        L.append(f"décision : {d.get('outcome')} {d.get('reason') or ''}"
                 + (f" ({d['detail']})" if d.get("detail") else "")
                 + (f" jusqu'à {_ts(d['until'])} (délai {_fmt_delay(d.get('delay_s'))})" if d.get("until") else ""))
    return L


def format_queue_groups(data):
    """Tableau groupé de queue.explain (fonction pure, testée)."""
    L = [f"{data.get('total', 0)} entrée(s) en file · workspace {data.get('workspace_id')} · {_ts(data.get('generated_at'))} UTC"]
    rows = []
    for g in data.get("groups") or []:
        reason = g.get("reason") or "-"
        if g.get("reason_detail"):
            reason += f"/{g['reason_detail']}"
        nxt = "-"
        if g.get("next_attempt_min"):
            nxt = _ts(g["next_attempt_min"])
            if g.get("next_attempt_max") and g["next_attempt_max"] != g["next_attempt_min"]:
                nxt += " .. " + _ts(g["next_attempt_max"])
        rows.append((g.get("automation_name") or g.get("automation_id") or "-", g.get("node_id") or "-", reason,
                     g.get("profile_name") or g.get("profile_id") or "-", g.get("class") or "-",
                     g.get("count", 0), g.get("never_examined", 0), _ts(g.get("oldest_created_at")), nxt))
    if rows:
        L += _table(rows, ("automation", "nœud", "raison", "profil", "classe", "count", "jamais examinés", "plus ancienne", "prochaine tentative"))
    else:
        L.append("(aucun groupe)")
    o = data.get("orphans") or {}
    L.append(f"orphelins : {o.get('count', 0)}"
             + "".join(f" · {b.get('automation_name') or b.get('automation_id')}/{b.get('node_id') or '-'} : {b.get('count')}"
                       for b in o.get("by_node") or []))
    return L


def format_queue_entry(e, decision=None):
    """Détail d'une entrée + dernière décision gate par gate (fonction pure, testée)."""
    L = [f"entrée {e.get('id')} · {e.get('status')} · {e.get('contact_email') or '-'}",
         f"  automation {e.get('automation_name') or e.get('automation_id')} · nœud {e.get('node_id') or '-'} · "
         f"profil {e.get('profile_name') or '-'} · classe {e.get('class') or '-'}",
         f"  créée {_ts(e.get('created_at'))} · tentatives {e.get('attempts')}/{e.get('max_attempts')} · prochaine {_ts(e.get('next_retry_at'))}",
         f"  raison {e.get('reason') or '-'}" + (f" ({e['reason_detail']})" if e.get("reason_detail") else "")
         + f" · reportée {_ts(e.get('deferred_at'))} jusqu'à {_ts(e.get('defer_until'))} ({e.get('defer_count') or 0} report(s))",
         f"  examinée la 1re fois {_ts(e.get('first_examined_at'))}, la dernière {_ts(e.get('last_examined_at'))}"]
    if e.get("last_error"):
        L.append(f"  dernière erreur : {e['last_error']}")
    if decision:
        L.append(f"dernière décision {_ts(decision.get('at'))} : {decision.get('outcome')} {decision.get('reason') or ''}"
                 + (f" ({decision['detail']})" if decision.get("detail") else ""))
        L += ["  " + ln for ln in format_trace(decision.get("trace"))]
    else:
        L.append("dernière décision : aucune enregistrée")
    return L


def cmd_queue_explain(a):
    jwt = apikey_for(a.env, a.workspace)
    group_by = None
    if a.group_by:
        parts = [p.strip() for p in a.group_by.split(",") if p.strip()]
        bad = [p for p in parts if p not in _GROUP_BY]
        if bad:
            die(f"--group-by : valeur(s) inconnue(s) {', '.join(bad)} (permis : {', '.join(_GROUP_BY)}).")
        group_by = ",".join(parts)
    params = {"workspace_id": a.workspace, "group_by": group_by, "automation_id": a.automation,
              "node_id": a.node, "reason": a.reason, "profile_id": a.profile, "class": a.klass,
              "status": a.status, "entry_id": a.entry}
    st, data = call_jwt(a.env, "GET", "/api/veridian/queue.explain", jwt, params=params)
    if st != 200 or not isinstance(data, dict):
        out(st, data)
        return
    entry = data.get("entry")
    decision = None
    if a.entry and entry:
        decision = entry.get("last_decision")
        if not decision or not decision.get("trace"):
            st2, d2 = call_jwt(a.env, "GET", "/api/veridian/decisions.list", jwt,
                               params={"workspace_id": a.workspace, "entry_id": a.entry, "trace": "1", "limit": 1})
            if st2 != 200:
                out(st2, d2)
                return
            found = (d2 or {}).get("decisions") or []
            decision = found[0] if found else decision
            entry["last_decision"] = decision
    if a.json:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    elif a.entry:
        if not entry:
            die(f"entrée {a.entry} introuvable dans {a.workspace}.")
        print("\n".join(format_queue_entry(entry, decision)))
    else:
        print("\n".join(format_queue_groups(data)))


def format_decisions(decisions, trace=False):
    L = []
    for d in decisions:
        reason = d.get("reason") or ""
        if d.get("detail"):
            reason += f" ({d['detail']})"
        L.append(f"{_ts(d.get('at'))}  {d.get('outcome', '-'):<10} {reason or '-'}  {d.get('contact_email') or '-'}  "
                 f"{d.get('automation_id') or '-'}/{d.get('node_id') or '-'}  profil {d.get('profile_name') or d.get('profile_id') or '-'}"
                 + (f"  jusqu'à {_ts(d['until'])}" if d.get("until") else "")
                 + f"  entrée {d.get('entry_id') or '-'}")
        if trace:
            L += ["    " + ln for ln in format_trace(d.get("trace"))]
    return L


def cmd_logs_decisions(a):
    jwt = apikey_for(a.env, a.workspace)
    since = parse_since(a.since)
    params = {"workspace_id": a.workspace, "email": a.email, "automation_id": a.automation, "node_id": a.node,
              "entry_id": a.entry, "reason": a.reason, "outcome": a.outcome, "since": since,
              "limit": a.limit, "trace": "1" if a.trace else None}
    decisions, cursor, level, pages = [], None, None, 0
    max_pages = 5 if a.all else 1
    while pages < max_pages:
        p = dict(params)
        if cursor:
            p["cursor"] = cursor
        st, data = call_jwt(a.env, "GET", "/api/veridian/decisions.list", jwt, params=p)
        if st != 200 or not isinstance(data, dict):
            out(st, data)
            return
        pages += 1
        decisions += data.get("decisions") or []
        level = data.get("level", level)
        cursor = data.get("next_cursor") or None
        if not cursor:
            break
    if a.json:
        print(json.dumps({"decisions": decisions, "next_cursor": cursor or "", "level": level}, indent=2, ensure_ascii=False))
        return
    print("\n".join(format_decisions(decisions, a.trace)) if decisions else "(aucune décision)")
    if cursor:
        print(f"… suite disponible : next_cursor={cursor}"
              + (" (limite de 5 pages atteinte)" if a.all else " (relance avec --all pour suivre jusqu'à 5 pages)"),
              file=sys.stderr)


def cmd_queue_recompute(a):
    if not a.automation and not a.entry:
        die("queue:recompute exige --automation X ou --entry ID (jamais « tout le workspace »).")
    if not 1 <= a.limit <= 5000:
        die("--limit doit être compris entre 1 et 5000.")
    body = {"workspace_id": a.workspace, "automation_id": a.automation, "node_id": a.node, "reason": a.reason,
            "profile_id": a.profile, "entry_ids": a.entry or None, "limit": a.limit}
    body = {k: v for k, v in body.items() if v is not None}
    if not a.yes:
        print("DRY-RUN (rien n'est envoyé) : POST /api/veridian/queue.recompute")
        print(json.dumps(body, indent=2, ensure_ascii=False))
        print("Effet : next_retry_at remis à NULL et raison effacée sur les entrées pending/failed du filtre ; "
              "rien n'est supprimé, aucune tentative consommée. Ajoute --yes pour l'exécuter.", file=sys.stderr)
        return
    jwt = apikey_for(a.env, a.workspace)
    out(*call_jwt(a.env, "POST", "/api/veridian/queue.recompute", jwt, body=body))


# ---- generic resource CRUD helpers ----
def _json_arg(s):
    if not s:
        return None
    try:
        if s.startswith("@"):
            return json.loads(Path(s[1:]).read_text())
        return json.loads(s)
    except FileNotFoundError:
        die(f"fichier JSON introuvable : {s[1:]}")
    except json.JSONDecodeError as e:
        die(f"JSON invalide ({'fichier '+s[1:] if s.startswith('@') else 'inline'}) : {e}")


# ---- settings (workspace) ----
# Champs de WorkspaceSettings RÉELLEMENT persistés par l'allowlist UpdateWorkspace
# (internal/service/workspace_service.go:347-412). Un champ hors de cette liste est
# silencieusement droppé côté serveur → on N'EXPOSE QUE ceux-là (anti-piège pixel
# staging 2026-06-11). Valeur = type Python attendu pour le coercion de settings:set.
SETTINGS_KEYS = {
    "website_url": str, "logo_url": str, "cover_url": str, "timezone": str,
    "file_manager": "json", "transactional_email_provider_id": str,
    "marketing_email_provider_id": str, "email_tracking_enabled": bool,
    "custom_endpoint_url": str, "custom_field_labels": "json",
    "blog_enabled": bool, "blog_settings": "json",
    "default_language": str, "languages": "json", "template_blocks": "json",
    # Veridian cold outreach (fallback workspace de la cascade) :
    "veridian_provider_class_rates": "json", "veridian_open_pixel_by_class": "json",
    "veridian_provider_class_daily_cap": "json", "veridian_per_recipient_daily_cap": int,
    "veridian_per_sender_daily_cap": int, "veridian_sending_window": "json",
    "veridian_jitter_pct": float, "veridian_anti_hash_enabled": bool,
    "veridian_anti_hash_window_hours": int, "veridian_excluded_provider_classes": "json", "veridian_marketing_email_provider_ids": "json",
}


def _coerce_setting(key, value):
    """Coerce une valeur string CLI vers le type attendu par WorkspaceSettings."""
    t = SETTINGS_KEYS.get(key)
    if t is None:
        die(f"clé settings inconnue/non éditable : {key}. "
            f"Éditables : {', '.join(sorted(SETTINGS_KEYS))}")
    if t == "json":
        try:
            return json.loads(value)
        except Exception:
            die(f"valeur de '{key}' attendue en JSON (ex '[\"fr\"]' ou '{{...}}'). Reçu : {value}")
    if t is bool:
        return value.lower() in ("1", "true", "yes", "on")
    if t is int:
        return int(value)
    if t is float:
        return float(value)
    return value


def _fetch_workspace(env, ws, jwt=None):
    jwt = jwt or owner_jwt(env, ws)
    _, wsd = call_jwt(env, "GET", "/api/workspaces.get", jwt, params={"id": ws})
    return wsd.get("workspace", wsd), jwt


def cmd_settings_get(a):
    w, _ = _fetch_workspace(a.env, a.workspace)
    settings = w.get("settings", {})
    if getattr(a, "key", None):
        print(json.dumps({a.key: settings.get(a.key)}, indent=2, ensure_ascii=False))
        return
    print(json.dumps({"workspace": a.workspace, "name": w.get("name"),
                      "settings": settings}, indent=2, ensure_ascii=False))


def _save_settings(env, ws, jwt, w, settings):
    """workspaces.update attend le workspace COMPLET {id, name, settings} et
    re-valide (timezone/default_language/languages requis). On renvoie donc les
    settings fusionnés intégralement, jamais un patch partiel."""
    body = {"id": ws, "name": w.get("name") or ws, "settings": settings}
    return call_jwt(env, "POST", "/api/workspaces.update", jwt, body=body)


def cmd_settings_set(a):
    """Patche UNE clé de settings (merge sur l'existant puis update complet)."""
    w, jwt = _fetch_workspace(a.env, a.workspace)
    settings = dict(w.get("settings") or {})
    settings[a.key] = _coerce_setting(a.key, a.value)
    out(*_save_settings(a.env, a.workspace, jwt, w, settings))


def cmd_settings_update(a):
    """Merge un objet settings (--file s.json ou --data '{...}') sur l'existant."""
    if not a.file and not a.data:
        die("--file <path.json> OU --data '<json>' requis.")
    try:
        if a.file:
            patch = json.loads(Path(a.file).read_text())
        else:
            patch = _json_arg(a.data)
    except FileNotFoundError:
        die(f"fichier introuvable : {a.file}")
    except json.JSONDecodeError as e:
        die(f"JSON invalide ({a.file or 'inline'}) : {e}")
    if not isinstance(patch, dict):
        die("--file/--data doit être un objet JSON de settings (ex {\"timezone\":\"Europe/Paris\"}).")
    unknown = [k for k in patch if k not in SETTINGS_KEYS]
    if unknown and not a.force:
        die(f"clés non persistées par l'allowlist UpdateWorkspace : {unknown}. "
            f"Elles seraient silencieusement droppées. Retire-les ou passe --force pour ignorer l'avertissement.")
    w, jwt = _fetch_workspace(a.env, a.workspace)
    settings = dict(w.get("settings") or {})
    settings.update(patch)
    out(*_save_settings(a.env, a.workspace, jwt, w, settings))


# ---- templates:push (contenu réel depuis fichier, code-mode MJML/HTML) ----
# === Garde-fou « préheader = nom du gabarit » (incidents 2026-08-07 / 2026-08-24) ===
#
# Le 2026-08-07, des gabarits cold ont été poussés sur le workspace `coldtunnel`
# avec `<mj-preview>{nom du gabarit}</mj-preview>` — « Ouverture observation »,
# « Premier contact », « Cloture »… Le préheader n'est pas une métadonnée
# interne : c'est le TEXTE D'APERÇU que le destinataire lit dans sa boîte, juste
# après l'objet. Le 2026-08-24, ces libellés sont en plus retombés en première
# ligne du corps texte de 215 mails cold, avant la salutation.
#
# On refuse donc AU PUSH ce que l'audit a dû rattraper après coup. Un préheader
# est soit une vraie accroche commerciale écrite pour le prospect, soit absent
# (le client mail retombe alors sur les premiers mots du corps — ce que fait un
# mail écrit à la main, et l'effet recherché en cold 1-to-1).
RX_MJ_PREVIEW = re.compile(r"<mj-preview\s*>(.*?)</mj-preview\s*>", re.S | re.I)


def _fold_label(s):
    return " ".join((s or "").split()).strip().lower()


def check_preview_not_template_name(mjml_source, name, template_id=None):
    """Refuse un MJML dont le <mj-preview> vaut le nom (ou l'id) du gabarit."""
    m = RX_MJ_PREVIEW.search(mjml_source or "")
    if not m:
        return
    preview = _fold_label(m.group(1))
    if not preview:
        return
    candidates = {_fold_label(name)}
    if template_id:
        candidates.add(_fold_label(template_id))
        candidates.add(_fold_label(template_id).replace("-", " "))
    candidates.add(_fold_label(name).replace(" ", "-"))
    candidates.discard("")
    if preview in candidates:
        die(
            f"REFUS : le <mj-preview> vaut le nom interne du gabarit ({m.group(1).strip()!r}).\n"
            f"  Le préheader est le texte d'aperçu LU PAR LE DESTINATAIRE dans sa boîte,\n"
            f"  affiché juste après l'objet — pas un libellé de gestion.\n"
            f"  Incident du 2026-08-07 : des gabarits cold poussés ainsi sur `coldtunnel`\n"
            f"  ont exposé « Ouverture observation » aux prospects ; le 2026-08-24 le même\n"
            f"  libellé est reparti en première ligne du corps texte de 215 mails.\n"
            f"  Corrige au choix :\n"
            f"    • retire le <mj-preview> (recommandé en cold 1-to-1 : la boîte affiche\n"
            f"      alors les premiers mots du corps, comme pour un mail écrit à la main) ;\n"
            f"    • ou écris une VRAIE accroche commerciale destinée au prospect."
        )


def cmd_templates_push(a):
    """Create OR update IDEMPOTENT d'un template email avec le contenu RÉEL d'un
    FICHIER (MJML ou HTML), en CODE MODE (editor_mode=code + mjml_source).

    Pourquoi code mode : Notifuse n'a PAS de convertisseur MJML→visual_editor_tree
    (seulement tree→MJML). Le visual mode exigerait donc de construire l'arbre à la
    main. Le code mode stocke le MJML brut dans email.mjml_source — c'est le chemin
    IA-first natif (cf domain/template.go EmailTemplate.Validate, EditorModeCode).
    Le HTML brut est wrappé dans <mjml><mj-body><mj-raw> pour rester compilable.

    Idempotence : --id fourni → update ; sinon on cherche un template du même name
    → update ; sinon create. --real-send ENVOIE un vrai mail de test (gated).
    """
    plain_text = None
    if a.plain_text_file:
        if not Path(a.plain_text_file).exists():
            die(f"fichier introuvable : {a.plain_text_file}")
        plain_text = Path(a.plain_text_file).read_text(encoding="utf-8")
        if not plain_text.strip():
            die("le fichier texte brut est vide")
        escaped = html.escape(plain_text).replace("\r\n", "\n").replace("\r", "\n").replace("\n", "<br />\n")
        raw = f"<mjml><mj-body><mj-section><mj-column><mj-text>{escaped}</mj-text></mj-column></mj-section></mj-body></mjml>"
        fname = a.plain_text_file.lower()
        is_html = False
    else:
        if not Path(a.file).exists():
            die(f"fichier introuvable : {a.file}")
        raw = Path(a.file).read_text(encoding="utf-8")
        fname = a.file.lower()
        is_html = fname.endswith((".html", ".htm")) or a.as_html
    if is_html and "<mjml" not in raw.lower():
        # wrap HTML brut dans un squelette MJML mj-raw (compilable, pas de réécriture)
        mjml_source = ("<mjml><mj-body><mj-raw>\n" + raw + "\n</mj-raw></mj-body></mjml>")
    else:
        mjml_source = raw

    # Garde-fou amont : un préheader ne doit JAMAIS valoir le nom du gabarit.
    check_preview_not_template_name(mjml_source, a.name, a.id)

    jwt = jwt_for_route(a.env, "templates.create", a.workspace, prefer_owner=False)

    # résout l'id cible (idempotence par --id puis par name)
    target_id = a.id
    existing = None
    if not target_id:
        _, lst = call_jwt(a.env, "GET", "/api/templates.list", jwt,
                          params={"workspace_id": a.workspace})
        for t in (lst.get("templates") or []):
            if t.get("name") == a.name:
                existing = t
                target_id = t.get("id")
                break
    else:
        _, tg = call_jwt(a.env, "GET", "/api/templates.get", jwt,
                         params={"workspace_id": a.workspace, "id": target_id})
        existing = tg.get("template")
    if not target_id:
        # slug déterministe depuis le name (≤32, [A-Za-z0-9_-])
        slug = re.sub(r"[^A-Za-z0-9_-]", "-", a.name).strip("-")[:32] or f"tpl{int(time.time())}"
        target_id = slug

    test_data = _json_arg(a.test_data) if a.test_data else {}
    email_block = {
        "editor_mode": "code",
        "mjml_source": mjml_source,
        "subject": a.subject or a.name,
        "compiled_preview": mjml_source,
    }
    if plain_text is not None:
        email_block["text"] = plain_text
        email_block["plain_text_only"] = True
    if a.subject_preview is not None:
        email_block["subject_preview"] = a.subject_preview
    if a.sender_id:
        email_block["sender_id"] = a.sender_id
    if a.reply_to:
        email_block["reply_to"] = a.reply_to

    # validation par compile (zéro envoi) — prouve que le MJML compile
    if not a.no_validate:
        cbody = {"workspace_id": a.workspace,
                 "message_id": f"cli-push-{int(time.time())}",
                 "mjml_source": mjml_source, "channel": "email",
                 "subject": email_block["subject"]}
        if test_data:
            cbody["test_data"] = test_data
        cst, cp = call_jwt(a.env, "POST", "/api/templates.compile", jwt, body=cbody)
        if cst not in (200, 201) or (isinstance(cp, dict) and cp.get("error")):
            die(f"compilation MJML échouée (HTTP {cst}) : {cp}. "
                f"Corrige le template ou passe --no-validate.")

    payload = {
        "workspace_id": a.workspace, "id": target_id, "name": a.name[:32],
        "channel": "email", "category": (a.category or "marketing")[:20],
        "email": email_block, "test_data": test_data,
    }
    verb = "templates.update" if existing else "templates.create"
    st, p = call_jwt(a.env, "POST", f"/api/{verb}", jwt, body=payload)
    result = {"action": "update" if existing else "create", "template_id": target_id,
              "http": st, "response": p}

    if a.real_send:
        if not a.to:
            die("--real-send exige --to <email>.")
        # envoi de test via un broadcast n'est pas direct ; on utilise transactional
        # seulement si une notif existe. Ici on signale honnêtement la limite.
        result["test_send"] = ("templates n'a pas d'endpoint d'envoi direct ; "
                               "utilise transactional:send ou broadcasts:send-test "
                               "avec ce template. (--real-send no-op ici)")
    print(json.dumps(result, indent=2, ensure_ascii=False))
    if st not in (200, 201):
        sys.exit(2)


def cmd_list(a):
    """Générique list : <resource>.list en GET avec filtres optionnels."""
    params = {"workspace_id": a.workspace}
    for kv in (getattr(a, "param", None) or []):
        k, _, v = kv.partition("=")
        params[k] = v
    out(*app_call(a.env, f"{a.resource}.list", a.workspace, params=params, method="GET"))


def cmd_get(a):
    params = {"workspace_id": a.workspace, "id": a.id}
    if a.resource == "automations":
        params["automation_id"] = params.pop("id")
    out(*app_call(a.env, f"{a.resource}.get", a.workspace, params=params, method="GET"))


def cmd_delete(a):
    body = {"workspace_id": a.workspace, "id": a.id}
    out(*app_call(a.env, f"{a.resource}.delete", a.workspace, body=body, method="POST"))


# broadcasts
def cmd_broadcasts_create(a):
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "broadcasts.create", a.workspace, body=body, method="POST"))


def cmd_broadcasts_action(a):
    body = {"workspace_id": a.workspace, "id": a.id}
    out(*app_call(a.env, f"broadcasts.{a.action}", a.workspace, body=body, method="POST"))


def cmd_broadcasts_schedule(a):
    body = {"workspace_id": a.workspace, "id": a.id}
    if a.at:
        try:
            scheduled = datetime.fromisoformat(a.at.replace("Z", "+00:00"))
        except ValueError as exc:
            die(f"date ISO invalide pour --at : {a.at} ({exc})")
        if scheduled.tzinfo is not None:
            scheduled = scheduled.astimezone(timezone.utc)
            body["timezone"] = "UTC"
        body["scheduled_date"] = scheduled.strftime("%Y-%m-%d")
        body["scheduled_time"] = scheduled.strftime("%H:%M")
        body["send_now"] = False
    else:
        body["send_now"] = True
    out(*app_call(a.env, "broadcasts.schedule", a.workspace, body=body, method="POST"))


def cmd_broadcasts_send_test(a):
    if not a.real_send:
        die("broadcasts:send-test envoie un VRAI mail de test → exige --real-send "
            "(staging route vers la boîte Lark de Robert). Préfère verify/dry-run.")
    body = {"workspace_id": a.workspace, "broadcast_id": a.id, "recipient_email": a.email}
    out(*app_call(a.env, "broadcasts.sendToIndividual", a.workspace, body=body, method="POST"))


# templates
def cmd_templates_create(a):
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    out(*app_call(a.env, "templates.create", a.workspace, body=body, method="POST"))


def cmd_templates_compile(a):
    # templates.compile rend un visual_editor_tree MJML (PAS un template_id) +
    # message_id requis. Soit on fournit --tree (l'arbre), soit --id pour récupérer
    # l'arbre du template existant via templates.get, puis on compile.
    body = {"workspace_id": a.workspace, "message_id": f"cli-compile-{int(time.time())}"}
    if a.tree:
        body["visual_editor_tree"] = _json_arg(a.tree)
    elif a.id:
        jwt = jwt_for_route(a.env, "templates.get", a.workspace)
        _, tg = call_jwt(a.env, "GET", "/api/templates.get", jwt,
                         params={"workspace_id": a.workspace, "id": a.id})
        tpl = tg.get("template", tg)
        tree = (tpl.get("email") or {}).get("visual_editor_tree")
        if not tree:
            die(f"template {a.id} sans visual_editor_tree (rien à compiler).")
        body["visual_editor_tree"] = tree
    else:
        die("--tree @file.json (arbre MJML) OU --id <template_id> requis.")
    if a.data:
        body["test_data"] = _json_arg(a.data)
    if a.body:
        body.update(_json_arg(a.body))
    out(*app_call(a.env, "templates.compile", a.workspace, body=body, method="POST"))


# contacts
def cmd_contacts_count(a):
    out(*app_call(a.env, "contacts.count", a.workspace, params={"workspace_id": a.workspace}, method="GET"))


def cmd_contacts_get(a):
    out(*app_call(a.env, "contacts.getByEmail", a.workspace,
                  params={"workspace_id": a.workspace, "email": a.email}, method="GET"))


def cmd_contacts_upsert(a):
    contact = _json_arg(a.data) if a.data else {"email": a.email}
    body = {"workspace_id": a.workspace, "contact": contact}
    out(*app_call(a.env, "contacts.upsert", a.workspace, body=body, method="POST"))


# Patch 2026-10-04 (audit skill distribue) : le skill AGENTS.md distribue
# documentait `contacts:import --file contacts.csv --list-id <id>` -- deux
# flags qui n'ont JAMAIS existe (la vraie commande attendait --data JSON +
# --lists). Plutot que de corriger juste la doc, on ajoute le CSV reel cote
# CLI : plus naturel pour un client qui exporte depuis un tableur, et
# borne en lots pour ne pas timeout / payload-trop-gros sur un import de
# 100 000 lignes (cf mission "fournisseur destinataire a l'import").
_CONTACTS_IMPORT_BATCH_SIZE = 500


def _read_contacts_csv(path):
    """Lit un CSV (premiere ligne = en-tetes) et retourne une liste de dicts.
    Colonne 'email' obligatoire (au moins une ligne non vide). Toutes les
    autres colonnes sont transmises telles quelles -- c'est l'API qui valide
    les champs reconnus, pas le CLI."""
    try:
        f = open(path, "r", encoding="utf-8-sig", newline="")
    except OSError as e:
        die(f"impossible de lire {path} : {e}")
    with f:
        reader = csv.DictReader(f)
        if not reader.fieldnames or "email" not in reader.fieldnames:
            die(f"{path} : en-tete 'email' manquant (colonnes trouvees : {reader.fieldnames}).")
        rows = []
        for i, row in enumerate(reader, start=2):  # 1 = en-tete
            email = (row.get("email") or "").strip()
            if not email:
                continue  # ligne vide / sans email : ignoree silencieusement
            contact = {k: v for k, v in row.items() if v not in (None, "")}
            contact["email"] = email
            rows.append(contact)
        return rows


def cmd_contacts_import(a):
    csv_path = getattr(a, "file", None)
    if csv_path and a.data:
        die("--file et --data sont exclusifs (un seul mode d'import à la fois).")
    if not csv_path and not a.data:
        die("il faut --data (tableau JSON) ou --file (CSV avec en-tete email,...).")

    lists = getattr(a, "lists", None)
    subscribe_to_lists = [l.strip() for l in lists.split(",") if l.strip()] if lists else None

    if csv_path:
        all_contacts = _read_contacts_csv(csv_path)
        if not all_contacts:
            die(f"{csv_path} : aucune ligne avec un email non vide.")
        total_imported, total_failed, batches = 0, 0, 0
        last_status, last_body = 200, {}
        for i in range(0, len(all_contacts), _CONTACTS_IMPORT_BATCH_SIZE):
            chunk = all_contacts[i:i + _CONTACTS_IMPORT_BATCH_SIZE]
            body = {"workspace_id": a.workspace, "contacts": chunk}
            if subscribe_to_lists:
                body["subscribe_to_lists"] = subscribe_to_lists
            last_status, last_body = app_call(a.env, "contacts.import", a.workspace, body=body, method="POST")
            batches += 1
            if isinstance(last_body, dict):
                total_imported += last_body.get("imported", len(chunk))
                total_failed += len(last_body.get("errors", []) or [])
        out(last_status, {"batches": batches, "total_rows": len(all_contacts),
                           "imported": total_imported, "failed": total_failed,
                           "last_batch_response": last_body})
        return

    contacts = _json_arg(a.data)
    if not isinstance(contacts, list):
        die("--data doit être un tableau JSON de contacts (ou @file.json).")
    body = {"workspace_id": a.workspace, "contacts": contacts}
    if subscribe_to_lists:
        body["subscribe_to_lists"] = subscribe_to_lists
    out(*app_call(a.env, "contacts.import", a.workspace, body=body, method="POST"))


def cmd_contacts_delete(a):
    body = {"workspace_id": a.workspace, "email": a.email}
    out(*app_call(a.env, "contacts.delete", a.workspace, body=body, method="POST"))


def cmd_lists_subscribe(a):
    contact = {"email": a.email}
    if a.data:
        contact.update(_json_arg(a.data))
    body = {"workspace_id": a.workspace, "contact": contact,
            "list_ids": [l.strip() for l in a.lists.split(",") if l.strip()]}
    out(*app_call(a.env, "lists.subscribe", a.workspace, body=body, method="POST"))


# automations
def cmd_automations_create(a):
    body = {"workspace_id": a.workspace, "automation": _json_arg(a.data)}
    out(*app_call(a.env, "automations.create", a.workspace, body=body, method="POST"))


def cmd_automations_action(a):
    body = {"workspace_id": a.workspace, "automation_id": a.id}
    out(*app_call(a.env, f"automations.{a.action}", a.workspace, body=body, method="POST"))


def cmd_automations_update(a):
    automation = _json_arg(a.data)
    automation.setdefault("id", a.id)
    body = {"workspace_id": a.workspace, "automation": automation}
    out(*app_call(a.env, "automations.update", a.workspace, body=body, method="POST"))


def cmd_automations_enroll(a):
    body = {"workspace_id": a.workspace, "automation_id": a.id,
            "contact_emails": [e.strip() for e in a.emails.split(",") if e.strip()]}
    out(*app_call(a.env, "automations.enroll", a.workspace, body=body, method="POST"))


# transactional
def cmd_transactional_create(a):
    body = {"workspace_id": a.workspace, "notification": _json_arg(a.data)}
    out(*app_call(a.env, "transactional.create", a.workspace, body=body, method="POST"))


def cmd_transactional_send(a):
    if not a.real_send:
        die("transactional:send envoie un VRAI mail → exige --real-send "
            "(staging route vers la boîte Lark de Robert).")
    notif = {"id": a.id, "contact": {"email": a.email}, "channels": ["email"]}
    if a.data:
        notif["data"] = _json_arg(a.data)
    body = {"workspace_id": a.workspace, "notification": notif}
    out(*app_call(a.env, "transactional.send", a.workspace, body=body, method="POST"))


# webhooks
def cmd_webhooks_create(a):
    body = {"workspace_id": a.workspace, "name": a.name, "url": a.url,
            "event_types": [e.strip() for e in (a.events or "").split(",") if e.strip()]}
    out(*app_call(a.env, "webhookSubscriptions.create", a.workspace, body=body, method="POST"))


def cmd_webhooks_action(a):
    body = {"workspace_id": a.workspace, "id": a.id}
    out(*app_call(a.env, f"webhookSubscriptions.{a.action}", a.workspace, body=body, method="POST"))


def cmd_webhooks_deliveries(a):
    out(*app_call(a.env, "webhookSubscriptions.deliveries", a.workspace,
                  params={"workspace_id": a.workspace, "id": a.id}, method="GET"))


# ---- admin (HMAC) ----
def cmd_admin_tenants(a):
    q = []
    if a.prefix:
        q.append(f"prefix={urllib.parse.quote(a.prefix)}")
    if a.orphans:
        q.append("include_orphans=true")
    if a.limit:
        q.append(f"limit={a.limit}")
    qs = ("?" + "&".join(q)) if q else ""
    out(*call_hmac(a.env, "GET", f"/api/veridian/admin/tenants{qs}", None))


def cmd_admin_grant(a):
    body = {"tenant_id": a.tenant_id}
    if a.reason:
        body["reason"] = a.reason
    out(*call_hmac(a.env, "POST", "/api/veridian/admin/grant-unlimited", body))


def cmd_admin_gc(a):
    body = {"dry_run": a.dry_run}
    if a.cap:
        body["cap"] = a.cap
    if a.include_clients:
        body["include_orphans"] = True
    out(*call_hmac(a.env, "POST", "/api/veridian/admin/gc-orphan-workspace-dbs", body))


def cmd_admin_stats(a):
    out(*call_hmac(a.env, "GET", "/api/veridian/admin/test-tenants-stats", None))


def cmd_admin_cold_simulate(a):
    body = {"mode": a.mode, "workspace_id": a.workspace}
    if a.data:
        body.update(_json_arg(a.data))
    out(*call_hmac(a.env, "POST", "/api/veridian/admin/cold-simulate", body))


# ---- generic escape hatch ----
def cmd_api(a):
    rv = a.resource.lstrip("/")
    if rv.startswith("api/"):
        rv = rv[4:]
    method = a.method.upper()
    params = {}
    for kv in (a.param or []):
        k, _, v = kv.partition("=")
        params[k] = v
    body = _json_arg(a.data) if a.data else None
    auth = a.auth or "auto"

    # Tenant/admin/HMAC routes
    route = f"/api/{rv}"
    is_hmac = auth == "hmac" or (auth == "auto" and any(route.startswith(p) for p in HMAC_PREFIXES))
    if is_hmac:
        if method == "GET" and params:
            route = route + "?" + urllib.parse.urlencode(params)
        out(*call_hmac(a.env, method, route, body))
        return

    if auth == "none":
        url = BASES[a.env] + route
        if method == "GET" and params:
            url += "?" + urllib.parse.urlencode(params)
        out(*_request(url, method, {"Content-Type": "application/json"},
                      json.dumps(body) if body is not None else None))
        return

    # JWT app route
    ws = a.workspace or params.get("workspace_id") or (body or {}).get("workspace_id")
    if not ws:
        die("--workspace requis pour une route applicative JWT (sauf --auth none/hmac).")
    prefer_owner = auth == "owner"
    jwt = jwt_for_route(a.env, rv, ws, prefer_owner)
    if method == "GET":
        q = dict(params)
        q.setdefault("workspace_id", ws)
        out(*call_jwt(a.env, "GET", route, jwt, params=q))
    else:
        b = dict(body or {})
        b.setdefault("workspace_id", ws)
        out(*call_jwt(a.env, "POST", route, jwt, body=b))


# ================================================================ COUVERTURE
# TOTALE (mission 2026-10-03) — commandes dédiées pour les routes listées par
# Robert comme utiles à la prospection, puis mécanique générique pour le
# reste du long tail (blog/tasks/setup/user/system settings/veridian admin
# extras/tenants lifecycle...). Cf. EXISTING_COMMAND_ROUTES plus haut pour ce
# qui était déjà couvert avant cette mission.

# ---- contactLists.* (prospection : qui est dans quelle liste, à quel statut)
def cmd_contactlists_get_by_ids(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "email": a.email, "list_id": a.id}
    out(*call_jwt(a.env, "GET", "/api/contactLists.getByIDs", jwt, params=params))


def cmd_contactlists_by_list(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "list_id": a.id}
    out(*call_jwt(a.env, "GET", "/api/contactLists.getContactsByList", jwt, params=params))


def cmd_contactlists_by_contact(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "email": a.email}
    out(*call_jwt(a.env, "GET", "/api/contactLists.getListsByContact", jwt, params=params))


def cmd_contactlists_update_status(a):
    jwt = apikey_for(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "email": a.email, "list_id": a.id, "status": a.status}
    out(*call_jwt(a.env, "POST", "/api/contactLists.updateStatus", jwt, body=body))


def cmd_contactlists_remove(a):
    jwt = apikey_for(a.env, a.workspace)
    body = {"workspace_id": a.workspace, "email": a.email, "list_id": a.id}
    out(*call_jwt(a.env, "POST", "/api/contactLists.removeContact", jwt, body=body))


# ---- automations.nodeExecutions (où est bloqué un contact dans l'automation)
def cmd_automations_node_executions(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "automation_id": a.id, "email": a.email}
    out(*call_jwt(a.env, "GET", "/api/automations.nodeExecutions", jwt, params=params))


# ---- templates.update direct (sans repasser par tout le contenu, cf templates:push)
def cmd_templates_update(a):
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    body.setdefault("id", a.id)
    out(*app_call(a.env, "templates.update", a.workspace, body=body, method="POST"))


# ---- segments.update / segments.preview
def cmd_segments_update(a):
    body = _json_arg(a.data) or {}
    body.setdefault("workspace_id", a.workspace)
    body.setdefault("id", a.id)
    out(*app_call(a.env, "segments.update", a.workspace, body=body, method="POST"))


def cmd_segments_preview(a):
    body = {"workspace_id": a.workspace, "tree": _json_arg(a.tree)}
    if a.limit:
        body["limit"] = a.limit
    out(*app_call(a.env, "segments.preview", a.workspace, body=body, method="POST"))


# ---- messages.broadcastStats
def cmd_messages_broadcast_stats(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "broadcast_id": a.id}
    out(*call_jwt(a.env, "GET", "/api/messages.broadcastStats", jwt, params=params))


# ---- settings.testSmtp (SMTP du WORKSPACE, via la route applicative — distinct
# de system:settings-test-smtp qui teste le SMTP SYSTÈME root, cf plus bas)
def cmd_workspace_test_smtp(a):
    body = {"workspace_id": a.workspace, "integration_id": a.id}
    out(*app_call(a.env, "settings.testSmtp", a.workspace, body=body, method="POST"))


# ---- webhookSubscriptions.update / eventTypes
def cmd_webhooks_update(a):
    body = _json_arg(a.data) or {}
    body["workspace_id"] = a.workspace
    body["id"] = a.id
    if a.name:
        body["name"] = a.name
    if a.url:
        body["url"] = a.url
    if a.events:
        body["event_types"] = [e.strip() for e in a.events.split(",") if e.strip()]
    if a.enabled is not None:
        body["enabled"] = a.enabled
    out(*app_call(a.env, "webhookSubscriptions.update", a.workspace, body=body, method="POST"))


def cmd_webhooks_event_types(a):
    out(*app_call(a.env, "webhookSubscriptions.eventTypes", a.workspace, method="GET"))


# ---- customEvents.import (bulk, --data tableau JSON ou --file)
def cmd_events_import(a):
    events = _json_arg(a.data) if a.data else json.loads(Path(a.file).read_text())
    body = {"workspace_id": a.workspace, "events": events}
    out(*app_call(a.env, "customEvents.import", a.workspace, body=body, method="POST"))


# ---- contacts.getByExternalID
def cmd_contacts_get_by_external_id(a):
    jwt = apikey_for(a.env, a.workspace)
    params = {"workspace_id": a.workspace, "external_id": a.external_id}
    out(*call_jwt(a.env, "GET", "/api/contacts.getByExternalID", jwt, params=params))


# ---- transactional.update / testTemplate
def cmd_transactional_update(a):
    updates = _json_arg(a.data) or {}
    body = {"workspace_id": a.workspace, "id": a.id, "updates": updates}
    out(*app_call(a.env, "transactional.update", a.workspace, body=body, method="POST"))


def cmd_transactional_test_template(a):
    body = {"workspace_id": a.workspace, "template_id": a.template_id,
            "integration_id": a.integration_id, "sender_id": a.sender_id,
            "recipient_email": a.email, "language": a.language or "en"}
    if a.options:
        body["email_options"] = _json_arg(a.options)
    out(*app_call(a.env, "transactional.testTemplate", a.workspace, body=body, method="POST"))


# ================================================================ long tail
# générique : tout le reste des routes resource.verb sans commande dédiée
# (blog*, templateBlocks*, tasks*, workspaces.{list,create,delete,
# acceptInvitation,deleteInvitation,verifyInvitationToken}, demo.reset,
# llm.chat, webhooks.register/status (inbound provider, DISTINCT de
# webhookSubscriptions), inboundWebhookEvents.list, timeline.list,
# veridian/{workspaces.inviteMember,messages.*,templates.deliverabilityScore,
# emailProfiles.usage}). Flags : --id (mappé au nom de paramètre attendu via
# GENERIC_ID_PARAM), --param k=v (GET), --set k=v (POST simple), --data/--file
# (corps complexe). Auth toujours owner JWT (a tous les droits ; évite les
# 403 d'un apikey sans permission précise — cf apikey_for qui retombe sur
# owner de toute façon en l'absence de NOTIFUSE_API_KEY).
GENERIC_ID_PARAM = {
    "blogPosts.get": "id", "blogPosts.delete": "id", "blogPosts.update": "id",
    "blogPosts.publish": "id", "blogPosts.unpublish": "id",
    "blogCategories.get": "id", "blogCategories.delete": "id", "blogCategories.update": "id",
    "blogThemes.get": "id", "blogThemes.update": "id", "blogThemes.publish": "id",
    "templateBlocks.get": "id", "templateBlocks.delete": "id", "templateBlocks.update": "id",
    "tasks.get": "id", "tasks.delete": "id", "tasks.reset": "id", "tasks.trigger": "id",
    "workspaces.delete": "id", "workspaces.deleteInvitation": "id",
    "broadcasts.refreshGlobalFeed": "broadcast_id", "broadcasts.testRecipientFeed": "broadcast_id",
}

# (resource.verb, méthode HTTP, nom de commande CLI, workspace requis ?)
GENERIC_RESOURCE_ROUTES = [
    ("blogCategories.list", "GET", "blog:categories-list", True),
    ("blogCategories.get", "GET", "blog:categories-get", True),
    ("blogCategories.create", "POST", "blog:categories-create", True),
    ("blogCategories.update", "POST", "blog:categories-update", True),
    ("blogCategories.delete", "POST", "blog:categories-delete", True),
    ("blogPosts.list", "GET", "blog:posts-list", True),
    ("blogPosts.get", "GET", "blog:posts-get", True),
    ("blogPosts.create", "POST", "blog:posts-create", True),
    ("blogPosts.update", "POST", "blog:posts-update", True),
    ("blogPosts.delete", "POST", "blog:posts-delete", True),
    ("blogPosts.publish", "POST", "blog:posts-publish", True),
    ("blogPosts.unpublish", "POST", "blog:posts-unpublish", True),
    ("blogThemes.list", "GET", "blog:themes-list", True),
    ("blogThemes.get", "GET", "blog:themes-get", True),
    ("blogThemes.getPublished", "GET", "blog:themes-get-published", True),
    ("blogThemes.create", "POST", "blog:themes-create", True),
    ("blogThemes.update", "POST", "blog:themes-update", True),
    ("blogThemes.publish", "POST", "blog:themes-publish", True),
    ("templateBlocks.list", "GET", "template-blocks:list", True),
    ("templateBlocks.get", "GET", "template-blocks:get", True),
    ("templateBlocks.create", "POST", "template-blocks:create", True),
    ("templateBlocks.update", "POST", "template-blocks:update", True),
    ("templateBlocks.delete", "POST", "template-blocks:delete", True),
    ("tasks.list", "GET", "tasks:list", True),
    ("tasks.get", "GET", "tasks:get", True),
    ("tasks.create", "POST", "tasks:create", True),
    ("tasks.delete", "POST", "tasks:delete", True),
    ("tasks.reset", "POST", "tasks:reset", True),
    ("tasks.trigger", "POST", "tasks:trigger", True),
    ("broadcasts.update", "POST", "broadcasts:update", True),
    ("broadcasts.getTestResults", "GET", "broadcasts:getTestResults", True),
    ("broadcasts.selectWinner", "POST", "broadcasts:selectWinner", True),
    ("broadcasts.refreshGlobalFeed", "POST", "broadcasts:refreshGlobalFeed", True),
    ("broadcasts.testRecipientFeed", "POST", "broadcasts:testRecipientFeed", True),
    ("workspaces.list", "GET", "workspaces:list", False),
    ("workspaces.create", "POST", "workspaces:create", False),
    ("workspaces.delete", "POST", "workspaces:delete", True),
    ("workspaces.deleteInvitation", "POST", "workspaces:delete-invitation", True),
    ("llm.chat", "POST", "llm:chat", True),
    ("email.testProvider", "POST", "integrations:test-provider", True),
    ("inboundWebhookEvents.list", "GET", "inbound-events:list", True),
    ("timeline.list", "GET", "timeline:list", True),
    ("veridian/messages.engagementByClass", "GET", "veridian:engagement-by-class", True),
    ("veridian/messages.replyStats", "GET", "veridian:reply-stats", True),
    ("automations.exitContact", "POST", "automations:exit-contact", True),
    ("automations.resetContact", "POST", "automations:reset-contact", True),
    ("veridian/messages.reputationStatus", "GET", "veridian:reputation-status", True),
    ("veridian/templates.deliverabilityScore", "GET", "veridian:deliverability-score", True),
    ("veridian/emailProfiles.usage", "GET", "veridian:email-profile-usage", True),
    ("veridian/workspaces.inviteMember", "POST", "veridian:invite-member", True),
]


def cmd_generic_rv(a):
    rv = a._rv
    method = a._method
    ws = a.workspace if getattr(a, "workspace", None) and a.workspace != "-" else None
    route = f"/api/{rv}"
    if method == "GET":
        params = {}
        if ws:
            params["workspace_id"] = ws
        if getattr(a, "id", None):
            params[GENERIC_ID_PARAM.get(rv, "id")] = a.id
        for kv in (getattr(a, "param", None) or []):
            k, _, v = kv.partition("=")
            params[k] = v
        jwt = owner_jwt(a.env, ws) if ws else None
        if jwt:
            out(*call_jwt(a.env, "GET", route, jwt, params=params))
        else:
            out(*call_anon(a.env, "GET", route, params=params))
        return
    body = _json_arg(getattr(a, "data", None)) or {}
    if ws:
        body.setdefault("workspace_id", ws)
    if getattr(a, "id", None):
        body.setdefault(GENERIC_ID_PARAM.get(rv, "id"), a.id)
    for kv in (getattr(a, "set", None) or []):
        k, _, v = kv.partition("=")
        body[k] = v
    jwt = owner_jwt(a.env, ws) if ws else None
    if jwt:
        out(*call_jwt(a.env, "POST", route, jwt, body=body))
    else:
        out(*call_anon(a.env, "POST", route, body=body))


# ---- long tail : routes RAW (chemin direct, pas resource.verb), placeholders
# {id}/{tenantId} substitués depuis --id/--tenant-id. auth: hmac|owner|none.
RAW_ROUTES = [
    # Mission 2026-10-04 (audit CLI, coverage Go=199 vs CLI=195) : cette
    # route de la mission "API & agents" n'avait aucune commande dediee.
    # createAgentInstallToken exige le JWT owner du workspace cible.
    # (agent:exchange-token est a part, bespoke : voir cmd_agent_exchange_token
    # -- reponse text/plain KEY=VALUE, pas JSON, incompatible avec
    # cmd_raw_generic qui json.loads() sans condition.)
    ("agent:install-token", "POST", "/api/workspaces.createAgentInstallToken", "owner", []),
    ("tenants:update-plan", "POST", "/api/tenants/update-plan", "hmac", []),
    ("tenants:suspend", "POST", "/api/tenants/suspend", "hmac", []),
    ("tenants:resume", "POST", "/api/tenants/resume", "hmac", []),
    ("tenants:delete-one", "DELETE", "/api/tenants/{id}", "hmac", ["id"]),
    ("tenants:soft-delete", "POST", "/api/tenants/{id}/soft-delete", "hmac", ["id"]),
    ("tenants:restore", "POST", "/api/tenants/{id}/restore", "hmac", ["id"]),
    ("tenants:purge", "POST", "/api/tenants/{id}/purge", "hmac", ["id"]),
    ("tenants:touch", "POST", "/api/tenants/{id}/touch", "hmac", ["id"]),
    ("tenants:usage-summary", "GET", "/api/tenants/{id}/usage-summary", "hmac", ["id"]),
    ("tenants:limits", "GET", "/api/tenants/{id}/limits", "hmac", ["id"]),
    ("tenants:health", "GET", "/api/tenants/{id}/health", "hmac", ["id"]),
    ("tenants:rotate-api-key", "POST", "/api/tenants/{id}/rotate-api-key", "hmac", ["id"]),
    ("tenants:transfer-owner", "POST", "/api/tenants/{id}/transfer-owner", "hmac", ["id"]),
    ("tenants:sync-member", "POST", "/api/tenants/{id}/sync-member", "hmac", ["id"]),
    ("tenants:remove-member", "POST", "/api/tenants/{id}/remove-member", "hmac", ["id"]),
    ("tenants:restore-member", "POST", "/api/tenants/{id}/restore-member", "hmac", ["id"]),
    ("tenants:attach-member", "POST", "/api/tenants/{tenantId}/attach-member", "hmac", ["tenantId"]),
    ("tenants:freeze-member", "POST", "/api/tenants/{tenantId}/freeze-member", "hmac", ["tenantId"]),
    ("tenants:unfreeze-member", "POST", "/api/tenants/{tenantId}/unfreeze-member", "hmac", ["tenantId"]),
    ("veridian:workspaces-attach-member", "POST",
     "/api/veridian/workspaces/{tenantId}/attach-member", "hmac", ["tenantId"]),
    ("admin:cache-invalidate", "POST", "/api/veridian/admin/cache/invalidate", "hmac", []),
    ("admin:attach-owner", "POST", "/api/veridian/admin/attach-owner", "hmac", []),
    ("admin:pricing-cache", "GET", "/api/veridian/admin/pricing-cache", "hmac", []),
    ("sso:issue-magic-link", "POST", "/api/sso/issue-magic-link", "hmac", []),
    ("users:by-email", "GET", "/api/users/by-email", "hmac", []),
    ("veridian:mode", "GET", "/api/veridian/mode", "none", []),
    ("veridian:hub-discovery-me", "GET", "/api/veridian/hub-discovery/me", "hmac", []),
    ("version", "GET", "/api/version", "none", []),
    ("utils:detect-favicon", "GET", "/api/detect-favicon", "none", []),
    ("workspaces:verify-invitation", "GET", "/api/workspaces.verifyInvitationToken", "none", []),
    ("workspaces:accept-invitation", "POST", "/api/workspaces.acceptInvitation", "none", []),
    ("demo:reset", "POST", "/api/demo.reset", "none", []),
    ("provider-webhooks:register", "POST", "/api/webhooks.register", "owner", []),
    ("provider-webhooks:status", "GET", "/api/webhooks.status", "owner", []),
    ("session:me", "GET", "/api/user.me", "owner", []),
    ("session:logout", "POST", "/api/user.logout", "owner", []),
    ("session:update-language", "POST", "/api/user.updateLanguage", "owner", []),
    ("session:signin", "POST", "/api/user.signin", "none", []),
    ("session:verify", "POST", "/api/user.verify", "none", []),
    ("session:root-signin", "POST", "/api/user.rootSignin", "none", []),
    ("system:setup-status", "GET", "/api/setup.status", "none", []),
    ("system:setup-initialize", "POST", "/api/setup.initialize", "none", []),
    ("system:setup-test-smtp", "POST", "/api/setup.testSmtp", "none", []),
    ("system:settings-get", "GET", "/api/settings.get", "owner", []),
    ("system:settings-update", "POST", "/api/settings.update", "owner", []),
    ("system:settings-test-smtp", "POST", "/api/settings.testSmtp", "owner", []),
    ("cron:run", "POST", "/api/cron", "none", []),
    ("cron:status", "GET", "/api/cron.status", "none", []),
    ("tasks:execute", "POST", "/api/tasks.execute", "none", ["id"]),
]


def cmd_raw_generic(a):
    path = a._path
    for ph in a._placeholders:
        val = getattr(a, "placeholder_" + ph, None) or a.id
        path = path.replace("{" + ph + "}", urllib.parse.quote(val, safe=""))
    body = None
    params = None
    if a._method in ("POST", "PUT", "DELETE") and a._method != "GET":
        body = _json_arg(getattr(a, "data", None))
        if body is None and a._method == "POST":
            body = {}
    else:
        params = {}
        for kv in (getattr(a, "param", None) or []):
            k, _, v = kv.partition("=")
            params[k] = v
    if a._auth == "hmac":
        out(*call_hmac(a.env, a._method, path, body))
    elif a._auth == "none":
        out(*call_anon(a.env, a._method, path, body=body, params=params))
    else:  # owner — nécessite un --workspace de référence pour obtenir un JWT
        if not getattr(a, "workspace", None):
            die(f"{a._path} (auth owner) exige --workspace <tenant_id de référence> "
                f"pour obtenir un JWT.")
        jwt = owner_jwt(a.env, a.workspace)
        if a._method == "GET":
            out(*call_jwt(a.env, "GET", path, jwt, params=params))
        else:
            out(*call_jwt(a.env, "POST", path, jwt, body=body or {}))


def cmd_agent_exchange_token(a):
    """agent:exchange-token -- POST /api/agent.exchangeToken, PUBLIC (le
    jeton EST le secret). Reponse reelle text/plain KEY=VALUE (PAS JSON :
    install.sh la parse avec `read`/IFS sans dependance jq, cf
    agent_handler.go handleExchangeToken) -- traitee ici a part de
    cmd_raw_generic qui suppose du JSON partout.
    Mission 2026-10-04 (audit CLI, coverage Go=199 vs CLI=195)."""
    token = _json_arg(a.data).get("token") if a.data else None
    if not token:
        die("--data '{\"token\":\"<jeton>\"}' requis.")
    url = BASES[a.env] + "/api/agent.exchangeToken"
    req = urllib.request.Request(
        url, data=json.dumps({"token": token}).encode(), method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=40) as r:
            raw = r.read().decode()
            status = r.status
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        status = e.code
    except Exception as e:
        die(f"Erreur réseau ({url}) : {e}")
        return
    parsed = {}
    for line in raw.splitlines():
        k, _, v = line.partition("=")
        if k:
            parsed[k] = v
    out(status, parsed or {"raw": raw})


# ================================================================ config
# `notifuse config <ws>` — mission Robert 03/10 : "voir la configuration d'un
# workspace en une commande, ça doit être pixel". Port Python FIDÈLE des
# cascades Go réelles (payload > intégration > workspace), lues directement
# dans les fichiers du fork :
#   - internal/service/queue/veridian_per_sender_cap.go  (per-sender)
#   - internal/service/queue/veridian_daily_cap.go       (per-recipient + classe + warmup)
#   - internal/domain/veridian_warmup.go                 (VeridianWarmupCapForDay)
#   - internal/service/queue/veridian_sending_window_gate.go (fenêtre, jour Go Sunday=0)
PROD_DB_HOST = "prod"
# Mission 2026-10-04 : le nom du conteneur DB prod change a CHAQUE redeploy
# Dokploy (suffixe = hash du projet compose, regenere) -- un nom en dur
# devient faux silencieusement (mesure : "No such container" en prod,
# _queue_staleness jamais mesure sans que personne ne le voie). Resolu
# dynamiquement via `docker ps` plutot que mis a jour a la main.
_PROD_DB_CONTAINER_CACHE = {}


def _prod_db_container():
    if "name" in _PROD_DB_CONTAINER_CACHE:
        return _PROD_DB_CONTAINER_CACHE["name"]
    rc, out_, err = _ssh(PROD_DB_HOST, "docker ps --format '{{.Names}}' | grep '^notifuse-db-'")
    name = out_.strip().splitlines()[0].strip() if rc == 0 and out_.strip() else None
    _PROD_DB_CONTAINER_CACHE["name"] = name
    return name


def _mask_secret(v):
    if not v:
        return v
    return "••••••••" if v else v


def _resolve_scalar_cap(provider, ws_settings, key):
    """Cascade PROVIDER > WORKSPACE pour un cap scalaire (int). Miroir de
    veridianResolvePerSenderCap / la branche per-recipient de
    veridianResolveDailyCaps (le payload, 3e niveau, n'existe qu'au message
    enqueued — hors scope d'une vue 'config', cf vérif file plus bas)."""
    pv = (provider or {}).get(key)
    if isinstance(pv, (int, float)) and pv > 0:
        return pv, "intégration"
    wv = (ws_settings or {}).get(key)
    if isinstance(wv, (int, float)) and wv > 0:
        return wv, "workspace (fallback — invisible depuis l'écran intégration)"
    return 0, "aucun (illimité)"


def _resolve_class_caps(provider, ws_settings):
    pv = (provider or {}).get("veridian_provider_class_daily_cap") or {}
    if pv:
        return pv, "intégration"
    wv = (ws_settings or {}).get("veridian_provider_class_daily_cap") or {}
    if wv:
        return wv, "workspace (fallback)"
    return {}, "aucun"


def _warmup_cap_for_day(started_at_iso, schedule, step_days, now):
    """Port fidèle de domain.VeridianWarmupCapForDay (veridian_warmup.go:56)."""
    if not started_at_iso or not schedule:
        return 0
    try:
        start = datetime.fromisoformat(started_at_iso.replace("Z", "+00:00"))
    except Exception:
        return 0
    if start.tzinfo is None:
        start = start.replace(tzinfo=timezone.utc)
    step_days = step_days if (step_days and step_days > 0) else 1
    elapsed_days = int((now - start).total_seconds() // 86400)
    if elapsed_days < 0:
        elapsed_days = 0
    idx = elapsed_days // step_days
    if idx >= len(schedule):
        idx = len(schedule) - 1
    cap = schedule[idx]
    return cap if cap and cap >= 0 else 0


def _sending_window_open(window, now_utc):
    """Port de VeridianSendingWindow.IsWithinWindow. Jour Go: Sunday=0..Saturday=6
    (time.Weekday) -> converti depuis Python isoweekday (Monday=1..Sunday=7) par
    `% 7` (Sunday 7%7=0, Monday 1%7=1, ... Saturday 6%7=6 : alignement exact)."""
    if not window:
        return True, "aucune fenêtre configurée — envoi 24/7 (non-régression upstream)"
    tzname = window.get("timezone") or "UTC"
    try:
        if ZoneInfo is None:
            raise RuntimeError("zoneinfo indisponible")
        tz = ZoneInfo(tzname)
    except Exception:
        tz = timezone.utc
        tzname = "UTC (zoneinfo indisponible)"
    local = now_utc.astimezone(tz)
    go_weekday = local.isoweekday() % 7
    days = window.get("days") or []
    in_day = go_weekday in days
    start_h = window.get("start_hour", 0)
    end_h = window.get("end_hour", 24)
    in_hours = start_h <= local.hour < end_h
    open_now = in_day and in_hours
    note = (f"{local.strftime('%A %d/%m %H:%M')} {tzname} — jour "
            f"{'ouvert' if in_day else 'FERMÉ'}, heure {'ouverte' if in_hours else 'FERMÉE'} "
            f"(fenêtre {start_h}h-{end_h}h, jours Go {days})")
    return open_now, note


def _effective_caps_for_integration(ws_settings, integ, now):
    prov = integ.get("email_provider") or integ.get("provider") or {}
    if (prov.get("kind") or "") != "smtp":
        return None  # seules les infras d'envoi SMTP portent des plafonds cold
    per_recip, per_recip_src = _resolve_scalar_cap(prov, ws_settings, "veridian_per_recipient_daily_cap")
    per_sender, per_sender_src = _resolve_scalar_cap(prov, ws_settings, "veridian_per_sender_daily_cap")
    class_caps, class_src = _resolve_class_caps(prov, ws_settings)
    warm_active = bool(prov.get("veridian_warmup_started_at") and prov.get("veridian_warmup_schedule"))
    warm_cap = _warmup_cap_for_day(prov.get("veridian_warmup_started_at"),
                                    prov.get("veridian_warmup_schedule"),
                                    prov.get("veridian_warmup_step_days"), now) if warm_active else 0
    window = prov.get("veridian_sending_window") or ws_settings.get("veridian_sending_window")
    window_open, window_note = _sending_window_open(window, now)
    if warm_active and warm_cap > 0:
        gate, val = "warmup (PRIME sur le cap-classe statique, le court-circuite)", warm_cap
    elif class_caps:
        min_class = min(class_caps.values())
        gate, val = ("classe la plus basse" if (not per_sender or min_class <= per_sender)
                     else "per-sender"), (min_class if (not per_sender or min_class <= per_sender) else per_sender)
    elif per_sender:
        gate, val = "per-sender", per_sender
    else:
        gate, val = "aucun", 0
    return {
        "integration_id": integ.get("id"), "name": integ.get("name"),
        "per_recipient_daily_cap": {"value": per_recip, "source": per_recip_src},
        "per_sender_daily_cap": {"value": per_sender, "source": per_sender_src},
        "bounce_freeze_threshold": ({"value": prov["veridian_hard_bounce_freeze_threshold"], "source": "intégration"}
                                    if prov.get("veridian_hard_bounce_freeze_threshold") else
                                    {"value": 0.03, "source": "défaut fork"}),
        "provider_class_daily_cap": {"value": class_caps, "source": class_src},
        "warmup": {"active": warm_active, "cap_today": warm_cap,
                   "started_at": prov.get("veridian_warmup_started_at"),
                   "schedule": prov.get("veridian_warmup_schedule"),
                   "step_days": prov.get("veridian_warmup_step_days")},
        "sending_window": {"open_now": window_open, "note": window_note},
        "plafond_limitant_aujourdhui": {"gate": gate, "valeur": val},
    }


def _ssh(host, remote_cmd, timeout=20):
    try:
        r = subprocess.run(["ssh", host, remote_cmd], capture_output=True, text=True, timeout=timeout)
        return r.returncode, r.stdout.strip(), r.stderr.strip()
    except Exception as e:
        return 2, "", str(e)


def _queue_staleness(env, workspace, integrations):
    """Compte, PAR LECTURE SEULE (psql -tAc, pas d'écriture), les messages en
    file dont le payload fige un plafond DIFFÉRENT du plafond résolu live
    aujourd'hui (mission : staleness des caps figés à l'enqueue). PROD only
    (le conteneur DB staging n'est pas le même nom) ; voir --no-queue-check."""
    if env != "prod":
        return {"mesuré": False, "raison": "queue-check implémenté PROD seulement (--no-queue-check pour staging)"}
    # 04/10 (audit) : workspace etait interpole tel quel dans une commande shell
    # distante sur l hote DB prod -> injection. Les ids Notifuse sont alphanumeriques.
    if not re.fullmatch(r"[A-Za-z0-9_]{1,64}", workspace or ""):
        return {"mesuré": False, "raison": "id de workspace invalide (alphanumerique attendu), contrôle de file non lancé"}
    container = _prod_db_container()
    if not container:
        return {"mesuré": False, "raison": "conteneur DB prod introuvable (docker ps) -- redeploy Dokploy recent ? nom non resolu."}
    db = f"notifuse_ws_{workspace}"
    rc, out_, err = _ssh(
        PROD_DB_HOST,
        f"docker exec {container} psql -U postgres -d {db} -tAc "
        f"\"SELECT integration_id, payload->>'veridian_per_sender_daily_cap' AS sc, "
        f"payload->>'veridian_per_recipient_daily_cap' AS rc, "
        f"payload->>'veridian_provider_class_daily_cap' AS cc, count(*) "
        f"FROM email_queue WHERE status='pending' GROUP BY 1,2,3,4;\"",
    )
    if rc != 0:
        return {"mesuré": False, "raison": f"ssh/psql échoué (rc={rc}): {err[:300]}"}
    by_integration = {}
    total_pending = 0
    total_stale = 0
    live_by_integ = {i["integration_id"]: i for i in integrations if i}
    for line in out_.splitlines():
        if not line.strip():
            continue
        parts = line.split("|")
        if len(parts) < 5:
            continue
        integ_id, sc, rc_, cc, cnt = parts[0], parts[1], parts[2], parts[3], parts[4]
        try:
            cnt = int(cnt)
        except ValueError:
            continue
        total_pending += cnt
        live = live_by_integ.get(integ_id)
        stale = False
        if live:
            if sc and str(live["per_sender_daily_cap"]["value"]) != sc:
                stale = True
            if rc_ and str(live["per_recipient_daily_cap"]["value"]) != rc_:
                stale = True
            if cc and json.loads(cc) != live["provider_class_daily_cap"]["value"]:
                stale = True
        entry = by_integration.setdefault(integ_id, {"pending": 0, "stale": 0})
        entry["pending"] += cnt
        if stale:
            entry["stale"] += cnt
            total_stale += cnt
    return {"mesuré": True, "pending_total": total_pending, "stale_total": total_stale,
            "par_integration": by_integration,
            "note": "'stale' = le payload fige un plafond différent de la valeur résolue live aujourd'hui "
                    "(payload gelé à l'enqueue, cf veridianResolve*Cap)."}


def cmd_config(a):
    ws, jwt = _fetch_workspace(a.env, a.workspace)
    settings = ws.get("settings") or {}
    integrations = ws.get("integrations") or []
    now = datetime.now(timezone.utc)

    # En mode scopé (CLI `notifuse`), pas de secret HMAC : le plan tenant
    # (admin cross-workspace) n'est ni demandé ni affiché. `notifuse-admin
    # config`/`status` le donnent (mode admin, inchangé).
    if _SCOPED_MODE:
        tenant = {"mesuré": False, "raison": "plan tenant = information admin, "
                  "indisponible en mode utilisateur (voir notifuse-admin status/config)"}
    else:
        _, tenant = call_hmac(a.env, "GET", f"/api/tenants/{urllib.parse.quote(a.workspace)}/status", None)
    _, cc = call_jwt(a.env, "GET", "/api/contacts.count", jwt, params={"workspace_id": a.workspace})
    _, lists = call_jwt(a.env, "GET", "/api/lists.list", jwt, params={"workspace_id": a.workspace})
    lists_view = []
    for L in (lists.get("lists") or lists if isinstance(lists, list) else lists.get("lists") or []):
        if not isinstance(L, dict):
            continue
        _, st = call_jwt(a.env, "GET", "/api/lists.stats", jwt, params={"workspace_id": a.workspace, "list_id": L.get("id")})
        lists_view.append({"id": L.get("id"), "name": L.get("name"), "stats": st})
    _, segments = call_jwt(a.env, "GET", "/api/segments.list", jwt, params={"workspace_id": a.workspace})
    _, templates = call_jwt(a.env, "GET", "/api/templates.list", jwt, params={"workspace_id": a.workspace})
    templates_view = [{"id": t.get("id"), "name": t.get("name"), "subject": t.get("subject") or t.get("email", {}).get("subject")}
                       for t in (templates.get("templates") or []) if isinstance(t, dict)]
    _, automations = call_jwt(a.env, "GET", "/api/automations.list", jwt, params={"workspace_id": a.workspace})
    automations_view = []
    for auto in (automations.get("automations") or []):
        if not isinstance(auto, dict):
            continue
        automations_view.append({"id": auto.get("id"), "name": auto.get("name"), "status": auto.get("status"),
                                 "nodes": len((auto.get("tree") or {}).get("children", []) or []) or None})
    _, webhooks = call_jwt(a.env, "GET", "/api/webhookSubscriptions.list", jwt, params={"workspace_id": a.workspace})
    _, members = call_jwt(a.env, "GET", "/api/workspaces.members", jwt, params={"id": a.workspace})
    _, breakdown = call_jwt(a.env, "GET", "/api/veridian/contacts.providerBreakdown", jwt, params={"workspace_id": a.workspace})

    caps = [c for c in (_effective_caps_for_integration(settings, i, now) for i in integrations) if c]
    if _SCOPED_MODE:
        # SSH vers le prod DB = capacité OPÉRATEUR (bastion), pas une route API
        # du workspace : jamais en mode scopé, même avec --no-queue-check absent.
        queue = {"mesuré": False, "raison": "contrôle opérateur (SSH prod DB), réservé à notifuse-admin"}
    elif a.no_queue_check:
        queue = {"mesuré": False, "raison": "sauté (--no-queue-check)"}
    else:
        queue = _queue_staleness(a.env, a.workspace, caps)

    # Etat LIVE du fusible de reputation par couple (domaine emetteur, classe
    # destinataire) : meme calcul que le gate d'envoi (route reputationStatus).
    rep_code, reputation = call_jwt(a.env, "GET", "/api/veridian/messages.reputationStatus", jwt, params={"workspace_id": a.workspace})
    if rep_code != 200 or not isinstance(reputation, dict):
        reputation = {"mesuré": False, "raison": f"reputationStatus HTTP {rep_code}"}

    # Lot 2 (08/10) : la VÉRITÉ des plafonds est celle du serveur (même fonction que
    # le worker). `plafonds_effectifs` ci-dessous est l'ancien port Python, gardé pour
    # `_queue_staleness` seulement : en cas d'écart, le serveur a raison.
    ov_code, ov = call_jwt(a.env, "GET", "/api/veridian/emailProfiles.overview", jwt, params={"workspace_id": a.workspace})
    if ov_code != 200 or not isinstance(ov, dict):
        ov = {"mesuré": False, "raison": f"emailProfiles.overview HTTP {ov_code}"}

    veridian_keys = {k: v for k, v in settings.items() if k.startswith("veridian_")}
    safe_integrations = []
    for i in integrations:
        prov = dict(i.get("email_provider") or i.get("provider") or {})
        smtp = dict(prov.get("smtp") or {})
        if smtp.get("has_password") or "password" in smtp:
            smtp["password"] = _mask_secret("x")
        imap = dict(i.get("imap_settings") or {})
        if imap.get("has_password") or "password" in imap:
            imap["password"] = _mask_secret("x")
        safe_integrations.append({
            "id": i.get("id"), "name": i.get("name"), "type": i.get("type"),
            "kind": prov.get("kind"), "host": (smtp or imap).get("host"),
            "senders": [s.get("email") for s in (prov.get("senders") or [])],
            "smtp": smtp or None, "imap": imap or None,
            "veridian_cold_settings": {k: v for k, v in prov.items() if k.startswith("veridian_")},
        })

    result = {
        "workspace": a.workspace, "env": a.env, "mesuré_le": now.isoformat(),
        "plan_tenant": tenant,
        "workspace_settings": {"name": ws.get("name"), "timezone": settings.get("timezone"),
                               "default_language": settings.get("default_language"),
                               "languages": settings.get("languages"), "veridian": veridian_keys},
        "integrations": safe_integrations,
        "profils_verite_serveur": ov,
        "plafonds_effectifs": caps,
        "file_attente": queue,
        "reputation_par_couple": reputation,
        "listes": lists_view,
        "segments": segments.get("segments") if isinstance(segments, dict) else segments,
        "templates": templates_view,
        "automations": automations_view,
        "webhooks": webhooks.get("webhooks") if isinstance(webhooks, dict) else webhooks,
        "membres": members.get("members") if isinstance(members, dict) else members,
        "contacts_total": cc.get("total_contacts") if isinstance(cc, dict) else cc,
        "contacts_par_classe": breakdown,
    }

    if a.json:
        print(json.dumps(result, indent=2, ensure_ascii=False))
        return

    print(f"=== notifuse config {a.workspace} ({a.env}) — mesuré {now.strftime('%Y-%m-%d %H:%M')}Z ===\n")
    print(f"PLAN/TENANT : {json.dumps(tenant, ensure_ascii=False)}")
    print(f"Contacts : {result['contacts_total']}  |  par classe : {breakdown.get('breakdown') if isinstance(breakdown, dict) else breakdown}\n")
    print(f"Workspace : {ws.get('name')}  tz={settings.get('timezone')}  langue={settings.get('default_language')}")
    for k, v in veridian_keys.items():
        print(f"  {k} = {json.dumps(v, ensure_ascii=False)}")
    print(f"\nIntégrations ({len(safe_integrations)}) :")
    for i in safe_integrations:
        print(f"  [{i['id'][:8]}] {i['name']}  type={i['type']} kind={i['kind']} host={i.get('host')}")
        if i["veridian_cold_settings"]:
            print(f"      cold: {json.dumps(i['veridian_cold_settings'], ensure_ascii=False)}")
    print("\n### PLAFONDS EFFECTIFS (résolution live, cascade intégration > workspace) ###")
    if not caps:
        print("  aucune infra SMTP cold configurée.")
    for c in caps:
        print(f"\n  Intégration {c['name']} [{c['integration_id'][:8]}]")
        print(f"    per_recipient_daily_cap = {c['per_recipient_daily_cap']['value']}  (source: {c['per_recipient_daily_cap']['source']})")
        print(f"    per_sender_daily_cap    = {c['per_sender_daily_cap']['value']}  (source: {c['per_sender_daily_cap']['source']})")
        print(f"    fusible réputation (ralentissement ÷2 dès ce taux, ÷4 dès 2x, par couple domaine x classe, sur rejets durs OU refus politique 7j) = {c['bounce_freeze_threshold']['value']}  (source: {c['bounce_freeze_threshold']['source']}) ; plainte = domaine ralenti ÷4, jamais gelé")
        print(f"    provider_class_daily_cap (source: {c['provider_class_daily_cap']['source']}) = "
              f"{json.dumps(c['provider_class_daily_cap']['value'], ensure_ascii=False)}")
        print(f"    warmup actif={c['warmup']['active']}  cap_du_jour={c['warmup']['cap_today']} "
              f"(démarré {c['warmup']['started_at']}, palier {c['warmup']['schedule']}, step={c['warmup']['step_days']}j)")
        print(f"    fenêtre d'envoi : {c['sending_window']['note']}")
        print(f"    >>> PLAFOND LIMITANT AUJOURD'HUI : {c['plafond_limitant_aujourdhui']['gate']} "
              f"= {c['plafond_limitant_aujourdhui']['valeur']}")
        if c['per_sender_daily_cap']['source'].startswith("workspace"):
            print("    ⚠️  le per-sender vient du WORKSPACE (invisible sur l'écran intégration) : "
                  "un changement ici impacte TOUTES les infras qui n'ont pas leur propre override.")
    print("\n### FUSIBLE RÉPUTATION PAR COUPLE (domaine émetteur x classe destinataire, 7 j glissants) ###")
    print("  Ralentissement progressif (débit de la classe ÷2 puis ÷4), jamais d'arrêt du domaine ; arrêt d'un couple seulement si le fournisseur refuse en bloc (>50 % de 5.7.x sur les 20 derniers envois).")
    if reputation.get("mesuré") is False:
        print(f"  non mesuré : {reputation.get('raison')}")
    for r in (reputation.get("integrations") or []):
        alerte = f" ; ALERTE plainte ({r.get('complaints_7d')}) : domaine ralenti ÷{r.get('domain_slowdown_factor')}" if r.get("alert") else ""
        print(f"\n  {r.get('integration_name')} [{str(r.get('integration_id'))[:8]}] {r.get('sender_domain')} : "
              f"envoyés 7j={r.get('sent_7d')} ; seuil={r.get('hard_bounce_rate_threshold')} "
              f"(réaction dès {r.get('min_sent_for_reaction')} envois par couple){alerte}")
        for c in (r.get("classes") or []):
            if c.get("stopped"):
                etat = f"ARRÊTÉ ({c.get('reason')}, {c.get('recent_policy_refusals')}/{c.get('recent_sent')} derniers en 5.7.x)"
            elif (c.get("slowdown_factor") or 1) > 1:
                etat = f"débit ÷{c.get('slowdown_factor')} ({c.get('reason')})"
            else:
                etat = "normal"
            print(f"      {str(c.get('class')):<20} envoyés={c.get('sent_7d'):<5} "
                  f"durs={c.get('hard_bounce_rate', 0) * 100:5.1f}% politique={c.get('policy_refusal_rate', 0) * 100:5.1f}% -> {etat}")
    print(f"\n### FILE D'ATTENTE (plafonds figés au moment de l'enqueue) ###")
    if queue.get("mesuré"):
        print(f"  {queue['pending_total']} messages en attente, {queue['stale_total']} avec un plafond figé "
              f"DIFFÉRENT du plafond résolu live aujourd'hui.")
        for iid, st in queue.get("par_integration", {}).items():
            print(f"    [{iid[:8]}] pending={st['pending']} stale={st['stale']}")
    else:
        print(f"  non mesuré : {queue.get('raison')}")
    print(f"\nListes ({len(lists_view)}) :")
    for L in lists_view:
        print(f"  {L['name']} [{L['id']}] : {json.dumps(L['stats'], ensure_ascii=False)}")
    print(f"\nSegments : {len(result['segments'] or [])}  |  Templates : {len(templates_view)}  |  "
          f"Automations : {len(automations_view)}  |  Webhooks : {len(result['webhooks'] or [])}  |  "
          f"Membres : {len(result['membres'] or [])}")
    for auto in automations_view:
        print(f"    automation {auto['name']} [{auto['id'][:8]}] status={auto['status']}")


def cmd_env(a):
    """`notifuse env <ws>` : exports shell pour scripts bulk. LECTURE SEULE par
    défaut — utilise exactement le mécanisme existant du CLI (apikey_for) :
    NOTIFUSE_API_KEY de ~/credentials/.all-creds.env si présente, SINON le JWT
    owner (idempotent, zéro mutation, dérivé via provision/auto-login — le
    même chemin que `status`/`settings:get`/`integrations:list`). Mint une
    VRAIE nouvelle clé (workspaces.createAPIKey, mutation réelle : +1 membre
    api_key sur le workspace) UNIQUEMENT avec --mint-new-key explicite."""
    if a.mint_new_key:
        jwt = owner_jwt(a.env, a.workspace)
        body = {"workspace_id": a.workspace, "email_prefix": a.api_key_prefix}
        st, p = call_jwt(a.env, "POST", "/api/workspaces.createAPIKey", jwt, body=body)
        if st not in (200, 201) or not p.get("token"):
            die(f"émission api_key échouée (HTTP {st}) : {p}")
        k = p["token"]
        print(f"# ⚠ api_key FRAÎCHEMENT ÉMISE ({p.get('email')}) — mutation réelle du "
              f"workspace (+1 membre api_key). PAS persistée ici : ajoute-la à "
              f"~/credentials/.all-creds.env si tu veux la réutiliser, sinon "
              f"`notifuse members:remove {a.workspace} --user-id <id>` pour la retirer.",
              file=sys.stderr)
    else:
        k = apikey_for(a.env, a.workspace)  # NOTIFUSE_API_KEY, sinon JWT owner — ZÉRO mutation
    print(f"export NOTIFUSE_API_URL={BASES[a.env]}")
    print(f"export NOTIFUSE_API_KEY={k}")
    print(f"export NOTIFUSE_WORKSPACE={a.workspace}")


# ================================================================ argparse
def build_parser():
    p = argparse.ArgumentParser(
        prog=os.path.basename(sys.argv[0]),
        description="CLI unifié Notifuse Veridian (provision + cold + tout par API).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Détail : ~/.claude/skills/notifuse-cli/SKILL.md",
    )
    p.add_argument("--env", choices=["prod", "staging"], default="prod",
                   help="cible (défaut: prod)")
    p.add_argument("--key", help="(CLI `notifuse` scopé) clé API à utiliser à la place de "
                   "NOTIFUSE_API_KEY — jamais lue depuis ~/credentials/.all-creds.env")
    sub = p.add_subparsers(dest="cmd", required=True)
    # Shim transparent : enregistre chaque nom de commande au moment de sa
    # création, SANS toucher aux ~90 appels existants. C'est ce qui rend le
    # check de couverture auto-cohérent (voir fin de build_parser).
    _orig_add_parser = sub.add_parser
    def _tracking_add_parser(name, *a, **kw):
        _REGISTERED_COMMANDS.add(name)
        return _orig_add_parser(name, *a, **kw)
    sub.add_parser = _tracking_add_parser

    sp = sub.add_parser("campaign:audit",
        help="VERDICT AVANT ENVOI : authentification des domaines, reverse DNS,\n"
             "plafonds, recouvrement de contenu, vivier. Sort en 2 si BLOQUANT.")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_campaign_audit)

    sp = sub.add_parser("doctor", help="self-test health + HMAC + cold-simulate")
    sp.set_defaults(func=cmd_doctor)

    sp = sub.add_parser("provision", help="provisionne un tenant (HMAC)")
    sp.add_argument("tenant_id")
    sp.add_argument("--email", required=True, help="owner email")
    sp.add_argument("--name", help="workspace name (défaut = tenant_id)")
    sp.add_argument("--plan", help="plan initial (défaut free)")
    sp.set_defaults(func=cmd_provision)

    sp = sub.add_parser("wipe", help="wipe tenant(s) de test (HMAC)")
    sp.add_argument("--prefix")
    sp.add_argument("--ids", help="tenant_ids séparés par virgule")
    sp.set_defaults(func=cmd_wipe)

    for name in ("verify", "dry-run"):
        sp = sub.add_parser(name, help="DRY-RUN cold : gates bloqueraient-ils ? ZÉRO mail (staging)")
        sp.add_argument("workspace", nargs="?", default=None)
        sp.add_argument("--cap", type=int, help="cap testé (défaut 1)")
        sp.add_argument("--contact", help="adresse destinataire testée (daily_cap)")
        sp.add_argument("--cls", help="classe testée (class_cap, défaut google)")
        sp.add_argument("--sender", help="sender testé (per_sender_cap)")
        sp.add_argument("--sender-domain", dest="sender_domain", help="domaine émetteur (warmup)")
        sp.add_argument("--tz", help="fallback timezone (sending_window)")
        sp.set_defaults(func=cmd_verify)

    sp = sub.add_parser("status", help="état consolidé d'un workspace")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_status)

    sp = sub.add_parser("breakdown", help="contacts par classe de provider")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--list", help="filtre liste")
    sp.set_defaults(func=cmd_breakdown)

    sp = sub.add_parser("keys:list", help="membres/api_keys du workspace (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_keys_list)

    sp = sub.add_parser("keys:provision", help="émet une api_key (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--prefix", help="email_prefix de l'api_key (défaut cli) → <prefix>@api.<ws>")
    sp.set_defaults(func=cmd_keys_provision)

    sp = sub.add_parser("keys:mint", help="(ADMIN) mint une clé SCOPÉE à un workspace, pour un client")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--role", default="member", choices=["member", "readonly"],
                     help="member (défaut, complet sur ce workspace) | readonly")
    sp.add_argument("--name", help="base de l'email_prefix (slug auto)")
    sp.add_argument("--store", action="store_true",
                     help="range la clé dans ~/credentials/.all-creds.env (NOTIFUSE_API_KEY_<WS>) au lieu de l'imprimer")
    sp.set_defaults(func=cmd_keys_mint)

    sp = sub.add_parser("keys:revoke", help="(ADMIN) révoque une clé API (removeMember, supprime le user api_key)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--user-id", dest="user_id")
    sp.add_argument("--email", help="alternative à --user-id : résolu via workspaces.members")
    sp.set_defaults(func=cmd_keys_revoke)

    # integrations
    sp = sub.add_parser("integrations:list", help="intégrations + config cold par infra")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_integrations_list)

    sp = sub.add_parser("integrations:plan", help="IAC cold : diff déclaré↔réel")
    sp.add_argument("--manifest")
    sp.set_defaults(func=cmd_integrations_plan)

    sp = sub.add_parser("integrations:apply", help="IAC cold : converge")
    sp.add_argument("--manifest")
    sp.add_argument("--yes", action="store_true")
    sp.set_defaults(func=cmd_integrations_apply)

    sp = sub.add_parser("integrations:cold", help="pose rates/caps/exclusion/tracking/pixel sur une infra (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", required=True, help="integration id")
    sp.add_argument("--rates", help='JSON map classe→emails/min, ex \'{"google":0.5}\'')
    sp.add_argument("--daily-cap", dest="daily_cap", help='JSON map classe→envois/jour')
    sp.add_argument("--per-recipient-cap", dest="per_recipient_cap", type=int)
    sp.add_argument("--exclude", help="classes exclues séparées par virgule")
    sp.add_argument("--tracking-domain", dest="tracking_domain")
    sp.add_argument("--pixel", help='JSON map classe→bool')
    sp.add_argument("--bounce-freeze-threshold", dest="bounce_freeze_threshold", type=float,
                    help="seuil du fusible de réputation (proportion de bounces durs sur 7 j, 0.01-0.15, ex 0.08). 0 = défaut 0.03. Une plainte ne gèle pas : elle ralentit le domaine ÷4 pendant 7 jours, sans arrêt.")
    sp.set_defaults(func=cmd_integrations_cold)

    # profils d'envoi : vérité, lien IMAP, pause (lot 2, 08/10/2026)
    sp = sub.add_parser("profiles:overview",
                        help="vérité des profils d'envoi : plafond du jour et porte limitante, reste, fusible, fenêtre, pause, IMAP lié")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--json", action="store_true", help="sortie JSON brute (contrat de l'API)")
    sp.set_defaults(func=cmd_profiles_overview, _routes=["/api/veridian/emailProfiles.overview"])

    sp = sub.add_parser("profiles:link-imap", help="lie un profil d'envoi à sa boîte IMAP de retour (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", required=True, help="id du profil d'envoi")
    sp.add_argument("--imap", help="id de l'intégration IMAP à lier")
    sp.add_argument("--none", action="store_true", help="retire le lien")
    sp.set_defaults(func=cmd_profiles_link_imap, _routes=["/api/workspaces.updateIntegration"])

    sp = sub.add_parser("profiles:set-usage",
                        help="usage exclusif d'un profil : commercial (rotation) | transactional (profil réservé) | unassigned (hors service)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", required=True, help="id du profil d'envoi")
    sp.add_argument("--usage", required=True, choices=["commercial", "transactional", "unassigned"])
    sp.set_defaults(func=cmd_profiles_set_usage, _routes=["/api/veridian/emailProfiles.setUsage"])

    sp = sub.add_parser(
        "profiles:create",
        help="crée un profil d'envoi (SMTP + IMAP de retour, ou Gmail avec mot de passe d'application), atomique (owner)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        description=(
            "Crée un profil d'envoi. Il naît HORS rotation et non vérifié : "
            "profiles:set-usage l'y fait entrer après vérification.\n"
            "Le secret (mot de passe SMTP, ou mot de passe d'application Gmail) se lit sur stdin ou dans un "
            "fichier, jamais en argument, et n'est ni affiché ni loggé."),
        epilog=(
            "Gmail : générez le mot de passe d'application (16 caractères) sur " + GMAIL_APP_PASSWORD_URL + "\n"
            "  (validation en deux étapes requise sur le compte ; Gmail impose smtp.gmail.com:587 et imap.gmail.com:993).\n\n"
            "Exemples :\n"
            "  printf '%s' \"$MOT_DE_PASSE\" | notifuse profiles:create ws --type smtp --name relais-1 --from-email hello@x.fr "
            "--host smtp.x.fr --port 587 --user hello@x.fr --secret-stdin --imap-host imap.x.fr\n"
            "  notifuse profiles:create ws --type gmail-app-password --name gmail-1 --from-email moi@gmail.com "
            "--secret-file ~/.secrets/gmail-1 --daily-cap 30"))
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--type", required=True, choices=["smtp", "gmail-app-password"])
    sp.add_argument("--name", required=True, help="nom du profil")
    sp.add_argument("--from-email", required=True, help="adresse d'expédition")
    sp.add_argument("--from-name", help="nom d'expéditeur (défaut : l'adresse)")
    sp.add_argument("--secret-stdin", action="store_true", help="lit le secret sur stdin (masqué sur un terminal)")
    sp.add_argument("--secret-file", help="lit le secret dans ce fichier (chmod 600)")
    sp.add_argument("--host", help="smtp : hôte SMTP")
    sp.add_argument("--port", type=int, help="smtp : port SMTP (défaut 587)")
    sp.add_argument("--user", help="smtp : identifiant SMTP")
    sp.add_argument("--no-tls", action="store_true", help="smtp : sans TLS")
    sp.add_argument("--imap-host", help="smtp : crée aussi la boîte IMAP de retour liée (même identifiant et même secret par défaut)")
    sp.add_argument("--imap-port", type=int, help="imap : port (défaut 993)")
    sp.add_argument("--imap-user", help="imap : identifiant si différent du SMTP")
    sp.add_argument("--imap-folder", help="imap : dossier (défaut INBOX)")
    sp.add_argument("--imap-secret-file", help="imap : secret différent de celui du SMTP, dans ce fichier")
    sp.add_argument("--account-type", choices=["personal", "workspace"], help="gmail : compte personnel (défaut) ou Workspace")
    sp.add_argument("--daily-cap", type=int, help="gmail : plafond journalier du profil (défaut serveur 30)")
    sp.add_argument("--dry-run", action="store_true", help="affiche le corps de la requête, secrets masqués, sans rien envoyer")
    sp.set_defaults(func=cmd_profiles_create, _routes=["/api/veridian/emailProfiles.create"])

    sp = sub.add_parser("prospection:stats",
                        help="agrégats du tableau de bord de prospection : réponses par séquence et par liste, avancement des séquences, stock")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--start", help="début de fenêtre AAAA-MM-JJ (défaut : tout l'historique)")
    sp.add_argument("--end", help="fin de fenêtre AAAA-MM-JJ, jour inclus")
    sp.set_defaults(func=cmd_prospection_stats, _routes=["/api/veridian/prospection.stats"])

    sp = sub.add_parser("queue:explain",
                        help="pourquoi les mails sont en file : groupes par automation, nœud, raison, profil ; --entry ID pour une entrée et sa dernière décision gate par gate")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--group-by", help="liste séparée par des virgules parmi automation,node,reason,profile,class (défaut serveur : automation,node,reason,profile)")
    sp.add_argument("--automation", help="filtre : id d'automation")
    sp.add_argument("--node", help="filtre : id de nœud")
    sp.add_argument("--reason", help="filtre : code de raison (window_closed, capacity, class_rate, not_examined…)")
    sp.add_argument("--profile", help="filtre : id de profil d'envoi")
    sp.add_argument("--class", dest="klass", help="filtre : classe du fournisseur destinataire (ovh, google…)")
    sp.add_argument("--status", help="filtre : statut de l'entrée")
    sp.add_argument("--entry", help="détail d'UNE entrée et sa dernière décision, gate par gate")
    sp.add_argument("--json", action="store_true", help="sortie JSON brute")
    sp.set_defaults(func=cmd_queue_explain, _routes=["/api/veridian/queue.explain", "/api/veridian/decisions.list"])

    sp = sub.add_parser("logs:decisions",
                        help="journal des décisions du worker (envoyé, reporté, échoué, écarté, sorti, recalculé) ; --trace pour les portes")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--email", help="filtre : e-mail du contact")
    sp.add_argument("--automation", help="filtre : id d'automation")
    sp.add_argument("--node", help="filtre : id de nœud")
    sp.add_argument("--entry", help="filtre : id d'entrée de file")
    sp.add_argument("--reason", help="filtre : code de raison")
    sp.add_argument("--outcome", choices=list(_OUTCOMES), help="filtre : issue de la décision")
    sp.add_argument("--since", help="durée (30m, 2h, 7d) ou date RFC3339")
    sp.add_argument("--limit", type=int, help="nombre de décisions par page (défaut serveur 50, max 200)")
    sp.add_argument("--trace", action="store_true", help="inclut la trace gate par gate")
    sp.add_argument("--all", action="store_true", help="suit next_cursor jusqu'à 5 pages")
    sp.add_argument("--json", action="store_true", help="sortie JSON brute")
    sp.set_defaults(func=cmd_logs_decisions, _routes=["/api/veridian/decisions.list"])

    sp = sub.add_parser("queue:recompute",
                        help="remet des entrées en file au recalcul (next_retry_at et raison effacés) ; sans --yes : affiche et ne fait rien")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--automation", help="id d'automation (obligatoire sauf si --entry)")
    sp.add_argument("--node", help="filtre : id de nœud")
    sp.add_argument("--reason", help="filtre : code de raison")
    sp.add_argument("--profile", help="filtre : id de profil d'envoi")
    sp.add_argument("--entry", action="append", help="id d'entrée (répétable)")
    sp.add_argument("--limit", type=int, required=True, help="nombre maximal d'entrées touchées (1..5000)")
    sp.add_argument("--yes", action="store_true", help="exécute réellement (sans lui : dry-run)")
    sp.set_defaults(func=cmd_queue_recompute, _routes=["/api/veridian/queue.recompute"])

    sp = sub.add_parser("profiles:pause", help="met un profil commercial en pause : le worker bascule sur le reste du pool (refusé sur un transactionnel)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", required=True, help="id du profil d'envoi")
    sp.set_defaults(func=cmd_profiles_pause, _routes=["/api/veridian/emailProfiles.pause"])

    sp = sub.add_parser("profiles:resume", help="relance un profil en pause")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", required=True, help="id du profil d'envoi")
    sp.set_defaults(func=cmd_profiles_resume, _routes=["/api/veridian/emailProfiles.resume"])

    sp = sub.add_parser("integrations:create-smtp", help="crée une intégration SMTP d'envoi (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--name", required=True)
    sp.add_argument("--host", required=True)
    sp.add_argument("--port", type=int, required=True)
    sp.add_argument("--user", required=True)
    password = sp.add_mutually_exclusive_group(required=True)
    password.add_argument("--password", help="secret littéral (déconseillé : visible dans argv)")
    password.add_argument("--password-env", dest="password_env",
                          help="nom de variable dans ~/credentials/.all-creds.env (recommandé)")
    sp.add_argument("--from-email", dest="from_email", required=True)
    sp.add_argument("--from-name", dest="from_name")
    sp.add_argument("--rate", type=int, help="rate_limit_per_minute (défaut 60)")
    sp.add_argument("--no-tls", dest="no_tls", action="store_true")
    sp.set_defaults(func=cmd_integrations_create_smtp)

    sp = sub.add_parser("integrations:create-imap", help="crée une boîte IMAP de retour (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--name", required=True)
    sp.add_argument("--host", required=True)
    sp.add_argument("--port", type=int, required=True)
    sp.add_argument("--user", required=True)
    password = sp.add_mutually_exclusive_group(required=True)
    password.add_argument("--password", help="secret littéral (déconseillé : visible dans argv)")
    password.add_argument("--password-env", dest="password_env",
                          help="nom de variable dans ~/credentials/.all-creds.env (recommandé)")
    sp.add_argument("--folder")
    sp.add_argument("--interval", type=int)
    sp.add_argument("--no-tls", dest="no_tls", action="store_true")
    sp.add_argument("--tls-server-name", dest="tls_server_name",
                    help="nom du certificat à vérifier quand --host est une IP privée (Tailscale)")
    sp.set_defaults(func=cmd_integrations_create_imap)

    sp = sub.add_parser("integrations:create-supabase",
                        help="crée une intégration Supabase (auth hooks, owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--name", required=True)
    sp.add_argument("--email-hook-key", dest="email_hook_key",
                    help="signature_key du Send Email Hook (chiffrée)")
    sp.add_argument("--user-hook-key", dest="user_hook_key",
                    help="signature_key du Before User Created Hook (chiffrée)")
    sp.add_argument("--add-to-lists", dest="add_to_lists",
                    help="list_ids (virgule) où ajouter les users créés")
    sp.add_argument("--custom-json-field", dest="custom_json_field",
                    help="custom_json_1..5 pour user_metadata")
    sp.add_argument("--reject-disposable", dest="reject_disposable", action="store_true")
    sp.set_defaults(func=cmd_integrations_create_supabase)

    sp = sub.add_parser("integrations:create-firecrawl",
                        help="crée une intégration Firecrawl (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--name", required=True)
    sp.add_argument("--api-key", dest="api_key", required=True)
    sp.add_argument("--base-url", dest="base_url", help="endpoint self-hosted (optionnel)")
    sp.set_defaults(func=cmd_integrations_create_firecrawl)

    sp = sub.add_parser("integrations:create-llm",
                        help="crée une intégration LLM anthropic|openai (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--name", required=True)
    sp.add_argument("--provider", required=True, choices=["anthropic", "openai"])
    sp.add_argument("--api-key", dest="api_key", required=True)
    sp.add_argument("--model", help="modèle (REQUIS par l'API ; défaut par provider)")
    sp.add_argument("--base-url", dest="base_url", help="endpoint OpenAI-compatible (openai)")
    sp.set_defaults(func=cmd_integrations_create_llm)

    sp = sub.add_parser("integrations:get", help="détail d'une intégration")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_integrations_get)

    sp = sub.add_parser("integrations:delete", help="supprime une intégration (owner)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_integrations_delete)

    sp = sub.add_parser("integrations:update",
                        help="update générique d'une intégration par type (owner, préserve secrets)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--name", help="renomme (sinon name actuel préservé)")
    sp.add_argument("--settings", help="bloc type-spécifique brut JSON (ou @file) — échappatoire")
    sp.add_argument("--api-key", dest="api_key", help="firecrawl/llm : nouvelle clé")
    sp.add_argument("--model", help="llm : nouveau modèle")
    sp.add_argument("--provider", choices=["anthropic", "openai"], help="llm : kind")
    sp.add_argument("--base-url", dest="base_url")
    sp.add_argument("--email-hook-key", dest="email_hook_key", help="supabase")
    sp.add_argument("--user-hook-key", dest="user_hook_key", help="supabase")
    sp.set_defaults(func=cmd_integrations_update)

    # settings
    sp = sub.add_parser("settings:get", help="dump des settings du workspace (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--key", help="ne sortir qu'une clé")
    sp.set_defaults(func=cmd_settings_get)

    sp = sub.add_parser("settings:set", help="patche UNE clé de settings (merge+update, owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("key", help="clé éditable (voir settings:keys via --help)")
    sp.add_argument("value", help="valeur (JSON pour objets/listes ; true/false ; nombre ; texte)")
    sp.set_defaults(func=cmd_settings_set)

    sp = sub.add_parser("settings:update",
                        help="merge un objet settings (--file/--data) sur l'existant (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--file", help="fichier JSON de settings (patch partiel)")
    sp.add_argument("--data", help="JSON inline de settings (patch partiel)")
    sp.add_argument("--force", action="store_true",
                    help="ignore l'avertissement sur les clés non persistées")
    sp.set_defaults(func=cmd_settings_update)

    # broadcasts
    sp = sub.add_parser("broadcasts:list", help="liste les broadcasts")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v (status, limit…)")
    sp.set_defaults(func=cmd_list, resource="broadcasts")

    sp = sub.add_parser("broadcasts:get", help="détail d'un broadcast")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="broadcasts")

    sp = sub.add_parser("broadcasts:create", help="crée un broadcast (--data @file.json)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_broadcasts_create)

    for act in ("cancel", "pause", "resume", "delete"):
        sp = sub.add_parser(f"broadcasts:{act}", help=f"{act} un broadcast")
        sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
        sp.set_defaults(func=cmd_broadcasts_action, action=act)

    sp = sub.add_parser("broadcasts:schedule", help="planifie un broadcast")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--at", help="ISO datetime ; absent = send_now")
    sp.set_defaults(func=cmd_broadcasts_schedule)

    sp = sub.add_parser("broadcasts:send-test", help="envoi de test (VRAI mail, --real-send)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--email", required=True)
    sp.add_argument("--real-send", dest="real_send", action="store_true")
    sp.set_defaults(func=cmd_broadcasts_send_test)

    # templates
    sp = sub.add_parser("templates:list", help="liste les templates")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v")
    sp.set_defaults(func=cmd_list, resource="templates")

    sp = sub.add_parser("templates:get", help="détail d'un template")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="templates")

    sp = sub.add_parser("templates:create", help="crée un template (--data @file.json)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_templates_create)

    sp = sub.add_parser("templates:delete", help="supprime un template")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_delete, resource="templates")

    sp = sub.add_parser("templates:compile", help="compile un MJML tree → HTML (preview, zéro envoi)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", help="template_id (récupère son visual_editor_tree)")
    sp.add_argument("--tree", help="visual_editor_tree MJML inline JSON ou @file")
    sp.add_argument("--data", help="test_data Liquid JSON (ou @file)")
    sp.add_argument("--body", help="champs body additionnels JSON (ou @file)")
    sp.set_defaults(func=cmd_templates_compile)

    sp = sub.add_parser("templates:push",
                        help="create/update depuis un fichier texte brut, MJML ou HTML")
    sp.add_argument("workspace", nargs="?", default=None)
    source = sp.add_mutually_exclusive_group(required=True)
    source.add_argument("--file", help="fichier .mjml ou .html (contenu réel)")
    source.add_argument("--plain-text-file", dest="plain_text_file",
                        help="fichier .txt envoyé en MIME text/plain pur")
    sp.add_argument("--name", required=True, help="nom du template (≤32)")
    sp.add_argument("--id", help="template_id cible (sinon résolu par name, sinon slug)")
    sp.add_argument("--subject", help="sujet email (défaut = name)")
    sp.add_argument("--subject-preview", dest="subject_preview",
                    help="aperçu de boîte mail explicite ; omettre pour ne rien afficher")
    sp.add_argument("--category", help="catégorie (défaut marketing, ≤20)")
    sp.add_argument("--sender-id", dest="sender_id")
    sp.add_argument("--reply-to", dest="reply_to")
    sp.add_argument("--test-data", dest="test_data", help="test_data Liquid JSON (ou @file)")
    sp.add_argument("--as-html", dest="as_html", action="store_true",
                    help="force le wrap HTML→mj-raw même sans extension .html")
    sp.add_argument("--no-validate", dest="no_validate", action="store_true",
                    help="saute la compilation de validation")
    sp.add_argument("--real-send", dest="real_send", action="store_true",
                    help="(garde-fou) envoi de test réel — voir transactional:send")
    sp.add_argument("--to", help="destinataire pour --real-send")
    sp.set_defaults(func=cmd_templates_push)

    # contacts
    sp = sub.add_parser("contacts:list", help="liste les contacts")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v (limit…)")
    sp.set_defaults(func=cmd_list, resource="contacts")

    sp = sub.add_parser("contacts:count", help="nombre de contacts")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_contacts_count)

    sp = sub.add_parser("contacts:get", help="contact par email")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.set_defaults(func=cmd_contacts_get)

    sp = sub.add_parser("contacts:upsert", help="upsert un contact")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--email"); sp.add_argument("--data", help="objet contact JSON (ou @file)")
    sp.set_defaults(func=cmd_contacts_upsert)

    sp = sub.add_parser("contacts:import", help="import batch (--data tableau JSON, ou --file CSV)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--data", help="tableau JSON de contacts (ou @file.json) -- exclusif avec --file")
    sp.add_argument("--file", help="CSV avec en-tete 'email' (+ colonnes libres) -- exclusif avec --data, importe par lots de %d" % _CONTACTS_IMPORT_BATCH_SIZE)
    sp.add_argument("--lists", help="list_ids séparés par virgule à abonner (subscribe_to_lists)")
    sp.set_defaults(func=cmd_contacts_import)

    sp = sub.add_parser("contacts:delete", help="supprime un contact")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.set_defaults(func=cmd_contacts_delete)

    sp = sub.add_parser("lists:subscribe", help="abonne un contact à des listes")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.add_argument("--lists", required=True, help="list_ids séparés par virgule")
    sp.add_argument("--data", help="champs contact supplémentaires JSON")
    sp.set_defaults(func=cmd_lists_subscribe)

    sp = sub.add_parser("lists:list", help="liste les listes de contacts")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v")
    sp.set_defaults(func=cmd_list, resource="lists")

    sp = sub.add_parser("lists:get", help="détail d'une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="lists")

    sp = sub.add_parser("lists:create", help="crée une liste")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--id", help="id de la liste")
    sp.add_argument("--name", help="nom de la liste")
    sp.add_argument("--description")
    sp.add_argument("--double-optin", dest="double_optin", action="store_true")
    sp.add_argument("--public", action="store_true")
    sp.add_argument("--data", help="objet liste complet JSON (échappatoire)")
    sp.set_defaults(func=cmd_lists_create)

    sp = sub.add_parser("lists:update", help="met à jour une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--name")
    sp.add_argument("--data", help="objet liste JSON (échappatoire)")
    sp.set_defaults(func=cmd_lists_update)

    sp = sub.add_parser("lists:delete", help="supprime une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_delete, resource="lists")

    sp = sub.add_parser("lists:stats", help="stats d'abonnement d'une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_lists_stats)

    # segments
    sp = sub.add_parser("segments:list", help="liste les segments")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v")
    sp.set_defaults(func=cmd_list, resource="segments")

    sp = sub.add_parser("segments:get", help="détail d'un segment")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="segments")

    sp = sub.add_parser("segments:create", help="crée un segment (--data @file : id,name,color,tree,timezone)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_segments_create)

    sp = sub.add_parser("segments:delete", help="supprime un segment")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_delete, resource="segments")

    sp = sub.add_parser("segments:contacts", help="contacts d'un segment")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_segments_contacts)

    sp = sub.add_parser("segments:rebuild", help="recalcule un segment")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_segments_rebuild)

    # members / team
    sp = sub.add_parser("members:list", help="membres du workspace + leurs permissions (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_members_list)

    sp = sub.add_parser("members:get-permissions",
                        help="affiche les permissions d'un membre — read-only (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--user-id", dest="user_id", help="user_id précis (défaut: tous les membres)")
    sp.add_argument("--email", help="filtre par email au lieu du user_id")
    sp.set_defaults(func=cmd_members_get_permissions)

    # Note: par DÉFAUT un membre invité reçoit les PLEINS DROITS (10 ressources
    # read+write). --read-only ou --resources pour restreindre. Sans ça, la route
    # serveur crée un membre SANS permission → dashboard cassé ("insufficient perms").
    sp = sub.add_parser("members:invite",
                        help="invite un membre — PLEINS DROITS par défaut (owner)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.add_argument("--read-only", dest="read_only", action="store_true",
                    help="droits en lecture seule sur les 10 ressources")
    sp.add_argument("--resources", help="restreint aux ressources CSV (ex: contacts,lists,broadcasts)")
    sp.add_argument("--permissions", help="objet UserPermissions JSON brut (ou @file) — override total")
    sp.set_defaults(func=cmd_members_invite)

    sp = sub.add_parser("members:remove", help="retire un membre (owner)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--user-id", dest="user_id", required=True)
    sp.set_defaults(func=cmd_members_remove)

    sp = sub.add_parser("members:permissions",
                        help="(re)définit les permissions d'un membre — PLEINS DROITS par défaut (owner)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--user-id", dest="user_id", required=True)
    sp.add_argument("--read-only", dest="read_only", action="store_true",
                    help="droits en lecture seule sur les 10 ressources")
    sp.add_argument("--resources", help="restreint aux ressources CSV (ex: contacts,lists,broadcasts)")
    sp.add_argument("--permissions", help="objet UserPermissions JSON brut (ou @file) — override total")
    sp.set_defaults(func=cmd_members_permissions)

    sp = sub.add_parser("magic-link", help="génère un magic link de connexion (owner)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--email", help="email cible (défaut owner)")
    sp.set_defaults(func=cmd_magic_link)

    # customEvents
    sp = sub.add_parser("events:list", help="liste les custom events")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v")
    sp.set_defaults(func=cmd_list, resource="customEvents")

    sp = sub.add_parser("events:get", help="détail d'un custom event")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="customEvents")

    sp = sub.add_parser("events:upsert", help="upsert un custom event (--data @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_events_upsert)

    # analytics / messages
    sp = sub.add_parser("analytics:query", help="exécute une requête analytics (--query @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--query", required=True,
                    help="objet Query JSON (ou @file)")
    sp.set_defaults(func=cmd_analytics_query)

    sp = sub.add_parser("analytics:schemas", help="schémas analytics disponibles")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_analytics_schemas)

    sp = sub.add_parser("messages:list", help="historique des messages envoyés")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v (limit, channel…)")
    sp.set_defaults(func=cmd_messages_list)

    # automations
    sp = sub.add_parser("automations:list", help="liste les automations")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--param", action="append", help="filtre k=v (status, list_id…)")
    sp.set_defaults(func=cmd_list, resource="automations")

    sp = sub.add_parser("automations:get", help="détail d'une automation")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="automations")

    sp = sub.add_parser("automations:create", help="crée une automation (--data @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_automations_create)

    for act in ("activate", "pause", "delete"):
        sp = sub.add_parser(f"automations:{act}", help=f"{act} une automation")
        sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
        sp.set_defaults(func=cmd_automations_action, action=act)

    sp = sub.add_parser("automations:update", help="met à jour une automation (--data @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_automations_update)

    sp = sub.add_parser("automations:enroll", help="enrôle des contacts dans une automation live")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--emails", required=True, help="emails séparés par virgule")
    sp.set_defaults(func=cmd_automations_enroll)

    # transactional
    sp = sub.add_parser("transactional:list", help="liste les transactional notifications")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_list, resource="transactional")

    sp = sub.add_parser("transactional:get", help="détail d'une notification")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="transactional")

    sp = sub.add_parser("transactional:create", help="crée une notification (--data @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_transactional_create)

    sp = sub.add_parser("transactional:delete", help="supprime une notification")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_delete, resource="transactional")

    sp = sub.add_parser("transactional:send", help="envoie un mail transactionnel (VRAI mail, --real-send)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True, help="notification id")
    sp.add_argument("--email", required=True)
    sp.add_argument("--data", help="variables Liquid JSON")
    sp.add_argument("--real-send", dest="real_send", action="store_true")
    sp.set_defaults(func=cmd_transactional_send)

    # webhooks
    sp = sub.add_parser("webhooks:list", help="liste les webhook subscriptions")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_list, resource="webhookSubscriptions")

    sp = sub.add_parser("webhooks:get", help="détail d'un webhook")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_get, resource="webhookSubscriptions")

    sp = sub.add_parser("webhooks:create", help="crée un webhook subscription")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--name", required=True)
    sp.add_argument("--url", required=True)
    sp.add_argument("--events", required=True, help="event_types séparés par virgule")
    sp.set_defaults(func=cmd_webhooks_create)

    for act in ("delete", "test", "toggle", "regenerateSecret"):
        sp = sub.add_parser(f"webhooks:{act}", help=f"{act} un webhook")
        sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
        sp.set_defaults(func=cmd_webhooks_action, action=act)

    sp = sub.add_parser("webhooks:deliveries", help="livraisons d'un webhook")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.set_defaults(func=cmd_webhooks_deliveries)

    # admin
    sp = sub.add_parser("admin:tenants", help="listing admin des tenants (HMAC)")
    sp.add_argument("--prefix"); sp.add_argument("--orphans", action="store_true")
    sp.add_argument("--limit", type=int)
    sp.set_defaults(func=cmd_admin_tenants)

    sp = sub.add_parser("admin:grant-unlimited", help="tenant → enterprise illimité (HMAC)")
    sp.add_argument("tenant_id"); sp.add_argument("--reason")
    sp.set_defaults(func=cmd_admin_grant)

    sp = sub.add_parser("admin:gc-orphans", help="GC bases workspace orphelines (HMAC, staging)")
    sp.add_argument("--dry-run", dest="dry_run", action="store_true")
    sp.add_argument("--cap", type=int)
    sp.add_argument("--include-clients", dest="include_clients", action="store_true",
                    help="DANGER: inclut les non-test (défaut: exclus)")
    sp.set_defaults(func=cmd_admin_gc)

    sp = sub.add_parser("admin:stats", help="stats cron cleanup (HMAC, staging)")
    sp.set_defaults(func=cmd_admin_stats)

    sp = sub.add_parser("admin:cold-simulate", help="frappe un prédicat de gate cold (HMAC, staging)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--mode", required=True,
                    help="inbound_reply|seed_sent|daily_cap_decision|class_cap_decision|"
                         "per_sender_cap_decision|warmup_cap_decision|sending_window_decision")
    sp.add_argument("--data", help="champs additionnels JSON (contact_email, cap…)")
    sp.set_defaults(func=cmd_admin_cold_simulate)

    # ============================================================ couverture
    # totale (mission 2026-10-03) — priorité prospection, puis long tail.
    sp = sub.add_parser("contactLists:getByIDs", help="statut d'un contact dans une liste précise")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True); sp.add_argument("--id", required=True, help="list_id")
    sp.set_defaults(func=cmd_contactlists_get_by_ids, _routes=["/api/contactLists.getByIDs"])

    sp = sub.add_parser("contactLists:getContactsByList", help="contacts d'une liste (avec statut)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True, help="list_id")
    sp.set_defaults(func=cmd_contactlists_by_list, _routes=["/api/contactLists.getContactsByList"])

    sp = sub.add_parser("contactLists:getListsByContact", help="listes d'un contact (avec statut)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.set_defaults(func=cmd_contactlists_by_contact, _routes=["/api/contactLists.getListsByContact"])

    sp = sub.add_parser("contactLists:updateStatus", help="change le statut d'un contact sur une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.add_argument("--id", required=True, help="list_id")
    sp.add_argument("--status", required=True, help="active|unsubscribed|bounced|complained...")
    sp.set_defaults(func=cmd_contactlists_update_status, _routes=["/api/contactLists.updateStatus"])

    sp = sub.add_parser("contactLists:removeContact", help="retire un contact d'une liste")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--email", required=True)
    sp.add_argument("--id", required=True, help="list_id")
    sp.set_defaults(func=cmd_contactlists_remove, _routes=["/api/contactLists.removeContact"])

    sp = sub.add_parser("automations:nodeExecutions", help="où un contact est bloqué dans l'automation (par nœud)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True, help="automation_id")
    sp.add_argument("--email", required=True)
    sp.set_defaults(func=cmd_automations_node_executions, _routes=["/api/automations.nodeExecutions"])

    sp = sub.add_parser("templates:update", help="patch direct d'un template (--data @file, sans repasser par templates:push)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--data", required=True, help="champs à modifier JSON (ou @file)")
    sp.set_defaults(func=cmd_templates_update, _routes=["/api/templates.update"])

    sp = sub.add_parser("segments:update", help="met à jour un segment (--data @file : name/color/tree/timezone)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--data", required=True)
    sp.set_defaults(func=cmd_segments_update, _routes=["/api/segments.update"])

    sp = sub.add_parser("segments:preview", help="prévisualise un arbre de segment SANS le créer (--tree @file)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--tree", required=True, help="TreeNode JSON (ou @file)")
    sp.add_argument("--limit", type=int)
    sp.set_defaults(func=cmd_segments_preview, _routes=["/api/segments.preview"])

    sp = sub.add_parser("messages:broadcastStats", help="stats d'envoi d'un broadcast précis")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True, help="broadcast_id")
    sp.set_defaults(func=cmd_messages_broadcast_stats, _routes=["/api/messages.broadcastStats"])

    sp = sub.add_parser("integrations:test-smtp", help="teste le SMTP d'une intégration du WORKSPACE (settings.testSmtp)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True, help="integration_id")
    sp.set_defaults(func=cmd_workspace_test_smtp, _routes=["/api/settings.testSmtp"])

    sp = sub.add_parser("webhooks:update", help="met à jour un webhook subscription")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--name"); sp.add_argument("--url")
    sp.add_argument("--events", help="event_types CSV")
    sp.add_argument("--enabled", type=lambda v: v.lower() in ("1", "true", "yes"))
    sp.add_argument("--data", help="objet additionnel JSON (ex custom_event_filters)")
    sp.set_defaults(func=cmd_webhooks_update, _routes=["/api/webhookSubscriptions.update"])

    sp = sub.add_parser("webhooks:eventTypes", help="liste les event_types disponibles pour un webhook")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.set_defaults(func=cmd_webhooks_event_types, _routes=["/api/webhookSubscriptions.eventTypes"])

    sp = sub.add_parser("events:import", help="import batch de custom events (--data tableau JSON ou --file)")
    sp.add_argument("workspace", nargs="?", default=None)
    grp = sp.add_mutually_exclusive_group(required=True)
    grp.add_argument("--data"); grp.add_argument("--file")
    sp.set_defaults(func=cmd_events_import, _routes=["/api/customEvents.import"])

    sp = sub.add_parser("contacts:get-by-external-id", help="contact par external_id")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--external-id", dest="external_id", required=True)
    sp.set_defaults(func=cmd_contacts_get_by_external_id, _routes=["/api/contacts.getByExternalID"])

    sp = sub.add_parser("transactional:update", help="met à jour une notification transactionnelle (--data champs à changer)")
    sp.add_argument("workspace", nargs="?", default=None); sp.add_argument("--id", required=True)
    sp.add_argument("--data", required=True, help="objet 'updates' JSON (ou @file)")
    sp.set_defaults(func=cmd_transactional_update, _routes=["/api/transactional.update"])

    sp = sub.add_parser("transactional:testTemplate", help="envoie un test de template transactional (VRAI mail)")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--template-id", dest="template_id", required=True)
    sp.add_argument("--integration-id", dest="integration_id", required=True)
    sp.add_argument("--sender-id", dest="sender_id", required=True)
    sp.add_argument("--email", required=True, help="destinataire du test")
    sp.add_argument("--language", default="en")
    sp.add_argument("--options", help="EmailOptions JSON additionnel (ou @file)")
    sp.set_defaults(func=cmd_transactional_test_template, _routes=["/api/transactional.testTemplate"])

    # ---- long tail générique (resource.verb) ----
    for rv, method, name, needs_ws in GENERIC_RESOURCE_ROUTES:
        sp = sub.add_parser(name, help=f"{rv} ({method}, générique couverture totale)")
        if needs_ws:
            sp.add_argument("workspace", nargs="?", default=None)
        else:
            sp.add_argument("--workspace", help="workspace de référence (JWT owner) si la route en a besoin")
        sp.add_argument("--id", help="id de la ressource visée (si applicable)")
        sp.add_argument("--param", action="append", help="query param k=v (GET)")
        sp.add_argument("--set", action="append", help="champ body k=v simple (POST)")
        sp.add_argument("--data", help="body JSON complet (ou @file) — prioritaire sur --set")
        sp.set_defaults(func=cmd_generic_rv, _rv=rv, _method=method, _routes=[f"/api/{rv}"])

    # ---- agent.exchangeToken : bespoke (reponse text/plain, pas JSON) ----
    sp = sub.add_parser("agent:exchange-token",
        help="/api/agent.exchangeToken (POST, PUBLIC -- le jeton EST le secret)")
    sp.add_argument("--data", required=True, help="""body JSON '{"token":"<jeton>"}' """)
    sp.set_defaults(func=cmd_agent_exchange_token, _routes=["/api/agent.exchangeToken"])

    # ---- long tail générique (chemin brut, placeholders {id}/{tenantId}) ----
    for name, method, path, auth, placeholders in RAW_ROUTES:
        sp = sub.add_parser(name, help=f"{path} ({method}, générique couverture totale, auth={auth})")
        if auth == "owner":
            sp.add_argument("--workspace", help="tenant de référence pour obtenir un JWT owner")
        if placeholders:
            sp.add_argument("--id", required=True, help="substitue {" + placeholders[0] + "}")
        sp.add_argument("--param", action="append", help="query param k=v (GET)")
        sp.add_argument("--data", help="body JSON (ou @file)")
        sp.set_defaults(func=cmd_raw_generic, _path=path, _method=method, _auth=auth,
                        _placeholders=placeholders, _routes=[path])

    # ---- notifuse config <workspace> : config complète pixel ----
    sp = sub.add_parser("config", help="configuration COMPLÈTE d'un workspace, lisible (+ --json), avec PLAFONDS EFFECTIFS")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--json", action="store_true", help="sortie JSON brute au lieu du texte lisible")
    sp.add_argument("--no-queue-check", dest="no_queue_check", action="store_true",
                    help="saute le comptage des messages en file aux plafonds figés (psql, lecture seule)")
    sp.set_defaults(func=cmd_config)

    # ---- notifuse env <workspace> : exports shell pour scripts bulk ----
    sp = sub.add_parser("env", help="imprime des 'export NOTIFUSE_...' pour scripter en bulk (eval \"$(notifuse env ws)\")")
    sp.add_argument("workspace", nargs="?", default=None)
    sp.add_argument("--mint-new-key", dest="mint_new_key", action="store_true",
                    help="⚠ MUTATION réelle : émet une VRAIE nouvelle api_key (+1 membre). "
                         "Sans ce flag : lecture seule (NOTIFUSE_API_KEY existante, sinon JWT owner).")
    sp.add_argument("--api-key-prefix", dest="api_key_prefix", default="bulk",
                    help="préfixe de la nouvelle clé si --mint-new-key (défaut: bulk)")
    sp.set_defaults(func=cmd_env)

    # ---- commandes internes (coverage) ----
    sp = sub.add_parser("__routes", help="(interne) dump JSON des routes couvertes par une commande dédiée")
    sp.set_defaults(func=lambda a: print(json.dumps(sorted({r for rs in COMMAND_ROUTES.values() for r in rs}))))

    sp = sub.add_parser("__commands", help="(interne) dump JSON des noms de commandes réellement enregistrés")
    sp.set_defaults(func=lambda a: print(json.dumps(sorted(_REGISTERED_COMMANDS))))

    sp = sub.add_parser("__workspace-commands",
        help="(interne) dump JSON de WORKSPACE_COMMANDS (ce que `notifuse` user expose)")
    sp.set_defaults(func=lambda a: print(json.dumps(sorted(WORKSPACE_COMMANDS))))

    sp = sub.add_parser("__admin-only-route-markers",
        help="(interne) dump JSON de ADMIN_ONLY_ROUTE_MARKERS")
    sp.set_defaults(func=lambda a: print(json.dumps(list(ADMIN_ONLY_ROUTE_MARKERS))))

    sp = sub.add_parser("__command-routes",
        help="(interne) dump JSON {commande: [routes]} (COMMAND_ROUTES complet)")
    sp.set_defaults(func=lambda a: print(json.dumps(COMMAND_ROUTES)))

    # ---- fusion + auto-vérification anti-dérive (cf en-tête du fichier) ----
    for _name, _routes in EXISTING_COMMAND_ROUTES.items():
        if _name not in _REGISTERED_COMMANDS:
            die(f"BUG couverture : EXISTING_COMMAND_ROUTES référence la commande "
                f"'{_name}' qui n'est pas enregistrée dans argparse (renommée/retirée ?).")
        COMMAND_ROUTES.setdefault(_name, []).extend(_routes)
    # Les sp.set_defaults(_routes=[...]) ci-dessus posent une valeur par défaut
    # sur le NAMESPACE au parsing, pas dans COMMAND_ROUTES directement : on les
    # récupère ici en relisant les defaults de chaque sous-parser.
    for _name, _sp in sub.choices.items():
        _r = _sp.get_default("_routes")
        if _r:
            COMMAND_ROUTES.setdefault(_name, []).extend(_r)

    # escape hatch
    sp = sub.add_parser("api", help="tape n'importe quelle route (auth auto)")
    sp.add_argument("method")
    sp.add_argument("resource", help="resource.verb (ex contacts.count) ou path /api/...")
    sp.add_argument("--workspace")
    sp.add_argument("--param", action="append", help="query param k=v (GET)")
    sp.add_argument("--data", help="body JSON inline ou @file")
    sp.add_argument("--auth", choices=["auto", "hmac", "owner", "apikey", "none"],
                    help="forcer l'auth (défaut auto)")
    sp.set_defaults(func=cmd_api)

    return p


def _setup_scoped_mode(args):
    """Appelé uniquement par le point d'entrée `notifuse` (user). Résout la
    clé scopée (--key > NOTIFUSE_API_KEY du PROCESS ENV, jamais la clé admin
    éventuellement présente dans ~/credentials/.all-creds.env), bascule
    owner_jwt()/apikey_for() en mode scopé, et refuse côté client tout
    --workspace différent de celui connu pour cette clé (NOTIFUSE_WORKSPACE —
    le JWT api_key Notifuse ne porte PAS de claim workspace, cf auth_service.go
    UserClaims : {user_id, email, type}, donc pas de lecture possible dans le
    jeton — on documente ce choix plutôt que de prétendre le contraire)."""
    global _SCOPED_MODE, _SCOPED_JWT
    _SCOPED_MODE = True
    _SCOPED_JWT = getattr(args, "key", None) or os.environ.get(APIKEY_VAR)

    home_ws = os.environ.get("NOTIFUSE_WORKSPACE")
    requested_ws = getattr(args, "workspace", None)
    if home_ws and requested_ws and requested_ws != home_ws:
        die(f"refusé côté client : clé scopée pour le workspace '{home_ws}', "
            f"commande demandée sur '{requested_ws}'. Mint une clé pour ce "
            f"workspace avec `notifuse-admin keys:mint {requested_ws}`.")
    # === Patch 2026-10-04 (audit skill distribué) ===
    # Le positional `workspace` est desormais optionnel (nargs="?") sur toutes
    # les WORKSPACE_COMMANDS : un client dont la cle est scopee a UN seul
    # workspace (le cas normal, `NOTIFUSE_WORKSPACE` ecrit par install.sh) n'a
    # pas a le retaper a chaque commande. `notifuse lists:list` marche
    # desormais aussitot apres l'install, sans positional -- c'etait
    # l'exemple meme du skill AGENTS.md distribue (rc=2 avant ce correctif).
    if requested_ws in (None, "") and hasattr(args, "workspace"):
        if home_ws:
            args.workspace = home_ws
        else:
            die("workspace manquant : passe-le en argument, ou exporte "
                "NOTIFUSE_WORKSPACE (ecrit automatiquement par install.sh).")


def main(mode="admin"):
    """mode='admin' -> notifuse-admin (superset historique, super clé).
    mode='user'    -> notifuse (scopé à un seul workspace, sa propre api_key)."""
    args = build_parser().parse_args()

    if mode == "user":
        if args.cmd not in WORKSPACE_COMMANDS:
            die(f"commande '{args.cmd}' refusée par `notifuse` (CLI utilisateur scopé) : "
                f"admin, HMAC ou cross-workspace. Utilise `notifuse-admin {args.cmd}`.")
        _setup_scoped_mode(args)
    elif getattr(args, "workspace", None) in (None, ""):
        # Mode admin (super clé) : AUCUN fallback implicite sur NOTIFUSE_WORKSPACE
        # -- une super clé agit cross-workspace, deviner la cible serait
        # dangereux. Le positional reste donc strictement requis ici (c'est
        # le mode user, scopé a UN SEUL workspace par construction, qui
        # beneficie du fallback -- cf. _setup_scoped_mode).
        if hasattr(args, "workspace"):
            die(f"commande '{args.cmd}' : workspace requis en argument "
                f"(pas de fallback implicite en mode admin/super-clé).")

    try:
        args.func(args)
    except SystemExit:
        raise
    except json.JSONDecodeError as e:
        die(f"JSON invalide en entrée : {e}")
    except KeyboardInterrupt:
        die("interrompu", code=130)
    except Exception as e:
        # Erreur LISIBLE, jamais de stacktrace Python brute (contrainte IA-first).
        die(f"{type(e).__name__}: {e}")


if __name__ == "__main__":
    main(mode="admin")
