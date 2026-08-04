#!/usr/bin/env python3
"""Bloque le retour des réservations staging surdimensionnées."""

from pathlib import Path
import re

JOB = Path("deploy/notifuse-staging.nomad.hcl")
EXPECTED = {"db": (250, 320, 2048), "notifuse": (300, 96, 512)}


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
for task, expected in EXPECTED.items():
    match = re.search(rf'task\s+"{re.escape(task)}"\s*\{{', text)
    assert match, f"task absente: {task}"
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
