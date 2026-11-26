#!/usr/bin/env python3
# Outil durable (26/11/2026) : épreuve réelle du parcours Mail en prod. Changer WS/HUB_TENANT pour une org jetable vivante du Hub. Fiche 84.
"""Parcours Mail complet en PROD dans un espace jetable du Hub (WS et HUB_TENANT ci-dessous) (26/11, objectif de la parente) :
contacts importés, segment, modèle avec désinscription, séquence, ENVOI RÉEL vers une adresse NEUVE de notre domaine
(messagerie-nord-776.fr, MX = Notifuse), réception lue avec ses en-têtes, réponse humaine depuis la boîte d'épreuve,
arrêt de la séquence sur réponse, désinscription en un clic, puis retrait de tout ce qui peut envoyer.

Aucun mail ne sort vers un tiers : expéditeur ET destinataire sont deux adresses neuves de messagerie-nord-776.fr, portées
par cet espace seul. Envoi réel seulement avec --real-send (règle 3 du dépôt ; GO de la parente, brief du 26/11).
Usage : parcours_mail_prod.py --real-send [--ws <espace> --hub-tenant <tenant>] [--stamp lot2611] [--ouvrir] [--autre-nom "Nom"] [--reprendre etat.json]
Sortie 0 = parcours prouvé, 1 = écart, 2 = non abouti."""
import argparse, json, os, re, subprocess, sys, time, urllib.request, urllib.error

WS = "e2esso1ibnvwkiz8edd1"  # org jetable PROD du Hub (tenant d7ee68e0-1635-4845-b04a-2076eb0caceb, née le 26/11 10:57Z, tenue 25 h), jusqu'au « fini »
HUB_TENANT = "d7ee68e0-1635-4845-b04a-2076eb0caceb"
DOM = "messagerie-nord-776.fr"
CLASSES = ("corporate", "corporate_selfhost", "other_hoster", "google", "microsoft", "orange", "free", "sfr", "laposte", "ovh", "ionos", "yahoo", "apple", "gmx")
HERE = os.path.dirname(os.path.abspath(__file__))
ap = argparse.ArgumentParser()
ap.add_argument("--real-send", action="store_true")
ap.add_argument("--reprendre")
ap.add_argument("--nettoyer-seulement", action="store_true")
ap.add_argument("--ws", help="espace de l'org jetable (sinon WS ci-dessus)")
ap.add_argument("--hub-tenant", help="tenant de l'org jetable (sinon HUB_TENANT ci-dessus)")
ap.add_argument("--ouvrir", action="store_true", help="ouvre le mail reçu (pixel) et suit son lien suivi, comme un lecteur : message.opened et clicked")
ap.add_argument("--autre-nom", help="réponse humaine de plus depuis une adresse neuve (parcours-autre-<stamp>@), absente du CRM, sous ce nom affiché")
ap.add_argument("--stamp", help="suffixe imposé des adresses d'épreuve (ex. lot2611, quand le Hub a déjà posé la personne au CRM)")
a = ap.parse_args()
WS, HUB_TENANT = a.ws or WS, a.hub_tenant or HUB_TENANT
if not re.fullmatch(r"[a-z0-9]{1,40}", WS) or (a.stamp and not re.fullmatch(r"[a-z0-9]{1,20}", a.stamp)):
    sys.exit("argument refusé")

etat_path = a.reprendre or os.path.join(HERE, f"parcours_{int(time.time())}.json")
E = json.load(open(etat_path)) if a.reprendre else {"stamp": a.stamp or str(int(time.time()))[-6:], "preuves": []}
S = E["stamp"]
# Envoi par le relais AGENCE (le relais nord se croit destinataire de messagerie-nord-776.fr et livre en local, mesuré le 26/11) :
# l'expéditeur est sur agence-veridian.fr (DKIM aligné), la boîte et l'adresse de retour sur le domaine servi par le MX Notifuse.
ENVOI, BOITE, RETOUR = f"parcours-envoi-{S}@agence-veridian.fr", f"parcours-boite-{S}@{DOM}", f"parcours-retour-{S}@{DOM}"
AUTRE = f"parcours-autre-{S}@{DOM}"  # --autre-nom : une personne absente du CRM, que la réponse crée
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


