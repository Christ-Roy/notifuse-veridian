#!/usr/bin/env python3
"""Moteur de DIFF idempotent : état déclaré (manifeste résolu) vs état réel (API).

Brique du CLI notifuse-iac.sh. Reçoit :
  - argv[1] : manifeste RÉSOLU (JSON, secrets en clair OU masqués selon le mode
    du wrapper — ici on ne LOG jamais les valeurs sensibles, on calcule juste
    des présences/diffs de structure).
  - stdin   : état réel du workspace (réponse JSON de /api/workspaces.get).

Émet sur stdout un PLAN JSON : liste d'actions { kind, op, name, reason }.
  op ∈ {create, update, noop}
  kind ∈ {integration_email, integration_imap, workspace_settings}

🔴 Idempotence : l'identité stable d'une intégration = son `name`. Une
intégration déclarée dont le name existe déjà côté réel → update (jamais
recreate, on garde l'ID + l'historique). Absente → create. Le diff de settings
compare champ par champ les clés cold (rates/caps/pixel) que l'IAC gouverne.

Ce module NE FAIT AUCUN appel réseau et NE RÉVÈLE AUCUN secret : il ne compare
que des structures (présence/égalité), et pour les passwords il compare une
EMPREINTE de présence, pas la valeur (le réel ne renvoie jamais le password en
clair de toute façon). Il décide quoi faire ; le wrapper bash exécute.
"""
import json
import sys

# Clés cold gouvernées par l'IAC au niveau workspace settings. On ne touche QUE
# celles-là (le reste des settings du workspace n'est pas dans le périmètre IAC).
COLD_SETTINGS_KEYS = [
    "veridian_provider_class_rates",
    "veridian_provider_class_daily_cap",
    "veridian_per_recipient_daily_cap",
    "veridian_open_pixel_by_class",
]


def _norm(v):
    """Normalise pour comparaison stable (None/0/{} équivalents = 'non posé')."""
    if v in (None, {}, [], 0, ""):
        return None
    return v


def diff_workspace_settings(declared_cold, real_settings):
    """Compare les clés cold déclarées vs réelles. Retourne (op, reason, target)."""
    target = {}
    changes = []

    # Mapping manifeste cold_outreach → clés settings.
    mapping = {
        "provider_class_rates": "veridian_provider_class_rates",
        "provider_class_daily_cap": "veridian_provider_class_daily_cap",
        "per_recipient_daily_cap": "veridian_per_recipient_daily_cap",
        "open_pixel_by_class": "veridian_open_pixel_by_class",
    }
    for man_key, set_key in mapping.items():
        if man_key not in declared_cold:
            continue
        want = _norm(declared_cold[man_key])
        have = _norm(real_settings.get(set_key))
        target[set_key] = declared_cold[man_key]
        if want != have:
            changes.append(set_key)

    if not target:
        return "noop", "aucune clé cold déclarée", {}
    if not changes:
        return "noop", "settings cold déjà conformes", target
    return "update", "diff sur: " + ", ".join(changes), target


def find_integration(real_integrations, name):
    for i in real_integrations or []:
        if i.get("name") == name:
            return i
    return None


def diff_email_integration(declared, real):
    """declared = {name, smtp:{host,port,use_tls}, senders:[...], tracking, cold...}.
    Retourne (op, reason)."""
    if real is None:
        return "create", "intégration absente"

    ep = real.get("email_provider") or {}
    reasons = []

    # SMTP host/port/tls.
    rsmtp = ep.get("smtp") or {}
    if declared.get("smtp"):
        for k in ("host", "port", "use_tls"):
            want = _norm(declared["smtp"].get(k))
            have = _norm(rsmtp.get(k))
            if want != have:
                reasons.append("smtp.%s" % k)

    # Senders : on compare l'ensemble des emails (l'identité d'un sender = email).
    want_senders = sorted(s.get("email", "") for s in declared.get("senders", []))
    have_senders = sorted(s.get("email", "") for s in ep.get("senders") or [])
    if want_senders != have_senders:
        reasons.append("senders")

    # Tracking domain.
    if _norm(declared.get("veridian_tracking_domain")) != _norm(ep.get("veridian_tracking_domain")):
        reasons.append("tracking_domain")

    # Cold rates/caps PAR INFRA (posés sur l'EmailProvider).
    for k in ("veridian_provider_class_rates", "veridian_provider_class_daily_cap",
              "veridian_per_recipient_daily_cap"):
        if _norm(declared.get(k)) != _norm(ep.get(k)):
            reasons.append(k)

    if not reasons:
        return "noop", "intégration conforme"
    return "update", "diff sur: " + ", ".join(reasons)


