#!/usr/bin/env python3
"""Contrôle de couverture CLI (Constitution CI, fiche 76) : chaque route /api/* du routeur
Go a une commande dans la CLI embarquée ; la CLI embarquée est intègre (MANIFEST) et
identique à la source de vérité quand elle est présente ; la CLI embarquée s'exécute
avec les SEULS fichiers qu'on distribue.

Appelé par scripts/veridian/cli-coverage-check.sh. Trois états, jamais deux :
  0 = VERT  · 1 = ROUGE (violation) · 2 = NON ABOUTI (mesure impossible, prévient sans bloquer)

Extraction du routeur (sans dépendance Go) :
  - littéral          mux.Handle("[METHOD ]/api/...")          -> route
  - helper            both("/api/...") / post("/api/...") ...   -> routes du fichier qui
                      enregistre via une variable de chemin (mux.Handle("GET "+path, ...))
  - concaténation     "/api/veridian/"+string(kind)+".move" + boucle `range Coll`
                      -> valeurs de Coll (littéraux ou constantes résolues dans le dépôt)
  - tout autre argument dynamique non résolu -> NON ABOUTI (pas de route passée sous silence)
"""
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

GO_FLOOR = 150  # plancher : en dessous, l'extraction est suspecte (grep cassé), cf. coverage_test.sh

RE_LIT = re.compile(r'\.Handle(?:Func)?\(\s*"(?:(?:GET|POST|PUT|DELETE|PATCH|HEAD) )?(/api/[^"]*)"\s*,')
RE_ARG = re.compile(r'\.Handle(?:Func)?\(\s*([^,\n]+),')
RE_LIT_ARG = re.compile(r'"(?:(?:GET|POST|PUT|DELETE|PATCH|HEAD) )?(/api/[^"]*)"')
RE_HELPER = re.compile(r'(?<![\w.])(\w+)\(\s*"(/api/[^"]*)"')
RE_CONCAT = re.compile(r'"(?:(?:GET|POST|PUT|DELETE|PATCH|HEAD) )?(/api/[^"]*)"\s*\+\s*string\((\w+)\)\s*\+\s*"([^"]*)"')
RE_LOOP = re.compile(r'for\s+_\s*,\s*(\w+)\s*:=\s*range\s+([\w.]+)')


def go_sources(root):
    """Fichiers Go non-test sous internal/, cmd/, pkg/ (jamais vendor ni node_modules)."""
    for sub in ("internal", "cmd", "pkg"):
        base = os.path.join(root, sub)
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = [d for d in dirnames if d not in ("vendor", "node_modules", "testdata")]
            for fn in filenames:
                if fn.endswith(".go") and not fn.endswith("_test.go"):
                    yield os.path.join(dirpath, fn)


def resolve_collection(name, corpus):
    """Valeurs d'un `var NAME = []T{A, B, "c"}` : identifiants résolus en constantes du dépôt."""
    m = re.search(r'var\s+' + re.escape(name) + r'\s*=\s*\[\]\w+\s*\{([^}]*)\}', corpus)
    if not m:
        return None
    values = []
    for tok in [t.strip() for t in m.group(1).split(",") if t.strip()]:
        if tok.startswith('"'):
            values.append(tok.strip('"'))
            continue
        c = re.search(r'\b' + re.escape(tok) + r'\s+\w+\s*=\s*"([^"]*)"', corpus) or \
            re.search(r'\b' + re.escape(tok) + r'\s*=\s*"([^"]*)"', corpus)
        if not c:
            return None
        values.append(c.group(1))
    return values


