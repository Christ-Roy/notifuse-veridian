#!/usr/bin/env python3
# Outil durable (26/11/2026, VER-166, fiche 97) : épreuve réelle des domaines d'envoi en PROD, dans un espace jetable du Hub.
"""Déclare un domaine de test À NOUS (sous-domaine de veridian.site, zone Cloudflare), publie ses enregistrements par l'API
Cloudflare, le fait vérifier par la route, puis envoie UN vrai mail par le relais agence, From sur ce domaine, vers une adresse
neuve de messagerie-nord-776.fr portée par le même espace (MX = Notifuse). Lit les en-têtes reçus : dkim=pass header.d=<domaine>,
dmarc=pass, spf. Aucun tiers : expéditeur et destinataire sont à nous et jetables.

Usage : domaine-envoi-prod.py --workspace <id> --real-send [--reprendre etat.json] [--nettoyer]
        domaine-envoi-prod.py --workspace <id> --real-send --reprendre etat.json --perte    (après un premier passage vert)
        domaine-envoi-prod.py --workspace <id> --reprendre etat.json --retour
--perte : revérification à l'usage. Le dernier contrôle du domaine d'épreuve est reculé de 25 h (vieillir-domaine-envoi.sh), son TXT
DKIM retiré, puis UN envoi : le domaine doit passer non vérifié (lost_at, lost_reason=missing) ; un second envoi part sans notre signature.
--retour : TXT republié, vérification par la route, le constat est effacé.
Sortie 0 = prouvé, 1 = écart, 2 = non abouti. Les accès SMTP et le jeton DNS sont lus dans le coffre, jamais affichés."""
import argparse, json, os, re, subprocess, sys, tempfile, time, urllib.parse, urllib.request

DOMAIN = "essai-envoi.veridian.site"
BOX_DOM = "messagerie-nord-776.fr"
CLASSES = ("corporate", "corporate_selfhost", "other_hoster", "google", "microsoft", "orange", "free", "sfr", "laposte", "ovh", "ionos", "yahoo", "apple", "gmx")
ap = argparse.ArgumentParser()
ap.add_argument("--workspace", required=True)
ap.add_argument("--real-send", action="store_true")
ap.add_argument("--reprendre")
ap.add_argument("--nettoyer", action="store_true", help="retire profils, domaine et enregistrements DNS de l'épreuve")
ap.add_argument("--perte", action="store_true", help="épreuve de la revérification à l'usage (clé retirée)")
ap.add_argument("--retour", action="store_true", help="republie la clé et revérifie")
a = ap.parse_args()
WS = a.workspace
etat_path = a.reprendre or os.path.join(tempfile.gettempdir(), f"domaine_envoi_{int(time.time())}.json")
E = json.load(open(etat_path)) if a.reprendre else {"stamp": str(int(time.time()))[-6:], "preuves": []}
S = E["stamp"]
FROM, BOX = f"essai-{S}@{DOMAIN}", f"essai-boite-{S}@{BOX_DOM}"
ecarts = []


def vault():
    out = {}
    for line in open(os.path.expanduser("~/credentials/.all-creds.env")):
        m = re.match(r'^(SMTP_AGENCE_(?:USER3|PW3|HOST|PORT)|CF_API_TOKEN|CF_ZONE_ID)=(.*)$', line.rstrip("\n"))
        if m:
            out[m.group(1)] = m.group(2).strip().strip('"').strip("'")
    return out


V = vault()


def save():
    json.dump(E, open(etat_path, "w"), indent=1, ensure_ascii=False)


def preuve(etape, ok, detail):
    print(("OK   " if ok else "ÉCART"), etape, "|", detail)
    E["preuves"].append({"etape": etape, "ok": ok, "detail": detail, "a": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())})
    if not ok:
        ecarts.append(etape)
    save()