def diff_imap_integration(declared, real):
    if real is None:
        return "create", "boîte IMAP absente"
    s = real.get("imap_settings") or {}
    reasons = []
    for k in ("host", "port", "username", "use_tls", "folder", "polling_interval_seconds"):
        want = _norm(declared.get(k))
        have = _norm(s.get(k))
        if want != have:
            reasons.append(k)
    if not reasons:
        return "noop", "boîte IMAP conforme"
    return "update", "diff sur: " + ", ".join(reasons)


def main(argv):
    if len(argv) != 2:
        sys.stderr.write("usage: plan.py <manifest_resolved.json>  (real state on stdin)\n")
        return 2

    with open(argv[1], "r", encoding="utf-8") as fh:
        man = json.load(fh)

    real_resp = json.load(sys.stdin)
    real_ws = (real_resp or {}).get("workspace") or {}
    real_integrations = real_ws.get("integrations") or []
    real_settings = real_ws.get("settings") or {}

    actions = []

    # 1. Intégrations d'envoi SMTP.
    for si in man.get("sending_integrations", []):
        name = si["id"]  # le `id` du manifeste = le `name` stable côté API
        real = find_integration(real_integrations, name)
        smtp = si.get("smtp", {})
        declared = {
            "name": name,
            "smtp": {
                "host": smtp.get("host"),
                "port": int(smtp["port"]) if smtp.get("port") not in (None, "") else None,
                "use_tls": smtp.get("use_tls", True),
            },
            "senders": si.get("senders", []),
            "veridian_tracking_domain": si.get("veridian_tracking_domain"),
        }
        # Cold rates/caps par infra (depuis cold_outreach, posés sur cette infra).
        cold = man.get("cold_outreach", {})
        if "provider_class_rates" in cold:
            declared["veridian_provider_class_rates"] = cold["provider_class_rates"]
        if "provider_class_daily_cap" in cold:
            declared["veridian_provider_class_daily_cap"] = cold["provider_class_daily_cap"]
        if "per_recipient_daily_cap" in cold:
            declared["veridian_per_recipient_daily_cap"] = cold["per_recipient_daily_cap"]
        op, reason = diff_email_integration(declared, real)
        actions.append({
            "kind": "integration_email", "op": op, "name": name, "reason": reason,
            "existing_id": (real or {}).get("id"),
        })

    # 2. Boîte IMAP de retour.
    ri = man.get("reply_inbox")
    if ri:
        imap = ri.get("imap", {})
        name = "Return inbox (cold bounce/reply)"
        real = find_integration(real_integrations, name)
        declared = {
            "host": imap.get("host"),
            "port": int(imap["port"]) if imap.get("port") not in (None, "") else None,
            "username": imap.get("username"),
            "use_tls": imap.get("use_tls", True),
            "folder": imap.get("folder", "INBOX"),
            "polling_interval_seconds": imap.get("polling_interval_seconds"),
        }
        op, reason = diff_imap_integration(declared, real)
        actions.append({
            "kind": "integration_imap", "op": op, "name": name, "reason": reason,
            "existing_id": (real or {}).get("id"),
        })

    # 3. Settings cold au niveau workspace (fallback global).
    cold = man.get("cold_outreach", {})
    if cold:
        op, reason, target = diff_workspace_settings(cold, real_settings)
        actions.append({
            "kind": "workspace_settings", "op": op, "name": "cold_outreach",
            "reason": reason, "target": target,
        })

    json.dump({"actions": actions}, sys.stdout, ensure_ascii=False)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
