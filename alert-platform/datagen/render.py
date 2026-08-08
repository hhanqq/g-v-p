"""Рендеринг сырого тела сообщения источника из шаблона + "грязь" формата.

Раздел 18.3, требование 4: часть сообщений намеренно неидеальна —
лишние переносы, обрезанные значения, задвоенные пробелы.
"""
from __future__ import annotations

import random


def render_body(template_text: str, context: dict, rnd: random.Random, dirty_share: float = 0.12) -> str:
    body = template_text.format(**context).rstrip("\n")
    if rnd.random() < dirty_share:
        body = _make_dirty(body, rnd)
    return body


def _make_dirty(body: str, rnd: random.Random) -> str:
    lines = body.split("\n")
    mutation = rnd.choice(["blank_line", "truncate", "double_space", "trailing_junk"])
    if mutation == "blank_line" and len(lines) > 1:
        idx = rnd.randint(1, len(lines) - 1)
        lines.insert(idx, "")
    elif mutation == "truncate" and lines:
        idx = rnd.randint(0, len(lines) - 1)
        cut = max(3, int(len(lines[idx]) * rnd.uniform(0.5, 0.85)))
        lines[idx] = lines[idx][:cut]
    elif mutation == "double_space":
        idx = rnd.randint(0, len(lines) - 1)
        lines[idx] = lines[idx].replace(": ", ":  ", 1)
    elif mutation == "trailing_junk":
        lines.append("--" if rnd.random() < 0.3 else "")
    return "\n".join(lines)
