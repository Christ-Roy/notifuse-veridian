#!/usr/bin/env python3
"""L'organisation vient du Hub (fiche 88) : poussée signée, relecture, puis la console LUE dans Chromium. Sort 0 (prouvé), 1 (défaut), 2 (non abouti).

Défaut : staging, espace jetable, retiré à la fin. --prod --ws <espace> --tenant <hub_tenant_id> : pousse la couleur seule, sous le nom
actuel (ni nom ni logo touchés), en version 1 et 2 (le Hub pousse des millisecondes : il reste devant), puis la retire. Dépend de notifuse-admin et playwright."""
import argparse, os, subprocess, sys, time, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

a = argparse.ArgumentParser()
a.add_argument("--prod", action="store_true"); a.add_argument("--ws"); a.add_argument("--tenant"); a.add_argument("--capture"); a.add_argument("--keep", action="store_true")
a = a.parse_args()
ENV = "prod" if a.prod else "staging"
BASE = "https://notifuse.app.veridian.site" if a.prod else "https://notifuse.staging.veridian.site"
WS, TENANT = a.ws or f"orga{int(time.time()) % 10**7}", a.tenant or str(uuid.uuid4())
COLOR, OTHER = "#b3261e", "#0b5c2e"
LOGO = "https://app.veridian.site/icon.svg"
OWNER = "orga-e2e@exemple.test"
bad = []


def check(name, ok, seen=""):
    print("ok " if ok else "KO ", name, seen)
    if not ok:
        bad.append(name)


if a.prod and not (a.ws and a.tenant):
    sys.exit("NON ABOUTI : --prod exige --ws et --tenant")
if not a.prod:
    st, p = nc.call_hmac(ENV, "POST", "/api/tenants/provision", {"tenant_id": WS, "owner_email": OWNER, "workspace_name": "Avant"})
    if st in (200, 201):
        st, p = nc.call_hmac(ENV, "POST", "/api/tenants/update-plan", {"tenant_id": WS, "plan": "free", "hub_tenant_id": TENANT})
    if st not in (200, 201):
        print("NON ABOUTI : préparation de l'espace", st, p.get("error")); sys.exit(2)
jwt = nc._owner_jwt_impl(ENV, WS, **({} if a.prod else {"email": OWNER}))
SYNC = "/api/hub/organisations/sync"


def push(version, brand, name=""):
    return nc.call_hmac(ENV, "POST", SYNC, {"hub_tenant_id": TENANT, "version": version, "nom": name, "type": "organisation", "marque": brand, "modules": ["notifuse"], "membres": [], "reglages": {}})


def relu():
    return nc.call_hmac(ENV, "GET", f"{SYNC}?hub_tenant_id={TENANT}")


brand = {"couleur_primaire": COLOR, "couleur_accent": None, "logo_url": None, "favicon_url": None}
name = ""
if not a.prod:
    name = "Marque E2E"
    brand.update(nom_affiche=name, logo_url=LOGO, favicon_url=LOGO)
st, p = push(1, brand, name)
check("poussée appliquée", st == 200 and p.get("applied") is True and p.get("workspace_id") == WS, f"{st} {p}")
st, p = push(1, brand, name)
check("rejeu : unchanged", st == 200 and p.get("applied") is False and p.get("reason") == "unchanged", p)
st, p = relu()
check("version relue", st == 200 and p.get("version") == 1, f"{st} {p}")
st, p = nc.call_hmac(ENV, "GET", f"{SYNC}?hub_tenant_id=inconnu-{int(time.time())}")
check("tenant inconnu : 404", st == 404, st)
st, p = nc.call_anon(ENV, "POST", SYNC, body={})
check("sans signature : 401", st == 401, st)
st, p = nc.call_jwt(ENV, "GET", "/api/veridian/organisation", jwt, params={"workspace_id": WS})
check("marque lue par un membre", st == 200 and p.get("managed") is (not a.prod) and (p.get("brand") or {}).get("couleur_primaire") == COLOR, f"{st} {p}")

try:
    from playwright.sync_api import sync_playwright
    with sync_playwright() as pw:
        b = pw.chromium.launch()
        ctx = b.new_context(viewport={"width": 1400, "height": 900}, locale="fr-FR")
        ctx.add_init_script(f"localStorage.setItem('auth_token', {jwt!r})")
        page = ctx.new_page()

        def console():
            page.goto(f"{BASE}/console/workspace/{WS}/settings/general", wait_until="networkidle", timeout=90000)
            page.wait_for_selector(".ant-form-item", timeout=40000)
            page.wait_for_timeout(2000)  # une absence se lit après que la chose a eu le temps d'arriver
            locked = page.locator("[data-testid=organisation-notice]").count() > 0
            check("console : nom et logo verrouillés si le Hub donne un nom, sinon libres", locked is (not a.prod), locked)
            return page.evaluate("""() => {
              const m = document.querySelector('.ant-menu-item-selected, .ant-tabs-tab-active .ant-tabs-tab-btn');
              const all = [...document.querySelectorAll('*')].flatMap(e => { const s = getComputedStyle(e); return [s.color, s.backgroundColor, s.borderColor] });
              return { selected: m && getComputedStyle(m).color, colors: [...new Set(all)],
                favicon: (document.querySelector('link[data-veridian-brand]') || {}).href || null, imgs: [...document.images].map(i => [i.src, i.naturalWidth]), text: document.body.innerText }
            }""")

        rgb = lambda h: "rgb(%d, %d, %d)" % tuple(int(h[i:i + 2], 16) for i in (1, 3, 5))
        v = console()
        check("console : la couleur du Hub est à l'écran", rgb(COLOR) in v["colors"], v["selected"])
        if not a.prod:
            check("console : le nom du Hub", name in v["text"])
            check("console : le logo du Hub", any(s == LOGO and w > 0 for s, w in v["imgs"]), [s for s, _ in v["imgs"]])
            check("console : le favicon du Hub", v["favicon"] == LOGO, v["favicon"])
        if a.capture:
            page.screenshot(path=a.capture)
        st, p = push(2, {**brand, "couleur_primaire": OTHER}, name)
        check("version 2 appliquée", st == 200 and p.get("applied") is True, f"{st} {p}")
        v = console()
        check("console : la couleur suit, l'ancienne a disparu", rgb(OTHER) in v["colors"] and rgb(COLOR) not in v["colors"], v["selected"])
        st, p = push(1, brand, name)
        check("version ancienne : stale", st == 200 and p.get("applied") is False and p.get("reason") == "stale" and p.get("version") == 2, p)
        b.close()
except Exception as e:  # noqa: BLE001 : une console illisible est un défaut ou une mesure non aboutie, jamais un vert
    check("console lue", False, str(e)[:300])
if a.prod:
    st, p = push(3, {"couleur_primaire": None}, "")
    check("couleur retirée", st == 200 and p.get("applied") is True, f"{st} {p}")
elif not a.keep:
    subprocess.run(["notifuse-admin", "--env", ENV, "wipe", "--ids", WS], capture_output=True, timeout=120)
sys.exit(1 if bad else 0)