def extract_routes(root):
    """Retourne (routes:set, non_resolues:list[(fichier, expression)])."""
    files = list(go_sources(root))
    texts = {}
    for path in files:
        with open(path, encoding="utf-8", errors="replace") as f:
            texts[path] = f.read()
    corpus = "\n".join(texts.values())
    routes, unresolved = set(), []
    for path, text in texts.items():
        text = "\n".join(l for l in text.split("\n") if not l.lstrip().startswith("//"))
        if not re.search(r'\.Handle(?:Func)?\(', text):
            continue
        routes.update(RE_LIT.findall(text))
        needs_helper = False
        for m in RE_ARG.finditer(text):
            arg = m.group(1).strip()
            if RE_LIT_ARG.fullmatch(arg):
                continue  # déjà pris par RE_LIT
            if re.fullmatch(r'"(?:(?:GET|POST|PUT|DELETE|PATCH|HEAD) )?/[^"]*"', arg):
                continue  # littéral hors /api (pages, webhooks, santé)
            if '/api/' in arg and "string(" in arg:
                c = RE_CONCAT.search(arg)
                if not c:
                    unresolved.append((os.path.relpath(path, root), arg))
                    continue
                prefix, var, suffix = c.group(1), c.group(2), c.group(3)
                coll = None
                loop_vars = {lm.group(1): lm.group(2) for lm in RE_LOOP.finditer(text)}
                origin = var
                for _ in range(5):  # suit les alias `kind := k` jusqu'à la variable de boucle
                    if origin in loop_vars:
                        break
                    a = re.search(r'\b' + re.escape(origin) + r'\s*:=\s*(\w+)\b', text)
                    if not a:
                        break
                    origin = a.group(1)
                if origin in loop_vars:
                    coll = loop_vars[origin].split(".")[-1]
                values = resolve_collection(coll, corpus) if coll else None
                if not values:
                    unresolved.append((os.path.relpath(path, root), arg))
                    continue
                routes.update(prefix + v + suffix for v in values)
                continue
            if re.search(r'\+\s*\w+|^\w+$', arg):
                needs_helper = True  # chemin porté par une variable : routes = appels du fichier
                continue
            unresolved.append((os.path.relpath(path, root), arg))
        if needs_helper:
            routes.update(p for _, p in RE_HELPER.findall(text))
    return routes, unresolved


def cli_routes(cli_dir, override=None):
    """Routes déclarées par la CLI embarquée (`__routes`), ou override JSON (selftest)."""
    if override:
        with open(override, encoding="utf-8") as f:
            return set(json.load(f)), None
    try:
        r = subprocess.run([sys.executable, os.path.join(cli_dir, "notifuse_common.py"), "__routes"],
                           capture_output=True, text=True, timeout=120, cwd=cli_dir)
    except (OSError, subprocess.SubprocessError) as e:
        return None, f"exécution impossible ({type(e).__name__})"
    if r.returncode != 0:
        tail = (r.stderr.strip().splitlines() or ["?"])[-1]
        return None, f"__routes en échec (rc={r.returncode}) : {tail}"
    try:
        return set(json.loads(r.stdout)), None
    except ValueError:
        return None, "__routes : sortie non JSON"


def read_exemptions(path):
    """Routes volontairement sans commande : {route: raison}. Fichier absent = aucune."""
    out = {}
    if not path or not os.path.isfile(path):
        return out
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\n")
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            route, _, raison = line.partition("\t")
            out[route.strip()] = raison.strip()
    return out


def read_manifest(agentcli):
    """Lit MANIFEST.sha256 : {nom: sha256}."""
    out = {}
    with open(os.path.join(agentcli, "MANIFEST.sha256"), encoding="utf-8") as f:
        for line in f:
            parts = line.split()
            if len(parts) == 2:
                out[parts[1]] = parts[0]
    return out


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def check_manifest_integrity(agentcli, manifest):
    """Chaque fichier listé existe et a le hash du MANIFEST. Retourne la liste des écarts."""
    ecarts = []
    for name, h in manifest.items():
        p = os.path.join(agentcli, name)
        if not os.path.isfile(p):
            ecarts.append(f"{name} : absent du dossier embarqué")
        elif sha256(p) != h:
            ecarts.append(f"{name} : hash différent du MANIFEST (édition à la main ?)")
    return ecarts


