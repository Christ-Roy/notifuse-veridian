#!/usr/bin/env python3
# Outil durable (27/11/2026, VER-265) : la CSP de la console mesurée en PROD dans un vrai navigateur, connexion par le Hub
# comprise. Fiche 96.
"""Émet un lien de connexion à usage unique pour le propriétaire de l'espace (HMAC, /api/sso/issue-magic-link), puis lance
tests/e2e-veridian/csp-parcours.mjs par `lourd --navigateur` (le lien passe en mémoire, jamais écrit) : les pages de la
console, l'éditeur de gabarits (Monaco), d'où vient Monaco, et le contrôle positif (un onerror injecté doit rester bloqué).
Lecture seule : aucune donnée n'est modifiée.

Usage : csp-parcours-prod.py <espace> [--sortie <dossier des captures>] [--firefox] [--parcours captures-console.mjs --env LARGEUR=390]
Sortie : la ligne RESULTAT du parcours ; 0 = vert, 1 = violation ou contrôle positif muet ou Monaco venu d'un CDN, 2 = non abouti."""
import argparse, base64, os, subprocess, sys, urllib.parse

ap = argparse.ArgumentParser()
ap.add_argument("ws")
ap.add_argument("--sortie", default=".")
ap.add_argument("--firefox", action="store_true")
ap.add_argument("--parcours", default="csp-parcours.mjs", help="autre parcours de tests/e2e-veridian (ex. captures-console.mjs), même connexion")
ap.add_argument("--env", action="append", default=[], help="variable passée au parcours, CLE=valeur (ex. LARGEUR=390)")
a = ap.parse_args()
import re  # noqa: E402
if not re.fullmatch(r"[a-z0-9-]+\.mjs", a.parcours) or not re.fullmatch(r"[a-z0-9]{1,40}", a.ws):
    print("NON ABOUTI : parcours (nom de fichier .mjs) ou espace invalide")
    sys.exit(2)
sys.path.insert(0, os.path.expanduser("~/.claude/skills/notifuse-cli/bin"))
import notifuse_common as nc  # noqa: E402

st, p = nc.call_hmac("prod", "GET", "/api/veridian/membres?workspace_id=" + urllib.parse.quote(a.ws))
mem = (p or {}).get("members") or []
owner = next((m for r in ("owner", "admin") for m in mem if m.get("role") == r and m.get("hub_user_id")), None)
if not owner:
    print("NON ABOUTI : aucun propriétaire relié au Hub", st)
    sys.exit(2)
st, r = nc.call_hmac("prod", "POST", "/api/sso/issue-magic-link",
                     {"hub_user_id": owner["hub_user_id"], "email": owner["email"], "next": f"/console/workspace/{a.ws}"})
link = (r or {}).get("magic_link_url")
if st != 200 or not link:
    print("NON ABOUTI : lien de connexion", st)
    sys.exit(2)
racine = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
nav = "NAVIGATEUR=firefox " if a.firefox else ""
extra = "".join(f"{e} " for e in a.env if e.split("=", 1)[0].isidentifier() and "'" not in e and " " not in e)
# Playwright 1.60.0 : la version des navigateurs de l'image lourd --navigateur (/ms-playwright).
cmd = ["lourd", "--navigateur", "--secrets", "LIEN", "sh", "-c",
       f"npm i --no-audit --no-fund @playwright/test@1.60.0 >/dev/null 2>&1 && PLAYWRIGHT_BROWSERS_PATH=/ms-playwright EXIGER_MONACO=1 {nav}{extra}node {a.parcours} {a.ws}"]
pr = subprocess.run(cmd, env=dict(os.environ, LIEN=link), capture_output=True, text=True, timeout=1800,
                    cwd=os.path.join(racine, "tests", "e2e-veridian"))
os.makedirs(a.sortie, exist_ok=True)
for line in pr.stdout.splitlines():
    if line.startswith("CAPTURE "):
        _, nom, b64 = line.split(" ", 2)
        open(os.path.join(a.sortie, f"{os.path.splitext(a.parcours)[0]}_{nom}.jpg"), "wb").write(base64.b64decode(b64))
    elif line.startswith("RESULTAT") or "NON ABOUTI" in line:
        print(line)
print("code", pr.returncode)
if pr.returncode not in (0, 1):
    print(pr.stderr[-800:])
sys.exit(pr.returncode if pr.returncode in (0, 1, 2) else 2)
