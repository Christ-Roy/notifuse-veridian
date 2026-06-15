#!/usr/bin/env python3
"""Rendu du manifeste IAC cold → JSON résolu (secrets ${VAR} injectés).

But (Robert 2026-06-15) : « tout poser comme IAC idempotent pour avoir d'un
coup d'œil notre contenu et l'éditer facilement depuis des fichiers ».

Ce module est la BRIQUE de rendu du CLI plan/apply (notifuse-iac.sh) :
  - lit le YAML déclaratif (iac/coldtunnel/workspace.yaml),
  - résout les placeholders ${VAR} depuis l'environnement (chargé par le
    wrapper bash depuis ~/credentials/.all-creds.env),
  - émet un JSON normalisé sur stdout.

🔴 SÉCURITÉ : les secrets résolus ne sortent QUE sur stdout (piped en mémoire
par le wrapper bash, jamais écrit sur disque). Le mode --redact remplace toute
valeur issue d'un ${VAR} par "***" → c'est ce que le `plan` affiche à l'écran.
Un ${VAR} non résolu en mode apply = erreur dure (on ne pousse pas un littéral
"${SMTP_...}" vers l'API).

Usage :
  render.py <manifest.yaml>            # JSON résolu (secrets en clair) — pour apply (piped)
  render.py --redact <manifest.yaml>   # JSON avec secrets masqués — pour plan (affichable)
"""
import json
import os
import re
import sys

import yaml

# ${VAR} ou ${VAR:-default}. On ne supporte QUE ${...} (pas $VAR nu) pour rester
# explicite et éviter de capturer accidentellement un $ littéral d'un mot de passe.
_VAR_RE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}")


class UnresolvedVar(Exception):
    pass


def _resolve_str(value, redact, missing):
    """Résout les ${VAR} dans une string. Retourne (résolu, a_touché_un_secret)."""
    touched = False

    def repl(m):
        nonlocal touched
        name = m.group(1)
        default = m.group(2)
        if name in os.environ and os.environ[name] != "":
            touched = True
            return "***" if redact else os.environ[name]
        if default is not None:
            # Une valeur par défaut littérale n'est PAS un secret → pas de redaction.
            return default
        # Variable absente et sans défaut.
        missing.append(name)
        touched = True
        return "***" if redact else m.group(0)  # garde le placeholder en clair → erreur dure ensuite

    out = _VAR_RE.sub(repl, value)
    return out, touched


def resolve(node, redact, missing):
    if isinstance(node, dict):
        return {k: resolve(v, redact, missing) for k, v in node.items()}
    if isinstance(node, list):
        return [resolve(v, redact, missing) for v in node]
    if isinstance(node, str):
        out, _ = _resolve_str(node, redact, missing)
        return out
    return node


def main(argv):
    redact = False
    args = list(argv[1:])
    if args and args[0] == "--redact":
        redact = True
        args = args[1:]
    if len(args) != 1:
        sys.stderr.write("usage: render.py [--redact] <manifest.yaml>\n")
        return 2

    with open(args[0], "r", encoding="utf-8") as fh:
        manifest = yaml.safe_load(fh)

    missing = []
    resolved = resolve(manifest, redact, missing)

    if missing and not redact:
        # En mode apply (non redact) : variable manquante = échec dur, on ne
        # pousse JAMAIS un placeholder littéral vers l'API.
        uniq = sorted(set(missing))
        sys.stderr.write(
            "ERREUR: variables d'environnement non résolues: %s\n"
            "  → vérifier ~/credentials/.all-creds.env\n" % ", ".join(uniq)
        )
        return 3

    json.dump(resolved, sys.stdout, ensure_ascii=False, sort_keys=False)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
