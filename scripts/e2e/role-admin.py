#!/usr/bin/env python3
"""Le rôle « admin » d'un espace (fiche 95), parcours réel. Propriétaire, admin et membre (à tous droits) frappent chaque route réservée d'un espace
jetable ; l'admin est désigné par la route signée du Hub, retiré par le propriétaire, et rien d'automatique ne le ramène.
Sort 0 (prouvé), 1 (défaut), 2 (non abouti). Aucun mail ne part (profils jamais essayés, adresses example.com, mot de passe d'application factice).

Défaut : staging. --prod : mêmes gestes sur un espace jetable de prod, retiré à la fin. --liens <fichier> garde l'espace et y écrit (0600) deux liens
de connexion pour les captures ; relancer ensuite avec --nettoyer <espace>."""
import argparse, json, os, re, sys, time, urllib.error, urllib.parse, urllib.request, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

p = argparse.ArgumentParser()
p.add_argument("--prod", action="store_true"); p.add_argument("--liens"); p.add_argument("--nettoyer")
a = p.parse_args()
ENV = "prod" if a.prod else "staging"
T = f"{int(time.time()) % 10**8}"
WS = a.nettoyer or f"wsadm{T}"
P = {k: str(uuid.uuid4()) for k in ("proprio", "admin", "membre", "second", "operateur")}
MAIL = {k: f"e2e-wsadm-{T}-{k}@example.com" for k in P}
bad = []


def non_abouti(*x):
    print("NON ABOUTI :", *x); nettoyer(); sys.exit(2)


def check(nom, ok, vu=""):
    print("ok " if ok else "KO ", nom, "" if ok else str(vu)[:300], flush=True)
    if not ok:
        bad.append(nom)


def hub(m, chemin, corps=None):
    st, r = nc.call_hmac(ENV, m, chemin, corps)
    return st, (r if isinstance(r, dict) else {"brut": r})


def geste(nom, corps):
    return hub("POST", f"/api/tenants/{WS}/{nom}", corps)


def roles():
    st, r = hub("GET", f"/api/hub/workspace-admins?workspace_id={WS}")
    if st != 200:
        non_abouti("rôles illisibles", st, r)
    return {m["hub_user_id"]: m for m in r["members"] if m.get("hub_user_id")}


def decision(qui, action, **plus):
    return hub("POST", "/api/hub/workspace-admins", {"workspace_id": WS, "hub_user_id": P[qui], "action": action,
               "actor_hub_user_id": P["operateur"], "reason": "e2e role-admin", **plus})


def lien(qui):
    st, r = hub("POST", "/api/sso/issue-magic-link", {"hub_user_id": P[qui], "email": MAIL[qui], "email_proven": True,
                "next": f"/console/workspace/{WS}"})
    return r.get("magic_link_url") if st == 200 else None


def jeton(qui):
    """Jeton de session, jamais à l'écran. None si la connexion est refusée."""
    u = lien(qui)
    if not u:
        return None
    try:
        html = urllib.request.urlopen(u, timeout=30).read().decode()
    except urllib.error.HTTPError:
        return None
    m = re.search(r"setItem\('auth_token',\s*\"([^\"]+)\"", html)
    return json.loads(f'"{m.group(1)}"') if m else None