def check_source_divergence(agentcli, manifest, source_dir):
    """Compare la copie embarquée à la source de vérité (skill). None si la source est absente."""
    if not source_dir or not os.path.isdir(source_dir):
        return None
    ecarts = []
    for name in manifest:
        src = os.path.join(source_dir, name)
        if not os.path.isfile(src):
            ecarts.append(f"{name} : absent de la source {source_dir}")
        elif sha256(src) != manifest[name]:
            ecarts.append(f"{name} : la source de vérité a changé, resynchroniser (scripts/veridian/sync-agent-cli.sh)")
    return ecarts


def check_shipped_set(agentcli, manifest):
    """La CLI doit tourner avec EXACTEMENT les fichiers qu'on distribue (tar = MANIFEST)."""
    tmp = tempfile.mkdtemp(prefix="cli-shipped-")
    try:
        for name in manifest:
            shutil.copy(os.path.join(agentcli, name), os.path.join(tmp, name))
        r = subprocess.run([sys.executable, os.path.join(tmp, "notifuse_common.py"), "__routes"],
                           capture_output=True, text=True, timeout=120, cwd=tmp)
        if r.returncode != 0:
            tail = (r.stderr.strip().splitlines() or ["?"])[-1]
            return f"CLI distribuée non exécutable avec les seuls fichiers du MANIFEST : {tail}"
        return None
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def run(root, source_dir=None, override=None, exemptions_path=None):
    agentcli = os.path.join(root, "internal", "http", "agentcli")
    if not os.path.isfile(os.path.join(agentcli, "MANIFEST.sha256")):
        print("NON ABOUTI : MANIFEST.sha256 introuvable (CLI embarquée absente)")
        return 2
    manifest = read_manifest(agentcli)
    rouge, abouti = [], []

    ecarts = check_manifest_integrity(agentcli, manifest)
    if ecarts:
        rouge.append("intégrité : " + "; ".join(ecarts))
    else:
        abouti.append(f"intégrité : {len(manifest)} fichiers conformes au MANIFEST")

    src = check_source_divergence(agentcli, manifest, source_dir)
    if src is None:
        abouti.append("source de vérité absente sur cette machine : divergence non mesurée (MANIFEST seul)")
    elif src:
        rouge.append("divergence source : " + "; ".join(src))
    else:
        abouti.append(f"source de vérité ({source_dir}) identique à la copie embarquée")

    shipped = check_shipped_set(agentcli, manifest)
    if shipped:
        rouge.append(shipped)
    else:
        abouti.append("CLI exécutable avec les seuls fichiers du MANIFEST")

    go_routes, unresolved = extract_routes(root)
    if unresolved:
        print("NON ABOUTI : routes à argument dynamique non résolu (ajouter la résolution ou une littérale) :")
        for f, e in unresolved:
            print(f"  - {f} : {e[:120]}")
        return 2
    if len(go_routes) < GO_FLOOR:
        print(f"NON ABOUTI : extraction Go suspecte ({len(go_routes)} routes, plancher {GO_FLOOR})")
        return 2

    cli, err = cli_routes(agentcli, override)
    if cli is None:
        # La CLI distribuée ne s'exécute pas : c'est un défaut de l'artefact livré, pas une mesure impossible.
        rouge.append(f"CLI embarquée non exécutable (__routes) : {err}")
        for line in abouti:
            print("OK  " + line)
        for line in rouge:
            print("ROUGE " + line)
        return 1

    exemptions = read_exemptions(exemptions_path)
    perimees = sorted(r for r in exemptions if r not in go_routes)
    if perimees:
        rouge.append("exemption(s) périmée(s) (route absente du routeur, retirer de la liste) : " + ", ".join(perimees))
    manquantes = sorted(go_routes - cli - set(exemptions))
    orphelines = sorted(cli - go_routes)
    exemptees = sorted(r for r in exemptions if r in go_routes and r not in cli)
    if manquantes:
        rouge.append(f"{len(manquantes)} route(s) /api/* du routeur SANS commande CLI : " + ", ".join(manquantes))
    else:
        abouti.append(f"couverture : {len(go_routes)} routes Go, toutes ont une commande CLI "
                      f"(ou une exemption motivée)")
    if exemptees:
        abouti.append(f"exemptées (sans commande, motivées dans scripts/veridian/cli-route-exemptions.txt) : "
                      + ", ".join(exemptees))
    if orphelines:
        abouti.append(f"avertissement : {len(orphelines)} commande(s) CLI visent une route absente du routeur : "
                      + ", ".join(orphelines))

    for line in abouti:
        print("OK  " + line)
    if rouge:
        for line in rouge:
            print("ROUGE " + line)
        return 1
    print("VERT : CLI embarquée à jour, toute route /api/* du routeur a sa commande.")
    return 0


