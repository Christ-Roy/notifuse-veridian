#!/usr/bin/env python3
"""Une personne du Hub est son hub_user_id (fiche 89) : son adresse change, un compte NEUF reprend l'ancienne, puis retirer, rétablir, rôle, geler.
Sort 0 (prouvé), 1 (défaut), 2 (non abouti). Aucun mail ne part.

Défaut : staging, espace jetable. --prod --ws <espace de test à nous> : mêmes gestes signés que le Hub sur deux comptes créés ici ; à la fin ils sont
retirés de l'espace, et supprimés au sens du Hub (users/sync deleted). Le nombre de membres est relu avant et après."""
import argparse, os, sys, time, urllib.parse, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

p = argparse.ArgumentParser()
p.add_argument("--prod", action="store_true"); p.add_argument("--ws"); p.add_argument("--keep", action="store_true")
a = p.parse_args()
ENV = "prod" if a.prod else "staging"
if a.prod and not a.ws:
    print("NON ABOUTI : --prod exige --ws"); sys.exit(2)
T = f"{int(time.time()) % 10**8}"
WS = a.ws or f"pers{T}"[:20]
ALICE, BOB, NOBODY = str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
OLD, NEW = f"e2e-pers-{T}-ancienne@example.com", f"e2e-pers-{T}-nouvelle@example.com"
bad = []


def check(name, ok, seen=""):
    print("ok " if ok else "KO ", name, "" if ok else seen)
    if not ok:
        bad.append(name)


def hub(method, path, body=None):
    st, r = nc.call_hmac(ENV, method, path, body)
    return st, (r if isinstance(r, dict) else {"brut": r})


def geste(nom, body):
    return hub("POST", f"/api/tenants/{WS}/{nom}", body)


def membres():
    st, r = hub("GET", f"/api/veridian/membres?workspace_id={urllib.parse.quote(WS)}")
    if st != 200:
        print("NON ABOUTI : membres illisibles", st); sys.exit(2)
    return {m["hub_user_id"]: m for m in r["members"] if m.get("hub_user_id")}, len(r["members"])


for _ in range(40):  # le staging s'endort (307) : on l'attend, 2 min au plus
    st, _r = nc.call_anon(ENV, "GET", "/api/veridian/mode")
    if st == 200:
        break
    time.sleep(3)
else:
    print("NON ABOUTI : instance endormie ou injoignable", st); sys.exit(2)
if not a.prod:
    st, r = hub("POST", "/api/tenants/provision", {"tenant_id": WS, "owner_email": f"e2e-pers-{T}-owner@example.com", "owner_hub_user_id": str(uuid.uuid4())})
    if st != 200:
        print("NON ABOUTI : espace", st, r); sys.exit(2)
