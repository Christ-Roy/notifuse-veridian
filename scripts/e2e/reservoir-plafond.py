#!/usr/bin/env python3
"""Épreuve staging (fiche 85) : un import massif et une grosse activation ne font pas partir un mail de plus que le plafond.

Espace jetable, profil SMTP plafonné à CAP par jour, branché sur le puits local de l'allocation staging (127.0.0.1:1025, aucun
mail dehors). On importe 600 contacts, on en active 500, on rejoue tout, on attend, et on compte les mails RÉELLEMENT envoyés
(message_history) : exactement CAP, le reste en file pour cause de plafond. Témoin : un second espace à 2 x CAP en envoie 2 x CAP
(la mesure bouge quand le paramètre bouge).

Usage : reservoir-plafond.py [--cap 50] [--keep]    Dépend de notifuse-admin (env staging seulement)."""
import argparse, json, subprocess, sys, time

ap = argparse.ArgumentParser()
ap.add_argument("--cap", type=int, default=50)
ap.add_argument("--keep", action="store_true")
a = ap.parse_args()
CLASSES = ("corporate", "corporate_selfhost", "other_hoster")  # example.com est reclassé d'après son MX
STAMP = str(int(time.time()))[-7:]


def cli(*args, check=True):
    p = subprocess.run(["notifuse-admin", "--env", "staging", *args], capture_output=True, text=True, timeout=180)
    if check and p.returncode != 0:
        sys.exit(f"ÉCHEC {' '.join(args[:3])} : {p.stdout[-400:]} {p.stderr[-400:]}")
    try:
        return json.loads(p.stdout)
    except ValueError:
        return {"_texte": p.stdout + p.stderr, "_rc": p.returncode}


def api(ws, method, res, body=None, check=True, **params):
    args = ["api", method, res, "--workspace", ws]
    if body is not None:
        args += ["--data", json.dumps(body)]
    for k, v in params.items():
        args += ["--param", f"{k}={v}"]
    return cli(*args, check=check)


ok = True


def verdict(nom, cond, detail):
    global ok
    ok = ok and bool(cond)
    print(("OK    " if cond else "ÉCHEC ") + nom + " : " + str(detail), flush=True)


def prepare(ws, cap, usage):
    cli("provision", ws, "--email", f"gf-{ws}@e2e.veridian.site")
    integ = api(ws, "POST", "/api/workspaces.createIntegration", {"workspace_id": ws, "name": "puits", "type": "email", "provider": {
        "kind": "smtp", "smtp": {"host": "127.0.0.1", "port": 1025, "use_tls": False}, "rate_limit_per_minute": 600,
        "veridian_profile_daily_cap": cap, "veridian_provider_class_rates": {c: 600 for c in CLASSES}, "veridian_provider_class_daily_cap": {c: 5000 for c in CLASSES},
        "senders": [{"id": "s1", "email": f"bot-{STAMP}@agences-veridian.fr", "name": "Bot", "is_default": True}]}})["integration_id"]
    w = api(ws, "GET", "/api/workspaces.get", id=ws)["workspace"]
    vu = [i for i in w["integrations"] if i["id"] == integ][0]["email_provider"]
    if vu["smtp"]["host"] != "127.0.0.1":
        sys.exit("le profil ne vise pas le puits local : rien n'est envoyé")
    verdict(f"{ws} profil plafonné", vu.get("veridian_profile_daily_cap") == cap, vu.get("veridian_profile_daily_cap"))
    if usage:  # l'usage commercial exige un transport vérifié : le mail d'essai part dans le puits local
        cli("integrations:verify", ws, "--id", integ, "--to", "essai@example.com")
        api(ws, "POST", "/api/veridian/emailProfiles.setUsage", {"workspace_id": ws, "integration_id": integ, "usage": usage})
    w = api(ws, "GET", "/api/workspaces.get", id=ws)["workspace"]
    if not usage:
        w["settings"]["marketing_email_provider_id"] = w["settings"]["transactional_email_provider_id"] = integ
        api(ws, "POST", "/api/workspaces.update", {"id": ws, "name": w["name"], "settings": w["settings"]})
    for l in ("reserve", "sequence"):
        api(ws, "POST", "/api/lists.create", {"workspace_id": ws, "id": l, "name": l, "is_double_optin": False, "is_public": False})
    tree = {"id": "root", "type": "mjml", "attributes": {"version": "4.0.0"}, "children": [{"id": "b", "type": "mj-body", "children": [
        {"id": "s", "type": "mj-section", "children": [{"id": "c", "type": "mj-column", "children": [{"id": "t", "type": "mj-text", "content": "Bonjour {{ contact.email }}"}]}]}]}]}
    api(ws, "POST", "/api/templates.create", {"workspace_id": ws, "id": "tpl", "name": "tpl", "channel": "email", "category": "marketing",
                                              "email": {"sender_id": "s1", "subject": "Essai {{ contact.email }}", "visual_editor_tree": tree}})
    api(ws, "POST", "/api/automations.create", {"workspace_id": ws, "automation": {
        "id": "seq", "workspace_id": ws, "name": "seq", "status": "draft", "list_id": "sequence", "root_node_id": "t",
        "trigger": {"event_kind": "list.subscribed", "list_id": "sequence", "frequency": "once"},
        "nodes": [{"id": "t", "automation_id": "seq", "type": "trigger", "config": {}, "next_node_id": "e"},
                  {"id": "e", "automation_id": "seq", "type": "email", "config": {"template_id": "tpl"}}]}})
    api(ws, "POST", "/api/automations.activate", {"workspace_id": ws, "automation_id": "seq"})


