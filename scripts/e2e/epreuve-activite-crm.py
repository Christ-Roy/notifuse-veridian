#!/usr/bin/env python3
# Outil durable (27/11/2026, VER-267) : épreuve de l'activité Mail vers le CRM (fiche 98) sur un espace RÉEL, sans rien y dérégler.
"""Un envoi et une réponse humaine entre deux adresses d'épreuve neuves, portées par deux profils d'épreuve de l'espace visé ;
le worker de l'outbox pousse au Hub un `message.sent` puis un `message.received`, que le CRM range sur la fiche de la boîte d'épreuve.

Différences avec parcours-mail-prod.py, qui vise une org jetable :
  - aucun réglage de l'espace n'est touché (ni fournisseur marketing, ni transactionnel) : l'envoi part par `mail:send --profile` ;
  - ni contact importé, ni liste, ni modèle, ni séquence ; seuls deux profils d'épreuve sont créés, plafonnés à 3 envois par jour ;
  - le nettoyage ne retire QUE ces deux profils (relus par leur id) ; le journal des messages reste, c'est la preuve.
Aucune boîte existante de l'espace n'est lue ni utilisée : la réception et la réponse sont lues par l'adresse d'épreuve seule.
Aucun mail vers un tiers : l'expéditeur est sur agence-veridian.fr (relais agence, DKIM aligné), la boîte et le retour sur
messagerie-nord-776.fr (MX Notifuse, servi à l'espace visé par VERIDIAN_MX_DOMAINS).

Usage : epreuve-activite-crm.py --ws robertbrunon --real-send [--stamp v267a] [--nom "Épreuve VER-267"] [--reprendre etat.json]
        epreuve-activite-crm.py --ws robertbrunon --nettoyer-seulement --reprendre etat.json
Sortie 0 = envoi et réponse prouvés côté Mail, 1 = écart, 2 = non abouti. La ligne CRM se lit ensuite (hub open --app crm)."""
import argparse, json, os, re, subprocess, sys, time

DOM = "messagerie-nord-776.fr"
CLASSES = ("corporate", "corporate_selfhost", "other_hoster", "google", "microsoft", "orange", "free", "sfr", "laposte", "ovh", "ionos", "yahoo", "apple", "gmx")
HERE = os.path.dirname(os.path.abspath(__file__))
ap = argparse.ArgumentParser()
ap.add_argument("--ws", required=True, help="espace Mail visé (doit être dans VERIDIAN_ACTIVITE_WORKSPACES)")
ap.add_argument("--real-send", action="store_true")
ap.add_argument("--nettoyer-seulement", action="store_true")
ap.add_argument("--reprendre")
ap.add_argument("--stamp", help="suffixe des adresses d'épreuve (minuscules et chiffres)")
ap.add_argument("--nom", default="Épreuve VER-267", help="nom affiché de la boîte d'épreuve (celui de la fiche CRM)")
ap.add_argument("--refus", action="store_true", help="en plus : un envoi vers un domaine .invalid (jamais résolu, rien ne sort) dont l'issue doit revenir « failed » avec la raison")
a = ap.parse_args()
if not re.fullmatch(r"[a-z0-9]{1,40}", a.ws) or (a.stamp and not re.fullmatch(r"[a-z0-9]{1,20}", a.stamp)):
    print("NON ABOUTI : --ws et --stamp en minuscules et chiffres"); sys.exit(2)
WS = a.ws
etat_path = a.reprendre or os.path.join(HERE, f"parcours_crm_{int(time.time())}.json")
E = json.load(open(etat_path)) if a.reprendre else {"ws": WS, "stamp": a.stamp or str(int(time.time()))[-6:], "preuves": []}
if E.get("ws") != WS:
    print(f"NON ABOUTI : l'état {etat_path} vise {E.get('ws')}, pas {WS}"); sys.exit(2)
S = E["stamp"]
ENVOI, BOITE, RETOUR = f"epreuve-envoi-{S}@agence-veridian.fr", f"epreuve-boite-{S}@{DOM}", f"epreuve-retour-{S}@{DOM}"
OBJET = f"{a.nom} : envoi {S}"
ecarts = []


def save():
    json.dump(E, open(etat_path, "w"), indent=1, ensure_ascii=False)


def cli(*args, check=True):
    p = subprocess.run(["notifuse-admin", "--env", "prod", *args], capture_output=True, text=True, timeout=300)
    if check and p.returncode != 0:
        print(f"NON ABOUTI {' '.join(args[:3])} : {p.stdout[-500:]} {p.stderr[-300:]}")
        save(); sys.exit(2)
    try:
        return json.loads(p.stdout)
    except ValueError:
        return {"_texte": (p.stdout + p.stderr)[-500:], "_rc": p.returncode}


