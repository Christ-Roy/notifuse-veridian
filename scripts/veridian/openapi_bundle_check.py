#!/usr/bin/env python3
"""openapi_bundle_check.py : l'OpenAPI du dépôt se regroupe SANS --force, et openapi.json versionné en est le reflet.

Mesuré le 26/11/2026 : 35 références cassées (réponses « #/components/responses/... » visées depuis des fichiers de chemins
qui ne les définissaient pas) ; le bundle ne sortait qu'avec --force, donc des réponses vides dans la doc servie.
Deux contrôles :
  1. `redocly bundle openapi/openapi.yaml` sans --force : une référence introuvable = ROUGE ;
  2. openapi.json (versionné) égal au bundle frais (comparaison JSON) : sinon ROUGE, « make openapi-bundle ».
Trois états : 0 VERT, 1 ROUGE, 2 NON ABOUTI (npx absent, réseau, redocly qui ne répond pas).
  openapi_bundle_check.py <racine>             contrôle le dépôt
  openapi_bundle_check.py <racine> --selftest  provoque les deux casses sur une copie et exige qu'elles soient vues
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

REDOCLY = "@redocly/cli@2.84.0"  # version figée : un bundle d'une autre version diffère et passerait pour une dérive
REGEN = f"npx --yes {REDOCLY} bundle openapi/openapi.yaml -o openapi.json --ext json"


def bundle(spec_dir, out):
    """Rend (code, sortie). code 0 bundle fait, 1 erreurs de références, 2 outil indisponible."""
    if not shutil.which("npx"):
        return 2, "npx absent"
    try:
        p = subprocess.run(["npx", "--yes", REDOCLY, "bundle", os.path.join(spec_dir, "openapi.yaml"), "-o", out, "--ext", "json"],
                           capture_output=True, text=True, timeout=240)
    except subprocess.TimeoutExpired:
        return 2, "redocly : délai dépassé"
    txt = (p.stdout or "") + (p.stderr or "")
    if p.returncode == 0 and os.path.exists(out):
        return 0, txt
    if "bundle not created" in txt or "Can't resolve $ref" in txt:
        return 1, txt
    return 2, txt


def resume(txt, n=12):
    lignes = [l for l in txt.splitlines() if l.startswith("[") or "Can't resolve" in l or "❌" in l]
    return "\n".join(lignes[: n * 2])


def controle(root):
    spec = os.path.join(root, "openapi")
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, "openapi.json")
        code, txt = bundle(spec, out)
        if code == 2:
            print("openapi : NON ABOUTI, redocly indisponible :", txt.strip()[-300:])
            return 2
        if code == 1:
            print("openapi : ROUGE, le bundle exige --force (références introuvables) :")
            print(resume(txt))
            return 1
        frais = json.load(open(out))
    versionne_p = os.path.join(root, "openapi.json")
    if not os.path.exists(versionne_p):
        print("openapi : ROUGE, openapi.json absent ; lancer :", REGEN)
        return 1
    if json.load(open(versionne_p)) != frais:
        print("openapi : ROUGE, openapi.json versionné n'est pas le bundle des sources ; lancer :", REGEN, "puis commiter")
        return 1
    print(f"openapi : VERT, bundle sans --force, openapi.json à jour ({len(frais.get('paths', {}))} chemins)")
    return 0


def selftest(root):
    """Les deux casses, provoquées sur une copie, doivent rendre ROUGE ; la copie intacte, VERT."""
    with tempfile.TemporaryDirectory() as tmp:
        copie = os.path.join(tmp, "depot")
        os.makedirs(copie)
        shutil.copytree(os.path.join(root, "openapi"), os.path.join(copie, "openapi"))
        shutil.copy(os.path.join(root, "openapi.json"), os.path.join(copie, "openapi.json"))
        rc = controle(copie)
        if rc == 2:
            return 2
        if rc != 0:
            print("selftest ROUGE : la copie intacte ne passe pas")
            return 1
        # Casse 1 : une référence vers une réponse que le fichier ne définit pas (le défaut du 26/11).
        ref = "'../components/veridian-responses.yaml#/VeridianForbidden'"
        dossier = os.path.join(copie, "openapi", "paths")
        cible = next((os.path.join(dossier, n) for n in sorted(os.listdir(dossier)) if ref in open(os.path.join(dossier, n)).read()), None)
        if not cible:
            print("selftest NON ABOUTI : aucune référence partagée à briser dans openapi/paths")
            return 2
        s = open(cible).read()
        s2 = s.replace(ref, "'#/components/responses/ReponseInexistante'", 1)
        open(cible, "w").write(s2)
        if controle(copie) != 1:
            print("selftest ROUGE : une référence cassée n'est pas vue")
            return 1
        open(cible, "w").write(s)
        # Casse 2 : openapi.json qui ne suit plus ses sources.
        oj = os.path.join(copie, "openapi.json")
        d = json.load(open(oj))
        d.setdefault("paths", {})["/api/zz.fictif"] = {}
        json.dump(d, open(oj, "w"))
        if controle(copie) != 1:
            print("selftest ROUGE : un openapi.json périmé n'est pas vu")
            return 1
    print("selftest openapi : VERT (référence cassée vue, dérive de openapi.json vue, copie intacte verte)")
    return 0


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    racine = os.path.abspath(sys.argv[1])
    sys.exit(selftest(racine) if "--selftest" in sys.argv[2:] else controle(racine))