def api(method, res, body=None, check=True, **params):
    args = ["notifuse-admin", "--env", "prod", "api", method, res, "--workspace", WS]
    tmp = None
    if body is not None:  # corps par fichier 0600 : un mot de passe ne passe jamais en argument de processus
        fd, tmp = tempfile.mkstemp(prefix="dom-", suffix=".json")
        with os.fdopen(fd, "w") as f:
            json.dump(body, f)
        args += ["--data", "@" + tmp]
    for k, v in params.items():
        args += ["--param", f"{k}={v}"]
    try:
        p = subprocess.run(args, capture_output=True, text=True, timeout=300)
    finally:
        if tmp:
            os.unlink(tmp)
    try:
        out = json.loads(p.stdout)
    except ValueError:
        out = {"_texte": (p.stdout + p.stderr)[-400:], "_rc": p.returncode}
    if check and p.returncode != 0:
        print(f"NON ABOUTI {method} {res} : {json.dumps(out, ensure_ascii=False)[:400]}")
        save(); sys.exit(2)
    return out


def cf(method, path, body=None):
    req = urllib.request.Request(f"https://api.cloudflare.com/client/v4/zones/{V['CF_ZONE_ID']}{path}", method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": f"Bearer {V['CF_API_TOKEN']}", "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def txt_put(name, value):
    """Pose un TXT sous le domaine d'épreuve. Jamais en dehors, jamais d'écrasement d'une autre valeur."""
    assert name == DOMAIN or name.endswith("." + DOMAIN), name
    found = cf("GET", "/dns_records?" + urllib.parse.urlencode({"name": name, "type": "TXT"}))["result"]
    if any(r["content"].strip('"') == value for r in found):
        return "déjà posé"
    if found and not name.endswith("._domainkey." + DOMAIN):
        raise SystemExit(f"NON ABOUTI : {name} porte déjà un autre TXT, rien n'est écrasé")
    r = cf("POST", "/dns_records", {"type": "TXT", "name": name, "content": value, "ttl": 60, "proxied": False, "comment": "VER-166 épreuve"})
    return "posé" if r.get("success") else f"ÉCHEC {r.get('errors')}"


def txt_del_all():
    q = urllib.parse.urlencode({"name.endswith": DOMAIN, "type": "TXT", "per_page": 100})
    for r in cf("GET", "/dns_records?" + q)["result"]:
        if r["name"] == DOMAIN or r["name"].endswith("." + DOMAIN):
            cf("DELETE", f"/dns_records/{r['id']}")
            print("DNS retiré :", r["name"])


def nettoyer():
    if E.get("domain_id"):
        print("domaine :", api("POST", "/api/veridian/sendingDomains.delete", {"workspace_id": WS, "id": E["domain_id"]}, check=False))
    w = api("GET", "/api/workspaces.get", id=WS)["workspace"]
    s = w["settings"]
    for k in ("marketing_email_provider_id", "transactional_email_provider_id"):
        if s.get(k) in (E.get("integ_envoi"), E.get("integ_boite")):
            s[k] = ""
    api("POST", "/api/workspaces.update", {"id": WS, "name": w["name"], "settings": s}, check=False)
    for k in ("integ_envoi", "integ_boite"):
        if E.get(k):
            print("profil", k, api("POST", "/api/workspaces.deleteIntegration", {"workspace_id": WS, "integration_id": E[k]}, check=False).get("_rc", "retiré"))
    txt_del_all()


def domaine():
    return next((d for d in api("GET", "/api/veridian/sendingDomains.list", workspace_id=WS)["domains"] if d["id"] == E["domain_id"]), None)


def etat(d):
    return {k: d.get(k) for k in ("status", "last_checked_at", "verified_at", "lost_at", "lost_reason")} | {
        "dkim": next(((c["verdict"]) for c in d.get("checks") or [] if c["kind"] == "dkim"), None), "lu_a": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}


def dkim_rec():
    return next(r for r in E["records"] if r["kind"] == "dkim")


def recu(subject, minutes=20):
    t0 = time.time()
    while time.time() - t0 < minutes * 60:
        time.sleep(20)
        l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", limit=20)
        m = next((m for m in (l.get("messages") if isinstance(l, dict) else l) or [] if subject in (m.get("subject") or "")), None)
        if m:
            raw = subprocess.run(["notifuse-admin", "--env", "prod", "mail:raw", WS, "--id", str(m["id"])], capture_output=True, text=True, timeout=120).stdout
            head = re.split(r"\r?\n\r?\n", raw, 1)[0]
            return {"id": m["id"], "ar": [" ".join(x.split()) for x in re.findall(r"(?im)^Authentication-Results: mx\.veridian\.site.*(?:\r?\n[ \t].*)*", head)],
                    "dkim_d": [x.lower() for x in re.findall(r"(?ims)^DKIM-Signature:.*?\bd=([^;\s]+)", head)],
                    "message_id": (re.search(r"(?im)^Message-ID:\s*(\S+)", head) or [None, None])[1]}
        print(f"  +{int(time.time() - t0)} s : {subject} pas encore reçu")
    return None


def envoyer(subject):
    r = api("POST", "/api/veridian/mail.messages.send", {"workspace_id": WS, "profile_id": E["integ_envoi"], "to": BOX, "subject": subject,
                                                          "text": f"Bonjour,\n\népreuve {S} de VER-166 : {subject}.\n"}, check=False)
    if not r.get("message_id"):
        print("NON ABOUTI envoi :", r); save(); sys.exit(2)
    return {"message_id": r["message_id"], "subject": subject, "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}


if a.perte:
    if not a.real_send:
        print("NON ABOUTI : --perte envoie de vrais mails, il exige --real-send"); sys.exit(2)
    P = E.setdefault("perte", {})
    if not P.get("avant"):
        d = domaine()
        if not d or d["status"] != "verified":
            print("NON ABOUTI : le domaine d'épreuve doit être vérifié avant --perte"); sys.exit(2)
        P["avant"] = etat(d); save()
        out = subprocess.run([os.path.join(os.path.dirname(os.path.abspath(__file__)), "vieillir-domaine-envoi.sh"), E["domain_id"], WS],
                             capture_output=True, text=True, timeout=600).stdout.strip()
        P["vieilli"] = out[-200:]; save()
        preuve("dernier contrôle reculé de 25 h (base prod, ligne d'épreuve)", E["domain_id"] in out, out[-200:])
    if not P.get("txt_retire"):
        q = urllib.parse.urlencode({"name": dkim_rec()["name"], "type": "TXT"})
        for r in cf("GET", "/dns_records?" + q)["result"]:
            cf("DELETE", f"/dns_records/{r['id']}")
        P["txt_retire"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()); save()
        print("TXT DKIM retiré, attente de l'expiration des caches (TTL 60 s)")
        time.sleep(150)
    if not P.get("envoi_declencheur"):
        P["envoi_declencheur"] = envoyer(f"Perte {S} : envoi qui déclenche le contrôle"); save()
    if not P.get("apres"):
        t0, d = time.time(), None
        while time.time() - t0 < 900:
            d = domaine()
            if d["status"] != "verified":
                break
            print(f"  +{int(time.time() - t0)} s : encore vérifié (l'envoi attend peut-être sa place dans la file)")
            time.sleep(20)
        P["apres"] = etat(d); save()
        preuve("non vérifié au premier envoi suivant (lost_at, lost_reason=missing)",
               d["status"] == "pending" and bool(d.get("lost_at")) and d.get("lost_reason") == "missing", json.dumps(P["apres"]))
    if not P.get("envoi_suivant"):
        P["envoi_suivant"] = envoyer(f"Perte {S} : envoi suivant, sans signature"); save()
    if not P.get("recu_suivant"):
        r = recu(P["envoi_suivant"]["subject"])
        P["recu_suivant"] = r; save()
        preuve("l'envoi suivant part sans notre signature", bool(r) and DOMAIN not in (r or {}).get("dkim_d", []), json.dumps(r, ensure_ascii=False)[:400])
    print("\nRÉSULTAT :", "VERT" if not ecarts else "ROUGE " + ", ".join(ecarts), "| état", etat_path)
    sys.exit(1 if ecarts else 0)

if a.retour:
    P = E.setdefault("perte", {})
    rec = dkim_rec()
    print("DNS", rec["name"], ":", txt_put(rec["name"], rec["value"]))
    t0 = time.time()
    while time.time() - t0 < 600:
        d = api("POST", "/api/veridian/sendingDomains.verify", {"workspace_id": WS, "id": E["domain_id"]})["domain"]
        if d["status"] == "verified":
            break
        time.sleep(30)
    P["retour"] = etat(d); save()
    preuve("clé republiée, vérifiée par la route, constat effacé", d["status"] == "verified" and not d.get("lost_at"), json.dumps(P["retour"]))
    sys.exit(1 if ecarts else 0)

if a.nettoyer:
    nettoyer(); save(); sys.exit(0)
if not a.real_send:
    print("NON ABOUTI : l'envoi réel exige --real-send"); sys.exit(2)
print("état :", etat_path, "| From", FROM, "| boîte", BOX)
print("prod :", subprocess.run(["notifuse-admin", "--env", "prod", "version"], capture_output=True, text=True).stdout.strip().replace("\n", " "))

# 1. Profils : l'expéditeur (From sur le domaine d'épreuve) et la boîte d'épreuve, tous deux par le relais agence.
if not E.get("integ_envoi"):
    if not V.get("SMTP_AGENCE_USER3") or not V.get("SMTP_AGENCE_PW3"):
        print("NON ABOUTI : accès du relais agence introuvables"); sys.exit(2)
    for key, addr, nom in (("integ_envoi", FROM, f"essai-domaine-{S}"), ("integ_boite", BOX, f"essai-boite-{S}")):
        r = api("POST", "/api/workspaces.createIntegration", {"workspace_id": WS, "name": nom, "type": "email", "provider": {
            "kind": "smtp", "smtp": {"host": V.get("SMTP_AGENCE_HOST", "smtp.agence-veridian.fr"), "port": int(V.get("SMTP_AGENCE_PORT", "587")),
                                     "username": V["SMTP_AGENCE_USER3"], "password": V["SMTP_AGENCE_PW3"], "use_tls": True},
            "rate_limit_per_minute": 10, "veridian_profile_daily_cap": 3,
            "veridian_sending_window": {"start_hour": 0, "end_hour": 24, "timezone": "UTC"},
            "veridian_provider_class_rates": {k: 10 for k in CLASSES}, "veridian_provider_class_daily_cap": {k: 3 for k in CLASSES},
            "senders": [{"id": "s1", "email": addr, "name": "Veridian Essai", "is_default": True}]}})
        E[key] = r["integration_id"]; save()
    preuve("profils créés (relais agence, plafond 3/jour)", True, f"{E['integ_envoi']}, {E['integ_boite']}")

# 2. Domaine déclaré, enregistrements publiés, vérifié par la route (lecture DNS réelle).
if not E.get("domain_id"):
    r = api("POST", "/api/veridian/sendingDomains.create", {"workspace_id": WS, "domain": DOMAIN})
    d = r["domain"]
    E["domain_id"], E["records"] = d["id"], d["records"]; save()
    preuve("domaine déclaré, clé générée", d["status"] == "pending" and len(d["records"]) == 3 and "private" not in json.dumps(r).lower(),
           f"{d['domain']} sélecteur {d['selector']}")
for rec in E["records"]:
    print("DNS", rec["kind"], rec["name"], ":", txt_put(rec["name"], rec["value"]))
if not E.get("verified"):
    t0, d = time.time(), None
    while time.time() - t0 < 600:
        d = api("POST", "/api/veridian/sendingDomains.verify", {"workspace_id": WS, "id": E["domain_id"]})["domain"]
        if d["status"] == "verified" and d.get("complete"):
            break
        print(f"  +{int(time.time() - t0)} s :", [(c["kind"], c["verdict"]) for c in d.get("checks") or []])
        time.sleep(30)
    E["verified"] = {"status": d["status"], "complete": d.get("complete"), "checks": [(c["kind"], c["verdict"], c["detail"]) for c in d["checks"]],
                     "verified_at": d.get("verified_at")}; save()
    preuve("vérifié par la route (DKIM, SPF, DMARC)", d["status"] == "verified" and bool(d.get("complete")), str(E["verified"]["checks"]))
    if d["status"] != "verified":
        raise SystemExit(1)
    time.sleep(65)  # cache du signataire : une minute au plus

# 3. ENVOI RÉEL : From sur le domaine d'épreuve, vers la boîte d'épreuve.
if not E.get("envoi"):
    subj = f"Épreuve domaine d'envoi {S}"
    r = api("POST", "/api/veridian/mail.messages.send", {"workspace_id": WS, "profile_id": E["integ_envoi"], "to": BOX, "subject": subj,
                                                          "text": f"Bonjour,\n\nceci est l'épreuve {S} de VER-166 : signature DKIM au nom de {DOMAIN}.\n"}, check=False)  # 202 « en file » : le CLI sort en non-zéro, le message_id fait foi
    E["envoi"] = {"reponse": r, "subject": subj, "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}; save()
    preuve("envoi mis en file", bool(r.get("message_id")), json.dumps(r, ensure_ascii=False)[:250])

# 4. Réception par le MX Notifuse, en-têtes lus.
if not E.get("recu"):
    t0, recu = time.time(), None
    while time.time() - t0 < 1200 and not recu:
        time.sleep(20)
        l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", limit=20)
        items = l.get("messages") if isinstance(l, dict) else l
        recu = next((m for m in items or [] if E["envoi"]["subject"] in (m.get("subject") or "")), None)
        if not recu:
            q = api("GET", "/api/veridian/queue.explain", group_by="reason", check=False)
            print(f"  +{int(time.time() - t0)} s : pas encore reçu ; file : {[(g.get('reason'), g.get('count')) for g in q.get('groups') or []]}")
    if not recu:
        preuve("réception", False, "rien reçu en 20 min"); sys.exit(1)
    E["recu"] = recu.get("id"); save()
raw = subprocess.run(["notifuse-admin", "--env", "prod", "mail:raw", WS, "--id", str(E["recu"])], capture_output=True, text=True, timeout=120).stdout
eml = etat_path.replace(".json", "_recu.eml")
open(eml, "w").write(raw)
head = re.split(r"\r?\n\r?\n", raw, 1)[0]
ar = [" ".join(x.split()) for x in re.findall(r"(?im)^Authentication-Results:.*(?:\r?\n[ \t].*)*", head)]
sigs = [m.group(1).lower() for m in re.finditer(r"(?ims)^DKIM-Signature:.*?\bd=([^;\s]+)", head)]
mid = re.search(r"(?im)^Message-ID:\s*(\S+)", head)
date = re.search(r"(?im)^Date:\s*(.+)$", head)
ours = next((x for x in ar if "mx.veridian.site" in x), "")
E["entetes"] = {"authentication_results": ar, "dkim_d": sigs, "message_id": mid and mid.group(1), "date": date and date.group(1).strip(), "eml": eml}
save()
print("Authentication-Results :", ours or ar)
preuve("dkim=pass header.d=" + DOMAIN, bool(re.search(r"dkim=pass[^;]*header\.d=" + re.escape(DOMAIN) + r"(\s|;|$)", ours)), ours)
preuve("dmarc=pass", "dmarc=pass" in ours, ours)
preuve("spf=pass", "spf=pass" in ours, ours)
print("\nRÉSULTAT :", "VERT" if not ecarts else "ROUGE " + ", ".join(ecarts), "| état", etat_path)
sys.exit(1 if ecarts else 0)
