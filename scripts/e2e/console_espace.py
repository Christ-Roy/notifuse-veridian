"""Ouvrir la console pour une mesure, dans UN espace et aucun autre (25/11/2026 : une mesure partie pour un espace à nous a ouvert celui d'un client).

    with espace(navigateur, "prod", "robertbrunon") as page: ...

La garde est posée AVANT le premier chargement, dans le navigateur : toute requête /api/ qui vise un autre espace est coupée avant de partir, et
l'épreuve s'arrête, sortie 2. Par défaut (lecture) toute écriture est coupée aussi. L'espace ouvert est relu dans l'adresse.

Épreuve : python3 scripts/e2e/console_espace.py   (staging, deux espaces jetables ; NAVIGATEUR_CDP, défaut http://127.0.0.1:9444) : 0, 1 ou 2.
"""
import contextlib, json, os, sys, time, urllib.parse, uuid

sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

LECTURES_POST = {"/api/analytics.query"}  # POST qui ne font que lire
SANS_ESPACE = ("/api/user.me", "/api/veridian/mode", "/api/setup.status")


class AutreEspace(SystemExit):
    def __init__(self, quoi):
        print(f"ARRÊT : {quoi}", file=sys.stderr)
        super().__init__(2)


def vise(request):
    """Espaces nommés par une requête : paramètre (workspace_id, ou id des routes workspaces.*), corps JSON. Ensemble vide : aucun."""
    u = urllib.parse.urlparse(request.url)
    q = urllib.parse.parse_qs(u.query)
    found = set(q.get("workspace_id", []))
    if u.path.startswith("/api/workspaces."):
        found |= set(q.get("id", []))
    try:
        body = json.loads(request.post_data or "null")
    except ValueError:
        body = None
    if isinstance(body, dict):
        found |= {v for k, v in body.items() if v and (k == "workspace_id" or (k == "id" and u.path.startswith("/api/workspaces.")))}
    return found


@contextlib.contextmanager
def espace(navigateur, env, ws, lecture=True, lien=None, **options):
    ctx = navigateur.new_context(**{"viewport": {"width": 1440, "height": 900}, "locale": "fr-FR", **options})
    coupees = []

    def garde(route):
        r = route.request
        path = urllib.parse.urlparse(r.url).path
        autres = vise(r) - {ws}
        if autres:
            coupees.append(f"{r.method} {path} vise {sorted(map(str, autres))}, attendu {ws}")
        elif lecture and r.method not in ("GET", "HEAD", "OPTIONS") and path not in LECTURES_POST:
            coupees.append(f"{r.method} {path} : écriture coupée, mesure en lecture")
        else:
            return route.continue_()
        route.abort()

    def controle():
        if coupees:
            raise AutreEspace(coupees[0])

    try:
        ctx.route("**/api/**", garde)
        page = ctx.new_page()
        page.controle = controle
        page.goto(lien or nc.owner_login_url(env, ws), timeout=60000)
        page.wait_for_url("**/console/**", timeout=60000)
        page.wait_for_timeout(2500)
        controle()
        chemin = urllib.parse.urlparse(page.url).path
        if not (chemin + "/").startswith(f"/console/workspace/{ws}/"):
            raise AutreEspace(f"la console a ouvert {chemin}, attendu l'espace {ws}")
        yield page
        controle()
    finally:
        ctx.close()


def epreuve():
    from playwright.sync_api import sync_playwright
    n = str(int(time.time()))[-7:]
    a, b = f"garde{n}a", f"garde{n}b"
    mail = f"garde{n}@e2e.veridian.site"
    hub = str(uuid.uuid5(uuid.NAMESPACE_URL, mail))
    bad = []

    def lien(suite=None):
        return nc.call_hmac("staging", "POST", "/api/sso/issue-magic-link", {"hub_user_id": hub, "email": mail, **({"next": suite} if suite else {})})[1]["magic_link_url"]

    def attendu(nom, arret, geste):
        try:
            geste()
            vu = False
        except AutreEspace:
            vu = True
        ok = vu == arret
        print(("ok   " if ok else "FAUX ") + nom + (" : arrêté" if vu else " : passé"))
        if not ok:
            bad.append(nom)

    try:
        for w in (a, b):  # b en dernier : c'est lui que l'ancien atterrissage choisissait
            st, _ = nc.call_hmac("staging", "POST", "/api/tenants/provision", {"tenant_id": w, "owner_email": mail, "plan": "free", "owner_hub_user_id": hub})
            if st != 200:
                print(f"NON ABOUTI : provision HTTP {st}")
                return 2
        with sync_playwright() as p:
            nav = p.chromium.connect_over_cdp(os.environ.get("NAVIGATEUR_CDP", "http://127.0.0.1:9444"))

            def ouvre(ws, suite, geste=None, lecture=True):
                with espace(nav, "staging", ws, lecture=lecture, lien=lien(suite)) as page:
                    if geste:
                        geste(page)

            base = nc.BASES["staging"]
            api = "([m,u,b]) => fetch(u,{method:m,headers:{'Content-Type':'application/json',Authorization:'Bearer '+localStorage.getItem('auth_token')},body:b}).catch(()=>0)"
            attendu("espace demandé", False, lambda: ouvre(a, f"/console/workspace/{a}"))
            attendu("atterrissage dans un autre espace", True, lambda: ouvre(a, f"/console/workspace/{b}"))
            attendu("aucun espace demandé, deux possibles", True, lambda: ouvre(a, None))
            attendu("page d'un autre espace ouverte en route", True,
                    lambda: ouvre(a, f"/console/workspace/{a}", lambda pg: (pg.goto(f"{base}/console/workspace/{b}/lists"), pg.wait_for_timeout(4000))))
            attendu("écriture en lecture seule", True,
                    lambda: ouvre(a, f"/console/workspace/{a}", lambda pg: pg.evaluate(api, ["POST", "/api/lists.create", json.dumps({"workspace_id": a, "id": "refusee", "name": "r"})])))
            attendu("même écriture, permise", False,
                    lambda: ouvre(a, f"/console/workspace/{a}", lambda pg: pg.evaluate(api, ["POST", "/api/lists.create", json.dumps({"workspace_id": a, "id": "permise", "name": "p"})]), lecture=False))
        ids = [x["id"] for x in nc.call_jwt("staging", "GET", "/api/lists.list", nc.owner_jwt("staging", a), params={"workspace_id": a})[1].get("lists") or []]
        ok = "permise" in ids and "refusee" not in ids
        print(("ok   " if ok else "FAUX ") + f"listes relues côté serveur : {ids} (permise oui, refusee non)")
        if not ok:
            bad.append("listes")
    finally:
        nc.call_hmac("staging", "POST", "/api/veridian/admin/wipe-test-tenants", {"tenant_ids": [a, b], "safety_client_prefixes": ["canary"]})
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(epreuve())
