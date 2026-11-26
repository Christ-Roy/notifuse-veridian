#!/usr/bin/env python3
"""Administrateur de plateforme sur un espace jetable de STAGING (fiche 95) : ajout, rejeu, protection du client, retrait, retour refusé. Sortie 0/1/2."""
import os, sys, time, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

E, N = "staging", str(int(time.time()))[-7:]
W, W2 = f"padm{N}", f"padz{N}"
HUB = lambda m: str(uuid.uuid5(uuid.NAMESPACE_URL, m))  # noqa: E731
CLIENT, ADMIN, MEMBRE = (f"{x}{N}@e2e.veridian.site" for x in ("client", "admin", "membre"))
bad = []


def dit(nom, vrai, detail=""):
    print(("ok   " if vrai else "FAUX ") + nom + (f" : {detail}" if detail or not vrai else ""))
    if not vrai:
        bad.append(nom)


def signe(m, r, b=None):
    return nc.call_hmac(E, m, r, b)


def roles(w=W):
    st, d = signe("GET", f"/api/veridian/admin/platform-admin?workspace_id={w}")
    if st != 200:
        raise SystemExit(2)
    return {m["hub_user_id"]: m["role"] for m in d["members"] if not m["machine"]}


def jeton(mail, w):
    import re, urllib.request
    st, d = signe("POST", "/api/sso/issue-magic-link", {"hub_user_id": HUB(mail), "email": mail, "next": f"/console/workspace/{w}"})
    html = urllib.request.urlopen(d["magic_link_url"], timeout=20).read().decode()
    return re.search(r"setItem\('auth_token',\s*\"([^\"]+)\"", html).group(1)


def geste(w=W, **k):
    return signe("POST", "/api/veridian/admin/platform-admin", {"workspace_id": w, "hub_user_id": HUB(ADMIN), "granted_by": "épreuve", "reason": "espace jetable", **k})


try:
    for w, o in ((W, CLIENT), (W2, ADMIN)):
        st, d = signe("POST", "/api/tenants/provision", {"tenant_id": w, "owner_email": o, "plan": "free", "owner_hub_user_id": HUB(o)})
        if st != 200:
            print(f"NON ABOUTI : provision HTTP {st}")
            sys.exit(2)
    for m in (ADMIN, MEMBRE):
        signe("POST", f"/api/tenants/{W}/sync-member", {"email": m, "hub_user_id": HUB(m), "email_proven": True, "role": "member"})
    avant = roles()
    dit("départ : un client propriétaire, deux membres", sorted(avant.values()) == ["member", "member", "owner"], avant and str(sorted(avant.values())))
    st, d = signe("POST", "/api/veridian/admin/attach-owner", {"tenant_id": W, "owner_email": ADMIN, "hub_user_id": HUB(ADMIN)})
    dit("attach-owner sans le dire : refusé", st == 409 and d.get("code") == "owner_exists" and roles() == avant, f"{st} {d.get('code')}")
    st, d = geste(dry_run=True)
    dit("à blanc : rien ne bouge", st == 200 and d["state"] == "promoted" and roles() == avant, f"{st} {d.get('state')}")
    st, d = geste()
    apres = roles()
    dit("ajout : deux propriétaires, le client et l'administrateur", st == 200 and apres == {**avant, HUB(ADMIN): "owner"}, f"{st} {d.get('state')}")
    st, d = geste()
    dit("rejeu sans effet", st == 200 and d["state"] == "already_admin" and roles() == apres, f"{st} {d.get('state')}")
    st, d = geste(hub_user_id=HUB("inconnu"))
    dit("personne inconnue : refusée", st == 404 and d.get("code") == "person_unknown" and roles() == apres, f"{st} {d.get('code')}")
    st, d = signe("POST", "/api/veridian/admin/platform-admin", {"workspace_id": W, "email": ADMIN, "granted_by": "épreuve", "reason": "par adresse"})
    dit("par adresse : refusé", st == 400 and roles() == apres, st)
    st, d = geste(w=W2)
    dit("propriétaire en propre : reconnu, pas marqué", st == 200 and d["state"] == "already_owner", f"{st} {d.get('state')}")
    st, d = signe("GET", f"/api/tenants/{W}/health")
    dit("l'état de santé annonce le client", st == 200 and d.get("owner_email") == CLIENT, st)

    a, c = jeton(ADMIN, W), jeton(CLIENT, W)
    ids = {m["email"]: m["user_id"] for m in nc.call_jwt(E, "GET", "/api/workspaces.members", c, params={"id": W})[1]["members"]}
    st, d = nc.call_jwt(E, "POST", "/api/workspaces.removeMember", a, body={"workspace_id": W, "user_id": ids[CLIENT]})
    dit("l'administrateur ne retire pas le client", st == 403 and d.get("code") == "client_owner_protected" and roles() == apres, f"{st} {d.get('code')}")
    st, d = nc.call_jwt(E, "POST", "/api/workspaces.removeMember", a, body={"workspace_id": W, "user_id": ids[MEMBRE]})
    dit("il gère les membres comme un propriétaire", st == 200 and HUB(MEMBRE) not in roles(), st)
    st, d = nc.call_jwt(E, "POST", "/api/lists.create", a, body={"workspace_id": W, "id": "essai", "name": "Essai", "is_double_optin": False, "is_public": False})
    dit("il travaille dans l'espace", st in (200, 201), st)
    st, d = nc.call_jwt(E, "POST", "/api/workspaces.removeMember", c, body={"workspace_id": W, "user_id": ids[ADMIN]})
    dit("le client retire l'administrateur", st == 200 and roles() == {HUB(CLIENT): "owner"}, st)
    st, d = geste()
    dit("jamais remis par la route", st == 409 and d.get("code") == "removed_by_owner", f"{st} {d.get('code')}")
    st, d = signe("POST", f"/api/tenants/{W}/sync-member", {"email": ADMIN, "hub_user_id": HUB(ADMIN), "email_proven": True, "role": "member"})
    dit("jamais remis par le Hub", st == 409 and d.get("code") == "removed_by_owner" and roles() == {HUB(CLIENT): "owner"}, f"{st} {d.get('code')}")
finally:
    signe("POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [W, W2], "safety_client_prefixes": ["canary"]})
print("ROUGE : " + ", ".join(bad) if bad else "VERT")
sys.exit(1 if bad else 0)