def selftest():
    """Éprouve l'extraction sur un routeur fabriqué : littéral, helper, concaténation, fictif."""
    tmp = tempfile.mkdtemp(prefix="cli-cover-selftest-")
    try:
        def w(rel, body):
            p = os.path.join(tmp, rel)
            os.makedirs(os.path.dirname(p), exist_ok=True)
            with open(p, "w", encoding="utf-8") as f:
                f.write(body)
        w("internal/http/a.go", 'package http\nfunc R(mux *M){ mux.Handle("GET /api/x.lit", h); mux.Handle("/t/", h) }\n')
        w("internal/veridian/pol/h.go", 'package pol\nfunc R(mux *M){\n both := func(p string, f F){ mux.Handle("GET "+p, f); mux.Handle("POST "+p, f) }\n both("/api/x.help", g)\n}\n')
        w("internal/veridian/folders/model.go", 'package folders\nconst (\n KindSegments Kind = "segments"\n KindLists Kind = "lists"\n)\nvar Kinds = []Kind{KindSegments, KindLists}\n')
        w("internal/veridian/folders/api/h.go", 'package api\nfunc R(mux *M){\n for _, k := range folders.Kinds {\n  kind := k\n  mux.Handle("POST /api/veridian/"+string(kind)+".move", a(kind))\n }\n}\n')
        routes, unresolved = extract_routes(tmp)
        expected = {"/api/x.lit", "/api/x.help", "/api/veridian/segments.move", "/api/veridian/lists.move"}
        if routes != expected or unresolved:
            print(f"selftest ROUGE extraction : obtenu={sorted(routes)} non_résolu={unresolved}")
            return 2
        # fictif : une route sans commande DOIT sortir dans le manque
        w("internal/http/fake.go", 'package http\nfunc R(mux *M){ mux.Handle("POST /api/zz.fictif", h) }\n')
        routes2, _ = extract_routes(tmp)
        cli = expected  # la CLI couvre tout sauf le fictif
        if sorted(routes2 - cli) != ["/api/zz.fictif"]:
            print("selftest ROUGE : la route fictive n'est pas détectée")
            return 2
        print("selftest OK : extraction littérale, helper, concaténation résolue, route fictive détectée")
        return 0
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main(argv):
    if len(argv) < 2:
        print("usage: cli_coverage_check.py <racine-du-depot> [--source DIR] [--cli-routes-json FICHIER] | --selftest")
        return 2
    if argv[1] == "--selftest":
        return selftest()
    root = os.path.abspath(argv[1])
    source_dir = None
    override = None
    if "--source" in argv:
        source_dir = argv[argv.index("--source") + 1]
    if "--cli-routes-json" in argv:
        override = argv[argv.index("--cli-routes-json") + 1]
    exemptions = os.path.join(os.path.dirname(os.path.abspath(__file__)), "cli-route-exemptions.txt")
    return run(root, source_dir, override, exemptions)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
