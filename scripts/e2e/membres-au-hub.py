#!/usr/bin/env python3
"""Un membre ne s'ajoute qu'au Hub (fiche 86) : parcours réel sur STAGING. Sort 0 (prouvé), 1 (défaut), 2 (non abouti).

Routes natives refusées (403 members_managed_by_hub), invitation par Notifuse -> Hub, acceptation au Hub, membre rattaché et relié.
Prépare l'espace mgatea et son propriétaire relié, retire l'espace à la fin (--keep pour le garder). Dépend de notifuse-admin et hub."""
import http.cookiejar, json, os, re, subprocess, sys, time, urllib.error, urllib.request, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

HUB, ENV, WS, OWNER = "https://hub.staging.veridian.site", "staging", "mgatea", "mgate-a@exemple.test"
GUEST = f"mgate-{int(time.time())}@exemple.test"
bad = []


def hub(*args):
    p = subprocess.run(["hub", "--env", ENV, *args], capture_output=True, text=True, timeout=120)
    try:
        return json.loads(p.stdout)
    except ValueError:
        print("NON ABOUTI : hub", args[0], p.stderr[-200:]); sys.exit(2)


def check(name, ok, seen=""):
    print("ok " if ok else "KO ", name, seen)
    if not ok:
        bad.append(name)


subprocess.run(["notifuse-admin", "--env", ENV, "provision", WS, "--email", OWNER], capture_output=True, timeout=120)
owner = hub("users:create", OWNER, "--uuid", str(uuid.uuid4()))
hub("users:create", GUEST, "--uuid", str(uuid.uuid4()))
nc.call_hmac(ENV, "POST", "/api/sso/issue-magic-link",
             {"hub_user_id": owner["supabase_user_id"], "email": OWNER, "email_proven": True})
jwt = nc._owner_jwt_impl(ENV, WS, email=OWNER)
for route, call in (("inviteMember", lambda: nc.call_jwt(ENV, "POST", "/api/workspaces.inviteMember", jwt, body={"workspace_id": WS, "email": GUEST})),
                    ("acceptInvitation", lambda: nc.call_anon(ENV, "POST", "/api/workspaces.acceptInvitation", body={"token": "faux"}))):
    st, p = call()
    check(f"natif {route} refusé", st == 403 and p.get("code") == "members_managed_by_hub", st)
st, p = nc.call_jwt(ENV, "GET", "/api/workspaces.listAPIKeys", jwt, params={"workspace_id": WS})
check("listAPIKeys reste servie", st == 200, st)
st, inv = nc.call_jwt(ENV, "POST", "/api/veridian/workspaces.inviteMember", jwt, body={"workspace_id": WS, "email": GUEST, "role": "member"})
check("invitation par le Hub", st == 200 and inv.get("hub_invitation_id"), f"{st} {inv.get('error', '')}")
if st == 200:
    token = re.search(r"/invite/([0-9a-f]+)", inv["magic_link_url"]).group(1)
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    op.open(hub("impersonate", GUEST)["links"]["hub"], timeout=60)
    try:
        r = op.open(urllib.request.Request(f"{HUB}/api/invitations/{token}/accept", data=b"{}", method="POST",
                                           headers={"content-type": "application/json", "origin": HUB}), timeout=60)
        st, body = r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        st, body = e.code, json.loads(e.read() or b"{}")
    check("acceptation au Hub", st == 200 and body.get("downstream_call") == "completed", f"{st} {body.get('downstream_call')}")
    st, p = nc.call_hmac(ENV, "GET", f"/api/veridian/membres?workspace_id={WS}")
    m = [x for x in p.get("members", []) if x["email"] == GUEST]
    check("membre rattaché et relié", len(m) == 1 and m[0]["role"] == "member" and bool(m[0]["hub_user_id"]), st)
if "--keep" not in sys.argv:
    subprocess.run(["notifuse-admin", "--env", ENV, "wipe", "--ids", WS], capture_output=True, timeout=120)
sys.exit(1 if bad else 0)