def nettoyer():
    """Retire tout ce qui peut envoyer : séquence en pause puis supprimée, profils supprimés. Les contacts d'épreuve restent
    (désinscrits), le journal des messages aussi : c'est la preuve."""
    for aid in [E.get("automation")]:
        if aid:
            print("pause séquence :", api("POST", "/api/automations.pause", {"workspace_id": WS, "automation_id": aid}, check=False).get("_rc", "ok"))
            print("suppression séquence :", api("POST", "/api/automations.delete", {"workspace_id": WS, "automation_id": aid}, check=False).get("_rc", "ok"))
    w = api("GET", "/api/workspaces.get", id=WS)["workspace"]
    s = w["settings"]
    for k in ("marketing_email_provider_id", "transactional_email_provider_id"):
        if s.get(k) in (E.get("integ_envoi"), E.get("integ_boite"), E.get("integ_autre")):
            s[k] = ""
    api("POST", "/api/workspaces.update", {"id": WS, "name": w["name"], "settings": s}, check=False)
    for k in ("integ_envoi", "integ_boite", "integ_autre"):
        if E.get(k):
            print("suppression profil", k, ":", api("POST", "/api/workspaces.deleteIntegration", {"workspace_id": WS, "integration_id": E[k]}, check=False).get("_rc", "ok"))
    left = [i for i in api("GET", "/api/workspaces.get", id=WS)["workspace"].get("integrations") or [] if i["id"] in (E.get("integ_envoi"), E.get("integ_boite"), E.get("integ_autre"))]
    preuve("nettoyage : plus aucun profil d'épreuve", not left, f"{len(left)} restant(s)")


if a.nettoyer_seulement:
    nettoyer(); sys.exit(1 if ecarts else 0)
if not a.real_send:
    print("NON ABOUTI : l'envoi réel exige --real-send"); sys.exit(2)
print("état :", etat_path, "| expéditeur", ENVOI, "| boîte d'épreuve", BOITE)
st = cli("version")
print("prod :", st.get("git_sha") or st)