def nettoyer():
    for q in ("admin", "membre", "second"):
        if not a.nettoyer:
            geste("remove-member", {"hub_user_id": P[q], "reason": "e2e"})
    st, r = hub("POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [WS]})
    if not a.nettoyer:
        for q in P:
            hub("POST", "/api/hub/users/sync", {"hub_user_id": P[q], "deleted": True})
    return st, r


if a.nettoyer:
    st, r = nettoyer()
    print(st, json.dumps(r)[:300]); sys.exit(0 if st == 200 else 1)
for _ in range(40):  # le staging s'endort : on l'attend, 2 min au plus
    st, _r = nc.call_anon(ENV, "GET", "/api/veridian/mode")
    if st == 200:
        break
    time.sleep(3)
else:
    print("NON ABOUTI : instance endormie ou injoignable", st); sys.exit(2)
st, r = hub("POST", "/api/tenants/provision", {"tenant_id": WS, "owner_email": MAIL["proprio"], "owner_hub_user_id": P["proprio"]})
if st != 200:
    print("NON ABOUTI : espace", st, r); sys.exit(2)

try:
    for q in ("admin", "membre"):
        st, r = geste("sync-member", {"hub_user_id": P[q], "email": MAIL[q], "email_proven": True, "role": "admin"})
        check(f"préparation {q} : une synchronisation, même « admin », ne donne que membre", st == 200 and r.get("app_role") == "member", f"{st} {r}")

    # --- la route signée
    st, r = nc.call_anon(ENV, "POST", "/api/hub/workspace-admins", {"workspace_id": WS, "hub_user_id": P["admin"], "action": "grant"})
    check("sans signature : 401", st == 401, st)
    st, r = decision("second", "grant")
    check("inconnu : 404 user_not_in_app", st == 404 and r.get("code") == "user_not_in_app", f"{st} {r}")
    st, r = decision("admin", "grant", email=MAIL["admin"])
    check("une adresse dans le corps : 400", st == 400, f"{st} {r}")
    st, r = decision("admin", "grant")
    check("désigné : membre devient admin", st == 200 and r.get("role_before") == "member" and r.get("role_after") == "admin" and r.get("changed") is True, f"{st} {r}")
    st, r = decision("admin", "grant")
    check("rejoué : rien ne change", st == 200 and r.get("changed") is False, f"{st} {r}")
    J = {q: jeton(q) for q in ("proprio", "admin", "membre")}
    if not all(J.values()):
        non_abouti("connexion impossible", [q for q in J if not J[q]])

    def api(qui, m, route, corps=None, params=None):
        st, r = nc.call_jwt(ENV, m, route, J[qui], body=corps, params=params)
        return st, (r if isinstance(r, dict) else {"brut": r})

    st, r = api("proprio", "GET", "/api/workspaces.members", params={"id": WS})
    eq = {m.get("email"): m for m in r.get("members", [])}
    check("Équipe : l'admin se voit admin", st == 200 and eq.get(MAIL["admin"], {}).get("role") == "admin", f"{st} {list(eq)}")
    uid = {q: eq.get(MAIL[q], {}).get("user_id") for q in ("proprio", "admin", "membre")}
    st, r = api("proprio", "GET", "/api/workspaces.get", params={"id": WS})
    ws = r.get("workspace", r)
    reglages = ws.get("settings", {})
    nom = ws.get("name")
    smtp = lambda q: {"workspace_id": WS, "type": "smtp_imap", "name": f"e2e {q}", "sender_email": f"{q}@example.com", "sender_name": "e2e",
                      "smtp": {"host": "smtp.example.com", "port": 587, "use_tls": True, "username": "x", "password": "factice"}}
    gmail = lambda q: {"workspace_id": WS, "type": "gmail_app_password", "name": f"e2e gmail {q}", "sender_email": f"e2e.{q}.{T}@gmail.com",
                       "sender_name": "e2e", "app_password": "abcdabcdabcdabcd", "gmail_account_type": "personal"}
    llm = lambda q: {"workspace_id": WS, "name": f"e2e llm {q}", "type": "llm",
                     "llm_provider": {"kind": "anthropic", "anthropic": {"api_key": "factice", "model": "claude-sonnet-5-5"}}}
    tout = {"workspace": {"read": True, "write": True}}

    # --- la matrice : qui PASSE le contrôle de rôle. Passer = pas un refus d'autorisation (401/403) ; le message d'erreur métier compte comme passé.
    REFUS = ("not an owner", "only the workspace owner", "only workspace owner", "only owner", "Forbidden", "forbidden")

    def passe(st, r):
        return st not in (401, 403) and not any(m in json.dumps(r) for m in REFUS)

    MATRICE = [  # nom, admin attendu, membre attendu, appel
        ("réglages de l'espace", True, False, lambda q: api(q, "POST", "/api/workspaces.update", {"id": WS, "name": nom, "settings": reglages})),
        ("profil SMTP", True, True, lambda q: api(q, "POST", "/api/veridian/emailProfiles.create", smtp(q))),
        ("profil Gmail, mot de passe d'application factice", True, True, lambda q: api(q, "POST", "/api/veridian/emailProfiles.create", gmail(q))),
        ("intégration IA", True, False, lambda q: api(q, "POST", "/api/workspaces.createIntegration", llm(q))),
        ("clé API", True, False, lambda q: api(q, "POST", "/api/workspaces.createAPIKey", {"workspace_id": WS, "email_prefix": f"e2e{T}{q}"})),
        ("jeton d'installation d'agent", True, False, lambda q: api(q, "POST", "/api/workspaces.createAgentInstallToken", {"workspace_id": WS})),
        ("droits d'un membre", True, False, lambda q: api(q, "POST", "/api/workspaces.setUserPermissions", {"workspace_id": WS, "user_id": uid["membre"], "permissions": FULL})),
        ("droits d'un propriétaire", False, False, lambda q: api(q, "POST", "/api/workspaces.setUserPermissions", {"workspace_id": WS, "user_id": uid["proprio"], "permissions": tout})),
        ("retirer un propriétaire", False, False, lambda q: api(q, "POST", "/api/workspaces.removeMember", {"workspace_id": WS, "user_id": uid["proprio"]})),
        ("abonnement webhook", True, False, lambda q: api(q, "POST", "/api/webhookSubscriptions.toggle", {"workspace_id": WS, "id": "inconnu", "enabled": False})),
        ("inviter (le Hub décide ensuite)", True, False, lambda q: api(q, "POST", "/api/veridian/workspaces.inviteMember", {"workspace_id": WS, "email": "pas-une-adresse", "role": "member"})),
        ("supprimer l'espace", False, False, lambda q: api(q, "POST", "/api/workspaces.delete", {"id": WS})),
    ]
    FULL = eq[MAIL["membre"]].get("permissions") or tout
    for nom_r, adm, mem, appel in MATRICE:
        for q, attendu in (("admin", adm), ("membre", mem)):
            st, r = appel(q)
            if st == 429 or st >= 502:
                non_abouti(nom_r, q, st)
            check(f"{nom_r} / {q} : {'passe' if attendu else 'refusé'}", passe(st, r) is attendu, f"{st} {r}")
            if q == "admin" and "profil" in nom_r or nom_r in ("réglages de l'espace", "clé API", "intégration IA", "droits d'un membre"):
                if attendu:
                    check(f"{nom_r} / {q} : vraiment fait (2xx)", 200 <= st < 300, f"{st} {r}")
        # Le propriétaire passe partout où l'admin passe (les refus métier sur lui-même n'ont pas de sens ici).
        if adm and nom_r not in ("droits d'un membre",):
            st, r = appel("proprio")
            check(f"{nom_r} / propriétaire : passe", passe(st, r), f"{st} {r}")
    J["membre"] = jeton("membre") or J["membre"]  # setUserPermissions coupe ses sessions, voulu
    rl = roles()
    check("propriétaire intact après la matrice", rl.get(P["proprio"], {}).get("role") == "owner", rl)
    st, r = api("admin", "GET", "/api/workspaces.get", params={"id": WS})
    integ = r.get("workspace", r).get("integrations") or []
    check("profil Gmail posé par l'admin, relu", any(i.get("name") == "e2e gmail admin" for i in integ), [i.get("name") for i in integ])
    cible = next((i["id"] for i in integ if i.get("name") == "e2e admin"), None)
    for q, attendu in (("membre", False), ("admin", True)):
        st, r = api(q, "POST", "/api/workspaces.deleteIntegration", {"workspace_id": WS, "integration_id": cible})
        check(f"supprimer une intégration / {q}", passe(st, r) is attendu, f"{st} {r}")

    # --- attach-owner ne retire jamais la propriété sans le dire
    avant = roles()
    st, r = hub("POST", "/api/veridian/admin/attach-owner", {"tenant_id": WS, "owner_email": MAIL["membre"], "hub_user_id": P["membre"]})
    check("attach-owner sans transfert : 409 owner_exists", st == 409 and r.get("code") == "owner_exists", f"{st} {r}")
    check("attach-owner refusé : aucun rôle changé", roles() == avant)
    # Un propriétaire désigné admin qui cède la propriété devient admin, sans passer par membre.
    st, r = decision("proprio", "grant")
    check("désigner un propriétaire : il reste propriétaire", st == 200 and r.get("role_after") == "owner", f"{st} {r}")
    st, r = hub("POST", "/api/veridian/admin/attach-owner", {"tenant_id": WS, "owner_email": MAIL["membre"], "hub_user_id": P["membre"], "transfer": True})
    rl = roles()
    check("transfert demandé : nouveau propriétaire, l'ancien est admin", st == 200 and rl[P["membre"]]["role"] == "owner" and rl[P["proprio"]]["role"] == "admin", f"{st} {rl}")
    st, r = hub("POST", "/api/veridian/admin/attach-owner", {"tenant_id": WS, "owner_email": MAIL["proprio"], "hub_user_id": P["proprio"], "transfer": True})
    rl = roles()
    check("transfert retour depuis un admin", st == 200 and rl[P["proprio"]]["role"] == "owner" and rl[P["membre"]]["role"] == "member", f"{st} {rl}")

    if a.liens:
        with os.fdopen(os.open(a.liens, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
            json.dump({"ws": WS, "admin": lien("admin"), "proprio": lien("proprio"), "personnes": P}, f)
        print("liens écrits ; espace gardé :", WS)
        print("PROUVÉ (hors retrait)" if not bad else f"DÉFAUT : {len(bad)}")
        os._exit(1 if bad else 0)

    # --- le propriétaire retire l'admin, rien ne le ramène
    st, r = api("proprio", "POST", "/api/workspaces.removeMember", {"workspace_id": WS, "user_id": uid["admin"]})
    check("le propriétaire retire l'admin", 200 <= st < 300, f"{st} {r}")
    st, r = api("admin", "GET", "/api/workspaces.get", params={"id": WS})
    check("l'admin retiré n'entre plus", st in (401, 403, 404), st)
    st, r = geste("sync-member", {"hub_user_id": P["admin"], "email": MAIL["admin"], "email_proven": True, "role": "admin"})
    check("synchronisation rejouée : 409 admin_withdrawn", st == 409 and r.get("code") == "admin_withdrawn", f"{st} {r}")
    st, r = geste("restore-member", {"hub_user_id": P["admin"], "email": MAIL["admin"]})
    check("rétablissement du Hub : 409 admin_withdrawn", st == 409 and r.get("code") == "admin_withdrawn", f"{st} {r}")
    st, r = geste("attach-member", {"hub_user_id": P["admin"], "hub_user_email": MAIL["admin"], "email_proven": True, "role": "admin"})
    check("rattachement sans invitation : 409", st == 409 and r.get("code") == "admin_withdrawn", f"{st} {r}")
    st, r = hub("POST", "/api/tenants/provision", {"tenant_id": WS, "owner_email": MAIL["proprio"], "owner_hub_user_id": P["proprio"]})
    rl = roles()
    check("provision rejouée : toujours dehors", rl.get(P["admin"], {}).get("role") == "" and rl[P["admin"]]["admin_withdrawn"] is True, f"{st} {rl.get(P['admin'])}")
    st, r = decision("admin", "grant")
    check("nouvelle désignation : 409 admin_withdrawn", st == 409 and r.get("code") == "admin_withdrawn", f"{st} {r}")
    st, r = geste("attach-member", {"hub_user_id": P["admin"], "hub_user_email": MAIL["admin"], "email_proven": True, "role": "member", "invitation_id": str(uuid.uuid4())})
    check("invitation acceptée : il revient, membre", st in (200, 201) and r.get("role") == "member", f"{st} {r}")
    st, r = decision("admin", "grant")
    check("toujours pas admin sans geste explicite", st == 409, f"{st} {r}")
    st, r = decision("admin", "grant", reinstate=True)
    check("geste explicite : admin de nouveau", st == 200 and r.get("role_after") == "admin", f"{st} {r}")
    st, r = decision("admin", "revoke")
    check("révoqué par le Hub : membre", st == 200 and r.get("role_after") == "member", f"{st} {r}")
    st, r = decision("proprio", "revoke")
    check("révoquer un propriétaire : 409 is_owner", st == 409 and r.get("code") == "is_owner", f"{st} {r}")
finally:
    if not a.liens:
        st, r = nettoyer()
        check("espace jetable retiré", st == 200, f"{st} {r}")
print("PROUVÉ" if not bad else f"DÉFAUT : {len(bad)}")
sys.exit(1 if bad else 0)
