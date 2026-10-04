#!/usr/bin/env python3
"""check-skill-examples-parse.py (Notifuse Veridian)

Garde-fou mission 2026-10-04 "audit skill distribue" : le skill embarque au
build (internal/http/agentskill/AGENTS.md + SKILL.md, servi par
/agent/skill.tar.gz) contenait des exemples `notifuse ...` qui ne
parsaient pas (positional manquant, flags inexistants) -- une doc client
qui divergeait du CLI reel SANS que rien ne le detecte.

Ce script extrait chaque bloc ```bash des deux fichiers, en tire les lignes
qui commencent par `notifuse `, et les fait parser par le VRAI argparse du
CLI embarque (internal/http/agentcli/notifuse_common.py -- la copie
versionnee servie en prod, pas la source sur le poste de l'operateur).

Les placeholders (<workspace>, <id>, <email>, ...) sont des tokens de
chaine valides pour argparse (il ne valide que les NOMS de commandes/flags,
pas le contenu) : ce n'est pas un faux negatif, c'est exactement ce qu'on
veut verifier ici.

Trois etats :
  0 vert   : tous les exemples parsent
  1 rouge  : au moins un exemple ne parse pas (liste exacte affichee)
  2 non abouti : fichier(s) source introuvable(s), ou CLI embarque introuvable
"""
import contextlib
import importlib.machinery
import importlib.util
import io
import os
import re
import shlex
import sys

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SKILL_FILES = [
    os.path.join(REPO_ROOT, "internal/http/agentskill/AGENTS.md"),
    os.path.join(REPO_ROOT, "internal/http/agentskill/SKILL.md"),
]
CLI_COMMON_PATH = os.path.join(REPO_ROOT, "internal/http/agentcli/notifuse_common.py")

BASH_BLOCK_RE = re.compile(r"```bash\n(.*?)```", re.DOTALL)
PLACEHOLDER_RE = re.compile(r"<[a-zA-Z0-9_]+>")


def load_cli_module():
    loader = importlib.machinery.SourceFileLoader("notifuse_common_skill_check", CLI_COMMON_PATH)
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def _join_continuations(block_lines):
    """Rejoint les lignes terminees par un backslash (continuation shell
    multi-ligne, ex. integrations:cold --rates ... \n  --daily-cap ...)."""
    joined = []
    buf = ""
    for raw_line in block_lines:
        stripped = raw_line.rstrip()
        if buf:
            stripped = stripped.strip()
        if stripped.endswith("\\"):
            buf += stripped[:-1] + " "
        else:
            joined.append(buf + stripped)
            buf = ""
    if buf:
        joined.append(buf)
    return joined


def strip_trailing_comment(line):
    """Retire un commentaire shell `# ...` en fin de ligne, en respectant les
    guillemets ('{"x":"#notarealcomment"}' ne doit pas etre tronque)."""
    try:
        tokens_with_pos = list(shlex.shlex(line, posix=True, punctuation_chars=False))
    except ValueError:
        return line
    # Reparse en gardant trace des positions pour ne couper qu'un VRAI '#'
    # hors guillemets : plus simple et robuste de re-scanner caractere par
    # caractere en suivant l'etat quote.
    in_single = in_double = False
    for i, ch in enumerate(line):
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif ch == "#" and not in_single and not in_double:
            return line[:i].rstrip()
    return line


def extract_notifuse_lines(path):
    with open(path, "r", encoding="utf-8") as f:
        content = f.read()
    lines = []
    for block in BASH_BLOCK_RE.findall(content):
        for line in _join_continuations(block.splitlines()):
            line = strip_trailing_comment(line).strip()
            if not line or line.startswith("#"):
                continue
            if not line.startswith("notifuse "):
                continue
            lines.append((path, line))
    return lines


def tokenize(line):
    # shlex pour respecter les guillemets ('{"automation_id":"<id>"}').
    # Les placeholders <xxx> ne sont jamais substitues -- des tokens de
    # chaine valides pour argparse, qui ne juge que noms de commandes/flags.
    tokens = shlex.split(line)
    assert tokens[0] == "notifuse"
    return tokens[1:]


def main():
    for path in SKILL_FILES:
        if not os.path.isfile(path):
            print(f"NON ABOUTI : fichier introuvable : {path}", file=sys.stderr)
            return 2
    if not os.path.isfile(CLI_COMMON_PATH):
        print(f"NON ABOUTI : CLI embarque introuvable : {CLI_COMMON_PATH}", file=sys.stderr)
        return 2

    try:
        cli = load_cli_module()
        parser = cli.build_parser()
    except Exception as e:
        print(f"NON ABOUTI : impossible de charger/construire le parser CLI : {e}", file=sys.stderr)
        return 2

    all_lines = []
    for path in SKILL_FILES:
        all_lines.extend(extract_notifuse_lines(path))

    if not all_lines:
        print("NON ABOUTI : aucune ligne `notifuse ...` trouvee dans les blocs bash -- "
              "verifier que les fichiers n'ont pas change de format.", file=sys.stderr)
        return 2

    failures = []
    for path, line in all_lines:
        tokens = tokenize(line)
        captured = io.StringIO()
        try:
            with contextlib.redirect_stderr(captured):
                parser.parse_args(tokens)
        except SystemExit:
            err = captured.getvalue()
            # Tolerance UNIQUEMENT sur une erreur de COERCION DE TYPE (int/
            # float) touchant un placeholder <xxx> -- la commande et le nom
            # du flag sont corrects, seule la VALEUR litterale ne l'est pas
            # (attendu : ce sont des exemples avec des placeholders, pas de
            # vraies valeurs). Toute autre erreur (flag inconnu, flag
            # manquant, sous-commande inconnue) reste un echec reel.
            if re.search(r"invalid (int|float) value: '<[a-zA-Z0-9_]+>'", err):
                continue
            failures.append((path, line))
        except Exception as e:
            failures.append((path, f"{line}  [exception: {e}]"))

    print(f"{len(all_lines)} exemple(s) `notifuse ...` extrait(s) des skills distribues.")
    if failures:
        print(f"\n✗ {len(failures)} exemple(s) NE PARSENT PAS :", file=sys.stderr)
        for path, line in failures:
            print(f"  {os.path.relpath(path, REPO_ROOT)} : {line}", file=sys.stderr)
        return 1

    print("✓ Tous les exemples `notifuse ...` du skill distribue parsent avec le CLI embarque.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