def api(method, res, body=None, check=True, **params):
    args = ["api", method, res, "--workspace", WS]
    if body is not None:
        args += ["--data", json.dumps(body)]
    for k, v in params.items():
        args += ["--param", f"{k}={v}"]
    return cli(*args, check=check)


def preuve(etape, ok, detail):
    print(("OK   " if ok else "ÉCART"), etape, "|", detail)
    E["preuves"].append({"etape": etape, "ok": ok, "detail": detail, "a": time.strftime("%H:%M:%SZ", time.gmtime())})
    if not ok:
        ecarts.append(etape)
    save()


def creds():
    out = {}
    for line in open(os.path.expanduser("~/credentials/.all-creds.env")):
        m = re.match(r'^(SMTP_AGENCE_(?:USER3|PW3|HOST|PORT))=(.*)$', line.rstrip("\n"))
        if m:
            out[m.group(1)] = m.group(2).strip().strip('"').strip("'")
    return out


def messages(**kw):
    l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", limit=50, **kw)
    return (l.get("messages") if isinstance(l, dict) else l) or []


def reglages():
    s = api("GET", "/api/workspaces.get", id=WS)["workspace"]["settings"]
    return {k: s.get(k) for k in ("marketing_email_provider_id", "transactional_email_provider_id")}


def nettoyer():
    """Retire les deux profils d'épreuve, et eux seuls (par leur id). Vérifie que les réglages d'envoi de l'espace n'ont pas bougé."""
    for k in ("integ_envoi", "integ_boite"):
        if E.get(k):
            print("suppression profil", k, ":", api("POST", "/api/workspaces.deleteIntegration", {"workspace_id": WS, "integration_id": E[k]}, check=False).get("_rc", "ok"))
    left = [i["id"] for i in api("GET", "/api/workspaces.get", id=WS)["workspace"].get("integrations") or [] if i["id"] in (E.get("integ_envoi"), E.get("integ_boite"))]
    preuve("nettoyage : plus aucun profil d'épreuve", not left, f"{len(left)} restant(s)")
    if E.get("reglages_avant"):
        apres = reglages()
        preuve("réglages d'envoi de l'espace inchangés", apres == E["reglages_avant"], f"avant {E['reglages_avant']} après {apres}")


if a.nettoyer_seulement:
    nettoyer(); sys.exit(1 if ecarts else 0)
if not a.real_send:
    print("NON ABOUTI : l'envoi réel exige --real-send"); sys.exit(2)
print("état :", etat_path, "| expéditeur", ENVOI, "| boîte d'épreuve", BOITE, f"« {a.nom} »")
E["prod"] = (cli("version").get("git_sha") or "?"); save()
print("prod :", E["prod"])