_, AVANT = membres()
try:
    st, r = geste("sync-member", {"hub_user_id": ALICE, "email": OLD, "user_email": OLD, "email_proven": True, "role": "member"})
    if st != 200:
        print("NON ABOUTI : préparation", st, r); sys.exit(2)
    alice_local = r.get("app_user_id")
    # Le Hub change l'adresse principale d'Alice, puis Bob, compte neuf, reprend l'ancienne.
    st, r = hub("POST", "/api/hub/users/sync", {"hub_user_id": ALICE, "email": NEW})
    check("adresse d'Alice changée", st == 200 and r.get("updated") is True, f"{st} {r}")
    st, r = hub("POST", "/api/sso/issue-magic-link", {"hub_user_id": NOBODY, "email": OLD})
    check("l'ancienne adresse n'ouvre plus rien", st == 400 and "user_not_in_app" in str(r), f"{st} {r.get('error')}")  # jamais le lien à l'écran
    st, r = geste("sync-member", {"hub_user_id": BOB, "email": OLD, "user_email": OLD, "email_proven": True, "role": "member"})
    check("Bob, compte neuf, prend l'ancienne adresse", st == 200 and r.get("app_user_id") != alice_local, f"{st} {r}")
    m, n = membres()
    check("deux personnes, aucun double", n == AVANT + 2 and m.get(ALICE, {}).get("email") == NEW and m.get(BOB, {}).get("email") == OLD, f"{n} {m}")

    st, r = hub("GET", "/api/users/by-email?" + urllib.parse.urlencode({"email": OLD, "hub_user_id": ALICE}))
    check("by-email : la personne par son identifiant", st == 200 and r.get("user_email") == NEW and r.get("found") is True, f"{st} {r}")
    st, r = hub("GET", "/api/users/by-email?" + urllib.parse.urlencode({"email": OLD, "hub_user_id": NOBODY}))
    check("by-email : identifiant inconnu, adresse ignorée", st == 200 and r.get("found") is False, f"{st} {r}")

    # Chaque geste vise Alice, avec l'ANCIENNE adresse dans le corps (celle de Bob).
    st, r = geste("freeze-member", {"hub_user_id": ALICE, "user_email": OLD})
    check("geler", st == 200 and r.get("hub_user_id") == ALICE and r.get("user_email") == NEW, f"{st} {r}")
    st, r = geste("unfreeze-member", {"hub_user_id": ALICE, "user_email": OLD})
    check("dégeler", st == 200 and r.get("user_email") == NEW, f"{st} {r}")
    st, r = geste("remove-member", {"hub_user_id": ALICE, "user_email": OLD, "reason": "e2e"})
    m, n = membres()
    check("retirer : Alice part, Bob reste", st == 200 and r.get("removed") is True and ALICE not in m and BOB in m and n == AVANT + 1, f"{st} {r} {n}")
    st, r = geste("remove-member", {"hub_user_id": ALICE, "user_email": OLD})
    m, _ = membres()
    check("retirer encore : rien à retirer, Bob reste", st == 200 and r.get("removed") is False and BOB in m, f"{st} {r}")
    st, r = geste("restore-member", {"hub_user_id": ALICE, "user_email": OLD})
    m, n = membres()
    check("rétablir : Alice revient sous sa nouvelle adresse", st == 200 and r.get("restored") is True and m.get(ALICE, {}).get("email") == NEW and n == AVANT + 2, f"{st} {r} {n}")
    st, r = geste("sync-member", {"hub_user_id": ALICE, "email": OLD, "user_email": OLD, "role": "admin"})
    check("rôle : la même personne", st == 200 and r.get("app_user_id") == alice_local, f"{st} {r}")
    st, r = geste("attach-member", {"hub_user_id": ALICE, "hub_user_email": OLD, "role": "member"})
    check("attach : déjà membre, aucun double", st == 200 and r.get("already_member") is True, f"{st} {r}")

    # Refus dits, jamais une fusion.
    st, r = hub("POST", "/api/hub/users/sync", {"hub_user_id": ALICE, "email": OLD})
    check("adresse déjà prise : 409 email_conflict", st == 409 and "email_conflict" in str(r), f"{st} {r}")
    for proven in (False, True):
        st, r = geste("sync-member", {"hub_user_id": NOBODY, "email": OLD, "user_email": OLD, "email_proven": proven, "role": "member"})
        check(f"autre personne, adresse de Bob (attestée={proven}) : 409 hub_user_conflict", st == 409 and r.get("code") == "hub_user_conflict", f"{st} {r}")
    for nom in ("restore-member", "freeze-member", "unfreeze-member"):
        st, r = geste(nom, {"hub_user_id": NOBODY, "user_email": OLD})
        check(f"{nom} : inconnue 404", st == 404 and r.get("code") == "user_not_in_app", f"{st} {r}")
    st, r = geste("transfer-owner", {"owner_hub_user_id": NOBODY, "reason": "e2e"})
    check("transfer-owner : inconnue 404", st == 404 and r.get("code") == "user_not_in_app", f"{st} {r}")
    for nom in ("remove-member", "restore-member", "freeze-member", "unfreeze-member"):
        st, r = geste(nom, {"user_email": OLD})
        st2, r2 = geste(nom, {"hub_user_id": "pas-un-uuid", "user_email": OLD})
        check(f"{nom} : sans identifiant ou mal formé, 400", st == 400 and st2 == 400, f"{st} {st2}")
    m, n = membres()
    check("à la fin : Alice et Bob, personne d'autre", ALICE in m and BOB in m and n == AVANT + 2, f"{n}")
finally:
    if not a.keep:
        for who in (ALICE, BOB):
            geste("remove-member", {"hub_user_id": who, "reason": "e2e"})
            hub("POST", "/api/hub/users/sync", {"hub_user_id": who, "deleted": True})
        if not a.prod:
            hub("POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [WS]})
        else:
            _, n = membres()
            check("remise en état : le nombre de membres d'avant", n == AVANT, f"{n} / {AVANT}")
print("PROUVÉ" if not bad else f"DÉFAUT : {len(bad)}")
sys.exit(1 if bad else 0)