try:
    # 1. Profils : l'expéditeur de la séquence et la boîte d'épreuve, par le relais nord (DKIM d=messagerie-nord-776.fr).
    if not E.get("integ_envoi"):
        c = creds()
        if not c.get("SMTP_AGENCE_USER3") or not c.get("SMTP_AGENCE_PW3"):
            print("NON ABOUTI : accès SASL du relais agence introuvables"); sys.exit(2)
        profils = [("integ_envoi", [("s1", ENVOI, True), ("s2", RETOUR, False)], f"parcours-envoi-{S}"), ("integ_boite", [("s1", BOITE, True)], f"parcours-boite-{S}")]
        if a.autre_nom:
            profils.append(("integ_autre", [("s1", AUTRE, True)], f"parcours-autre-{S}"))
        for key, senders, nom in profils:
            r = api("POST", "/api/workspaces.createIntegration", {"workspace_id": WS, "name": nom, "type": "email", "provider": {
                "kind": "smtp", "smtp": {"host": c.get("SMTP_AGENCE_HOST", "smtp.agence-veridian.fr"), "port": int(c.get("SMTP_AGENCE_PORT", "587")),
                                         "username": c["SMTP_AGENCE_USER3"], "password": c["SMTP_AGENCE_PW3"], "use_tls": True},
                "rate_limit_per_minute": 10, "veridian_profile_daily_cap": 3,
                "veridian_sending_window": {"start_hour": 0, "end_hour": 24, "timezone": "UTC"},
                "veridian_provider_class_rates": {k: 10 for k in CLASSES}, "veridian_provider_class_daily_cap": {k: 3 for k in CLASSES},
                "senders": [{"id": i, "email": ad, "name": a.autre_nom if key == "integ_autre" else "Veridian Parcours", "is_default": d} for i, ad, d in senders]}})
            E[key] = r["integration_id"]; save()
        w = api("GET", "/api/workspaces.get", id=WS)["workspace"]
        w["settings"]["marketing_email_provider_id"] = E["integ_envoi"]
        w["settings"]["transactional_email_provider_id"] = ""  # la boîte n'est pas le profil réservé : sa réponse part d'elle
        api("POST", "/api/workspaces.update", {"id": WS, "name": w["name"], "settings": w["settings"]})
        hosts = {i["id"]: i["email_provider"]["smtp"].get("host") for i in api("GET", "/api/workspaces.get", id=WS)["workspace"]["integrations"] if i["id"] in (E["integ_envoi"], E["integ_boite"])}
        preuve("profils créés (relais agence, plafond 3/jour)", len(hosts) == 2 and all("agence-veridian" in (h or "") for h in hosts.values()), str(hosts))

    # 1 bis. Abonnement aux réponses : posé par le HUB lui-même (il garde le secret) ; ici, on relit seulement qu'il existe.
    # ancien commentaire : (message.received vers le Hub, qui crée fiche et tâche au CRM : VER-247).
    if not E.get("abonnement"):
        sys.path.insert(0, os.path.expanduser('~/.claude/skills/notifuse-cli/bin'))
        import notifuse_common as nc
        subs = api("GET", "/api/webhookSubscriptions.list", check=False, workspace_id=WS)
        L = subs.get("subscriptions") or subs.get("webhook_subscriptions") or []
        E["abonnement"] = [(x.get("id"), x.get("url"), x.get("event_types")) for x in L]
        preuve("abonnement message.received du Hub présent", any("message.received" in json.dumps(x) for x in L), str(E["abonnement"]))

    # 2. Listes, import groupé, segment.
    if not E.get("import"):
        for lid, nom in ((f"preserve{S}", "Parcours réserve"), (f"psequence{S}", "Parcours séquence")):
            api("POST", "/api/lists.create", {"workspace_id": WS, "id": lid, "name": nom, "is_double_optin": False, "is_public": False})
        contacts = [{"email": BOITE, "first_name": "Boîte", "last_name": "Épreuve", "custom_string_5": "corporate_selfhost"},
                    {"email": f"parcours-temoin1-{S}@example.test", "first_name": "Témoin", "custom_string_5": "corporate"},
                    {"email": f"parcours-temoin2-{S}@example.test", "first_name": "Témoin", "custom_string_5": "corporate"}]
        r = api("POST", "/api/veridian/contacts.intake", {"workspace_id": WS, "contacts": contacts, "list_ids": [f"preserve{S}"],
                                                          "dedupe": ["email"], "existing": "attach", "allow_triggers": False})
        E["import"] = {k: r.get(k) for k in ("created", "existing", "attached", "rejected")}
        preuve("import groupé de 3 contacts dans la réserve", (r.get("created") or 0) + (r.get("existing") or 0) == 3, str(E["import"]))
        seg = f"parcours{S}"
        api("POST", "/api/segments.create", {"workspace_id": WS, "id": seg, "name": f"Parcours {S} : boîte d'épreuve", "color": "#2F855A", "timezone": "UTC",
                                             "tree": {"kind": "leaf", "leaf": {"source": "contacts", "contact": {"filters": [
                                                 {"field_name": "email", "field_type": "string", "operator": "contains", "string_values": [f"parcours-boite-{S}"]}]}}}})
        E["segment"] = seg; save()
        n = None
        for _ in range(12):
            time.sleep(5)
            sg = api("GET", "/api/segments.get", check=False, id=seg)
            sg = sg.get("segment") or sg
            n = sg.get("users_count")
            if sg.get("status") == "active":
                break
        preuve("segment : la seule boîte d'épreuve", n == 1, f"statut {sg.get('status')}, {n} contact(s)")

    # 3. Modèle (lien de désinscription) et séquence d'un mail.
    if not E.get("automation"):
        tpl, seq, liste = f"ptpl{S}", f"parcours{S}", f"psequence{S}"
        tree = {"id": "root", "type": "mjml", "attributes": {"version": "4.0.0"}, "children": [{"id": "b", "type": "mj-body", "children": [
            {"id": "s", "type": "mj-section", "children": [{"id": "c", "type": "mj-column", "children": [
                {"id": "t", "type": "mj-text", "content": f"Bonjour {{{{ contact.first_name }}}}, ceci est l'épreuve {S} du parcours Mail Veridian (envoi réel vers notre boîte d'épreuve). <a href=\"{{{{ unsubscribe_url }}}}\">Se désinscrire</a>"}]}]}]}]}
        api("POST", "/api/templates.create", {"workspace_id": WS, "id": tpl, "name": f"Parcours {S}", "channel": "email", "category": "marketing",
                                              "email": {"sender_id": "s1", "subject": f"Épreuve parcours Mail {S}", "visual_editor_tree": tree}})
        api("POST", "/api/automations.create", {"workspace_id": WS, "automation": {
            "id": seq, "workspace_id": WS, "name": f"Parcours {S}", "status": "draft", "list_id": liste, "root_node_id": "t",
            "trigger": {"event_kind": "list.subscribed", "list_id": liste, "frequency": "once"},
            "nodes": [{"id": "t", "automation_id": seq, "type": "trigger", "config": {}, "next_node_id": "e"},
                      {"id": "e", "automation_id": seq, "type": "email", "config": {"template_id": tpl}}]}})
        api("POST", "/api/automations.activate", {"workspace_id": WS, "automation_id": seq})
        E["automation"], E["template"] = seq, tpl; save()
        st = [x.get("status") for x in api("GET", "/api/automations.list", limit=100).get("automations") or [] if x["id"] == seq]
        preuve("séquence active", st == ["live"], str(st))

    # 4. Inscription de la seule boîte d'épreuve, puis ENVOI RÉEL.
    if not E.get("envoye"):
        r = api("POST", "/api/veridian/reservoir.activate", {"workspace_id": WS, "automation_id": E["automation"], "emails": [BOITE], "limit": 1})
        preuve("inscription par le réservoir", r.get("enrolled") == 1, str({k: r.get(k) for k in ("enrolled", "states")}))
        t0, vu = time.time(), None
        while time.time() - t0 < 1200 and not vu:
            time.sleep(20)
            msgs = api("GET", "/api/messages.list", limit=20).get("messages") or []
            vu = next((m for m in msgs if m.get("contact_email") == BOITE and m.get("template_id") == E["template"]), None)
            if not vu:
                q = api("GET", "/api/veridian/queue.explain", group_by="reason", check=False)
                print(f"  +{int(time.time() - t0)} s : pas encore parti ; file : {[(g.get('reason'), g.get('count')) for g in q.get('groups') or []]}")
        if not vu:
            preuve("envoi réel", False, "rien parti en 20 min"); raise SystemExit
        E["envoye"] = {k: vu.get(k) for k in ("id", "sent_at", "status", "message_id")}
        preuve("envoi réel parti", True, f"{int(time.time() - t0)} s, {E['envoye']}")

    # 5. Réception dans la boîte d'épreuve (MX Notifuse, port 25 public), en-têtes lus.
    if not E.get("recu"):
        boxes = api("GET", "/api/veridian/mail.mailboxes.list", check=False, workspace_id=WS)
        box = next((b for b in (boxes.get("mailboxes") or boxes if isinstance(boxes, list) else boxes.get("mailboxes") or []) if (b.get("address") or "").lower() == BOITE), None)
        E["box_boite"] = box and box.get("id")
        t0, recu = time.time(), None
        while time.time() - t0 < 900 and not recu:
            time.sleep(20)
            l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", limit=20)
            items = l.get("messages") if isinstance(l, dict) else l
            recu = next((m for m in items or [] if BOITE in json.dumps(m).lower()), None)
            if not recu:
                print(f"  +{int(time.time() - t0)} s : pas encore reçu")
        if not recu:
            preuve("réception", False, "rien reçu en 15 min"); raise SystemExit
        E["recu"] = recu.get("id"); save()
        raw = subprocess.run(["notifuse-admin", "--env", "prod", "mail:raw", WS, "--id", str(E["recu"])], capture_output=True, text=True, timeout=120).stdout
        open(os.path.join(HERE, f"parcours_{S}_recu.eml"), "w").write(raw)
        head = raw.split("\n\n", 1)[0] if "\n\n" in raw else raw.split("\r\n\r\n", 1)[0]
        dkim = re.search(r"(?im)^DKIM-Signature:.*?\bd=([^;\s]+)", head)
        ar = re.findall(r"(?im)^Authentication-Results:.*(?:\n[ \t].*)*", head)
        lu = re.search(r"(?im)^List-Unsubscribe:\s*(.*(?:\n[ \t].*)*)", head)
        lup = re.search(r"(?im)^List-Unsubscribe-Post:\s*(.*)", head)
        rcv = re.findall(r"(?im)^Received:.*(?:\n[ \t].*)*", head)
        E["entetes"] = {"dkim_d": dkim and dkim.group(1), "auth_results": ar, "list_unsubscribe": lu and " ".join(lu.group(1).split()),
                        "list_unsubscribe_post": lup and lup.group(1).strip(), "received": [" ".join(x.split())[:220] for x in rcv[:4]]}
        save()
        preuve("reçu dans la boîte d'épreuve par le MX Notifuse", True, f"message {E['recu']}")
        preuve("signature DKIM alignée sur l'expéditeur", bool(dkim) and dkim.group(1).lower() == ENVOI.split("@")[1], f"d={dkim and dkim.group(1)}")
        preuve("List-Unsubscribe en un clic (RFC 8058)", bool(lu) and bool(lup), f"{E['entetes']['list_unsubscribe']} | {E['entetes']['list_unsubscribe_post']}")
        print("Authentication-Results :", ar or "absent (le récepteur ne l'écrit pas encore : lot D1)")

    # 5 bis. --ouvrir : le lecteur ouvre le mail (pixel) et clique son lien suivi (activité Mail vers le Hub, fiche 98).
    if a.ouvrir and not E.get("ouvert"):
        raw = subprocess.run(["notifuse-admin", "--env", "prod", "mail:raw", WS, "--id", str(E["recu"])], capture_output=True, timeout=120).stdout
        import email as _email
        from email import policy as _policy
        html = next((p.get_content() for p in _email.message_from_bytes(raw, policy=_policy.default).walk() if p.get_content_type() == "text/html"), "")
        pix = [u for u in re.findall(r'<img[^>]+src="([^"]+)"', html) if "/t/" in u or "/opens" in u]
        lien = [u for u in re.findall(r'href="([^"]+)"', html) if "/r/" in u or "/visit" in u]
        ua = {"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"}
        res = {}
        for nom, urls in (("ouverture", pix), ("clic", lien)):
            if not urls:
                res[nom] = "absent"
                continue
            try:
                with urllib.request.urlopen(urllib.request.Request(urls[0], headers=ua), timeout=20) as r:
                    res[nom] = r.status
            except urllib.error.HTTPError as e:
                res[nom] = e.code
            time.sleep(2)
        E["ouvert"] = {"a": time.strftime("%H:%M:%SZ", time.gmtime()), **res}; save()
        preuve("ouverture et clic du lecteur", res.get("ouverture") == 200 and res.get("clic") not in (None, "absent"), str(E["ouvert"]))

    # 6. Réponse humaine depuis la boîte d'épreuve, retour au MX, arrêt de la séquence.
    if not E.get("reponse"):
        r = cli("mail:reply", WS, "--id", str(E["recu"]), "--profile", E["integ_boite"], "--to", RETOUR,
                "--text", f"Bonjour, réponse humaine de l'épreuve {S}. Merci de me rappeler.", "--yes", "--json", check=False)
        preuve("réponse envoyée depuis la boîte d'épreuve", r.get("_rc") in (None, 0) and "error" not in json.dumps(r).lower(), json.dumps(r, ensure_ascii=False)[:250])
        E["reponse"] = r; save()
    if not E.get("reponse_recue"):
        t0, rr = time.time(), None
        while time.time() - t0 < 900 and not rr:
            time.sleep(20)
            l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", kind="reply_human", limit=20)
            items = l.get("messages") if isinstance(l, dict) else l
            rr = next((m for m in items or [] if BOITE in json.dumps(m).lower()), None)
            if not rr:
                print(f"  +{int(time.time() - t0)} s : réponse pas encore reçue")
        preuve("réponse humaine reçue et classée reply_human", bool(rr), json.dumps({k: (rr or {}).get(k) for k in ("id", "kind", "from", "subject")}, ensure_ascii=False))
        E["reponse_recue"] = rr and rr.get("id"); save()
        en = api("GET", "/api/automations.enrollments", automation_id=E["automation"], check=False)
        rows = en.get("enrollments") or en.get("items") or []
        moi = [x for x in rows if (x.get("contact_email") or x.get("email")) == BOITE]
        preuve("séquence arrêtée sur réponse", bool(moi) and all(x.get("status") in ("exited", "completed", "stopped") for x in moi),
               str([(x.get("status"), x.get("exit_reason") or x.get("reason")) for x in moi]))

    # 6 bis. --autre-nom : réponse humaine d'une adresse absente du CRM ; le Hub crée la personne sous ce nom (from_name, VER-247).
    if a.autre_nom and E.get("integ_autre") and not E.get("autre_recue"):
        if not E.get("autre_reponse"):
            r = cli("mail:reply", WS, "--id", str(E["recu"]), "--profile", E["integ_autre"], "--to", RETOUR,
                    "--text", f"Bonjour, je suis une autre personne ({S}). Rappelez-moi.", "--yes", "--json", check=False)
            E["autre_reponse"] = r; save()
            preuve("réponse depuis l'adresse absente du CRM envoyée", r.get("_rc") in (None, 0) and "error" not in json.dumps(r).lower(), json.dumps(r, ensure_ascii=False)[:200])
        t0, rr = time.time(), None
        while time.time() - t0 < 900 and not rr:
            time.sleep(20)
            l = api("GET", "/api/veridian/mail.messages.list", check=False, workspace_id=WS, direction="in", kind="reply_human", limit=20)
            rr = next((m for m in (l.get("messages") if isinstance(l, dict) else l) or [] if AUTRE in json.dumps(m).lower()), None)
        E["autre_recue"] = rr and {k: rr.get(k) for k in ("id", "from", "from_name", "received_at")}; save()
        preuve("réponse de l'adresse absente du CRM reçue, nom affiché", bool(rr) and (rr or {}).get("from_name") == a.autre_nom, str(E["autre_recue"]))

    # 7. Désinscription en un clic depuis l'en-tête du mail reçu.
    if not E.get("desinscrit"):
        url = next((u for u in re.findall(r"<(https?://[^>]+)>", E["entetes"].get("list_unsubscribe") or "")), None)
        if not url:
            preuve("désinscription", False, "aucune URL https dans List-Unsubscribe")
        else:
            req = urllib.request.Request(url, data=b"List-Unsubscribe=One-Click", method="POST", headers={"Content-Type": "application/x-www-form-urlencoded"})
            try:
                code = urllib.request.urlopen(req, timeout=30).status
            except urllib.error.HTTPError as e:
                code = e.code
            time.sleep(5)
            cl = api("GET", "/api/contacts.getByEmail", check=False, workspace_id=WS, email=BOITE)
            rows = (cl.get("contact") or cl).get("contact_lists") or []
            st = {x.get("list_id"): x.get("status") for x in rows}
            E["desinscrit"] = {"http": code, "listes": st}; save()
            preuve("désinscription en un clic", code in (200, 202, 204) and st.get(f"psequence{S}") == "unsubscribed", str(E["desinscrit"]))
finally:
    if E.get("integ_envoi") and E.get("desinscrit"):
        nettoyer()  # seulement parcours fini : un écart en route garde tout pour la reprise
    save()
print("\nRÉSULTAT :", "VERT" if not ecarts else "ROUGE " + ", ".join(ecarts), "| état", etat_path)
sys.exit(1 if ecarts else 0)
