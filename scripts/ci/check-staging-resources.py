#!/usr/bin/env python3
"""Bloque le retour des réservations staging surdimensionnées."""

from pathlib import Path
import re

JOB = Path("deploy/notifuse-staging.nomad.hcl")
# Un nom de tache ne se code JAMAIS en dur : Nomad nomme le conteneur d'apres la
# tache, donc les noms bougent quand on les rend auto-porteurs. Le meme controle
# a casse le deploiement staging de `hub` le 2026-09-07 avec « task absente:
# pgproxy », bloquant tout push sur sa branche de deploiement.
#
# On accepte donc les DEUX noms, le nouveau d'abord et l'ancien en repli : le
# controle traverse le renommage sans fenetre rouge, et un futur renommage n'a
# qu'a ajouter un alias.
EXPECTED = {
    ("notifuse-staging-db", "db"): (250, 320, 2048),
    ("notifuse-staging", "notifuse"): (300, 96, 512),
}


def block(text: str, start: int) -> str:
    opening = text.index("{", start)
    depth = 0
    for pos in range(opening, len(text)):
        if text[pos] == "{":
            depth += 1
        elif text[pos] == "}":
            depth -= 1
            if depth == 0:
                return text[opening + 1 : pos]
    raise AssertionError("bloc HCL non fermé")


text = JOB.read_text(encoding="utf-8")
assert "memory_max = 7000" not in text, "memory_max=7000 interdit en staging"
DECLAREES = re.compile(r'^[ \t]*task\s+"([^"]+)"', re.MULTILINE)

for noms, expected in EXPECTED.items():
    match = None
    for task in noms:
        match = re.search(rf'task\s+"{re.escape(task)}"\s*\{{', text)
        if match:
            break
    # Aucun des noms acceptes : c'est un vrai defaut, pas un renommage. Le
    # message nomme les candidats ET les taches reellement declarees.
    declarees = DECLAREES.findall(text)
    assert match, f"task absente sous aucun de ses noms {noms} - declarees : {declarees}"
    task_block = block(text, match.start())
    resources = re.search(r"resources\s*\{", task_block)
    assert resources, f"resources absent: {task}"
    resource_block = block(task_block, resources.start())
    actual = tuple(
        int(re.search(rf"\b{name}\s*=\s*(\d+)", resource_block).group(1))
        for name in ("cpu", "memory", "memory_max")
    )
    assert actual == expected, f"{task}: {actual} != {expected}"
    assert actual[2] <= 3072, f"fusible excessif: {task}"

print("OK staging resources notifuse")