try:
    if "reglages_avant" not in E:
        E["reglages_avant"] = reglages(); save()
    # 1. Deux profils d'épreuve, relais agence, 3 envois par jour au plus.
    if not E.get("integ_envoi"):
        c = creds()
        if not c.get("SMTP_AGENCE_USER3") or not c.get("SMTP_AGENCE_PW3"):
            print("NON ABOUTI : accès SASL du relais agence introuvables"); sys.exit(2)
        for key, senders, nom, affiche in (("integ_envoi", [("s1", ENVOI, True), ("s2", RETOUR, False)], f"epreuve-envoi-{S}", "Veridian Épreuve"),
                                            ("integ_boite", [("s1", BOITE, True)], f"epreuve-boite-{S}", a.nom)):
            r = api("POST", "/api/workspaces.createIntegration", {"workspace_id": WS, "name": nom, "type": "email", "provider": {
                "kind": "smtp", "smtp": {"host": c.get("SMTP_AGENCE_HOST", "smtp.agence-veridian.fr"), "port": int(c.get("SMTP_AGENCE_PORT", "587")),
                                         "username": c["SMTP_AGENCE_USER3"], "password": c["SMTP_AGENCE_PW3"], "use_tls": True},
                "rate_limit_per_minute": 10, "veridian_profile_daily_cap": 3,
                "veridian_sending_window": {"start_hour": 0, "end_hour": 24, "timezone": "UTC"},
                "veridian_provider_class_rates": {k: 10 for k in CLASSES}, "veridian_provider_class_daily_cap": {k: 3 for k in CLASSES},
                "senders": [{"id": i, "email": ad, "name": affiche, "is_default": d} for i, ad, d in senders]}})
            E[key] = r["integration_id"]; save()
        preuve("deux profils d'épreuve créés, réglages de l'espace intacts", reglages() == E["reglages_avant"], str({k: E[k] for k in ("integ_envoi", "integ_boite")}))

    # 2. Envoi réel par le profil d'épreuve, vers la boîte d'épreuve.
    if not E.get("envoye"):
        E["avant"] = sorted({str(m.get("id")) for m in messages()} | {str(m.get("id")) for m in messages(kind="reply_human")}); save()
        t0 = time.time()
        r = cli("mail:send", WS, "--profile", E["integ_envoi"], "--to", BOITE, "--subject", OBJET,
                "--text", f"Bonjour, ceci est l'envoi de l'épreuve {S} (VER-267), entre deux adresses d'épreuve.", "--yes", "--json", check=False)
        ok = r.get("_rc") in (None, 0) and "error" not in json.dumps(r).lower()
        E["envoye"] = {"a": time.strftime("%H:%M:%SZ", time.gmtime()), "duree_s": round(time.time() - t0, 1), "reponse": r}; save()
        preuve("envoi parti tout de suite (issue « sent » rendue par l'API)", ok and r.get("status") == "sent", json.dumps(r, ensure_ascii=False)[:250])
        if not ok:
            raise SystemExit

    # 3. Réception dans la boîte d'épreuve (MX Notifuse).
    if not E.get("recu"):
        t0, recu = time.time(), None
        while time.time() - t0 < 900 and not recu:
            time.sleep(20)
            recu = next((m for m in messages() if BOITE in json.dumps(m).lower() and S in json.dumps(m, ensure_ascii=False) and str(m.get("id")) not in E["avant"]), None)
            if not recu:
                print(f"  +{int(time.time() - t0)} s : pas encore reçu")
        if not recu:
            preuve("réception dans la boîte d'épreuve", False, "rien reçu en 15 min"); raise SystemExit
        E["recu"] = recu.get("id"); save()
        preuve("reçu dans la boîte d'épreuve", True, f"message {E['recu']}, objet {recu.get('subject')!r}")

    # 4. Réponse humaine depuis la boîte d'épreuve, vers l'adresse de retour de l'expéditeur.
    if not E.get("reponse"):
        r = cli("mail:reply", WS, "--id", str(E["recu"]), "--profile", E["integ_boite"], "--to", RETOUR,
                "--text", f"Bonjour, réponse humaine de l'épreuve {S} (VER-267).", "--yes", "--json", check=False)
        ok = r.get("_rc") in (None, 0) and "error" not in json.dumps(r).lower()
        E["reponse"] = {"a": time.strftime("%H:%M:%SZ", time.gmtime()), "reponse": r}; save()
        preuve("réponse envoyée depuis la boîte d'épreuve", ok, json.dumps(r, ensure_ascii=False)[:250])
        if not ok:
            raise SystemExit
    if not E.get("reponse_recue"):
        t0, rr = time.time(), None
        while time.time() - t0 < 900 and not rr:
            time.sleep(20)
            rr = next((m for m in messages(kind="reply_human") if BOITE in json.dumps(m).lower() and str(m.get("id")) not in E["avant"]), None)
            if not rr:
                print(f"  +{int(time.time() - t0)} s : réponse pas encore reçue")
        preuve("réponse humaine reçue, classée reply_human", bool(rr), json.dumps({k: (rr or {}).get(k) for k in ("id", "kind", "from", "from_name", "subject")}, ensure_ascii=False))
        E["reponse_recue"] = rr and rr.get("id"); save()
    # 5. --refus : le mail écrit à la main part tout de suite et l'API rend son issue réelle (27/11) ; un domaine .invalid
    # (RFC 2606, jamais résolu) est refusé avant tout SMTP par le pré-filtre : rien ne sort, l'issue doit dire pourquoi.
    if a.refus and not E.get("refus"):
        t0 = time.time()
        r = cli("mail:send", WS, "--profile", E["integ_envoi"], "--to", f"epreuve-refus-{S}@domaine-absent-{S}.invalid", "--subject", f"{a.nom} : refus {S}",
                "--text", "Envoi d'épreuve vers un domaine qui n'existe pas.", "--yes", "--json", check=False)
        E["refus"] = {"a": time.strftime("%H:%M:%SZ", time.gmtime()), "duree_s": round(time.time() - t0, 1), "reponse": r}; save()
        preuve("envoi refusé : issue rendue tout de suite, avec la raison", r.get("status") == "failed" and bool(r.get("error")),
               json.dumps({k: r.get(k) for k in ("status", "error", "note")}, ensure_ascii=False) + f" en {E['refus']['duree_s']} s")
finally:
    if not a.reprendre or E.get("reponse_recue"):
        nettoyer()
    save()
    print("RÉSULTAT", json.dumps({"stamp": S, "prod": E.get("prod"), "boite": BOITE, "envoye": (E.get("envoye") or {}).get("a"),
                                  "recu": E.get("recu"), "reponse_recue": E.get("reponse_recue"), "ecarts": ecarts}, ensure_ascii=False))
sys.exit(1 if ecarts else 0)