def lot(ws, debut, n):
    return {"workspace_id": ws, "list_ids": ["reserve"], "dedupe": ["email", "siren"], "contacts": [
        {"email": f"c{i}-{STAMP}@example.com", "siren": f"{500000000 + i}", "custom_string_5": "corporate"} for i in range(debut, debut + n)]}


def envoyes(ws):
    p = api(ws, "GET", "/api/veridian/prospection.stats")
    return sum(s["sent"] for q in p.get("sequences", []) for s in q.get("stages", []))


def joue(ws, cap, usage):
    prepare(ws, cap, usage)
    r = api(ws, "POST", "/api/veridian/contacts.intake", lot(ws, 0, 600))
    verdict(f"{ws} import", (r.get("created"), r.get("subscribed")) == (600, 600), r)
    r = api(ws, "POST", "/api/veridian/contacts.intake", lot(ws, 0, 600))
    verdict(f"{ws} rejeu de l'import", (r.get("created"), r.get("existing"), r.get("subscribed")) == (0, 600, 0), r)
    act = {"workspace_id": ws, "automation_id": "seq", "list_id": "reserve", "limit": 500, "batch_id": "lot-1"}
    r1 = api(ws, "POST", "/api/veridian/reservoir.activate", act)
    r2 = api(ws, "POST", "/api/veridian/reservoir.activate", act)
    verdict(f"{ws} activation", r1.get("enrolled") == 500, {k: r1.get(k) for k in ("selected", "enrolled", "remaining")})
    verdict(f"{ws} rejeu de l'activation", r2.get("replayed") is True and r2.get("remaining") == 100, {k: r2.get(k) for k in ("replayed", "enrolled", "remaining")})


def attendre(ws, cap):
    vu, stable = -1, 0
    for _ in range(90):  # 15 min au plus, stable 3 minutes
        n = envoyes(ws)
        stable = stable + 1 if n == vu and n > 0 else 0
        vu = n
        if stable >= 18:
            break
        time.sleep(10)
    q = api(ws, "GET", "/api/veridian/queue.explain", group_by="reason")
    verdict(f"{ws} mails partis", vu == cap, f"{vu} (plafond {cap})")
    o = api(ws, "GET", "/api/veridian/emailProfiles.overview")["profiles"][0]["plan"]
    verdict(f"{ws} profil", o["sent_today"] == cap, {k: o.get(k) for k in ("sent_today", "remaining_today", "daily_cap_today", "limiting_gate")})
    print("      file :", json.dumps(q)[:700], flush=True)


# Le troisième : profil qui sert aussi aux mails transactionnels (usage non déclaré) ; le plafond doit tenir quand même.
ESPACES = [(f"gfresa{STAMP}", a.cap, "commercial"), (f"gfresb{STAMP}", 2 * a.cap, "commercial"), (f"gfresc{STAMP}", a.cap, "")]
try:
    for w, c, u in ESPACES:
        joue(w, c, u)
    for w, c, u in ESPACES:
        attendre(w, c)
finally:
    if not a.keep:
        cli("wipe", "--ids", ",".join(w for w, _, _ in ESPACES), check=False)
print("VERT" if ok else "ROUGE")
sys.exit(0 if ok else 1)
