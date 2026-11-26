#!/usr/bin/env python3
"""Generate web/data/themes.json — the list of card sets and card backs the UI offers.

The registry is derived from what is actually on disk, so it cannot drift from the
art: a theme listed here always has 54 faces, a back always has a file. Run with
--check to validate (used by the gate) without rewriting.
"""
from __future__ import annotations

import argparse
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
WEB = ROOT / "src/regicide/server/web"
IMG = WEB / "img"
OUT = WEB / "data/themes.json"

# id -> (label, hasText, pixelated). hasText means the card face has a blank
# middle that this build fills with the card's rule text.
THEMES = {
    "official": ("Official", False, False),
    "simple": ("Simple", False, False),
    "8bit": ("Standard", False, True),
    "pokemon": ("Pokémon", False, False),
}
# The back that suits each set. Picking a set selects this back; the player can
# still override it afterwards (mix and match).
THEME_BACK = {
    "simple": "red",
    "pokemon": "pokemon",
    "8bit": "8bit",
}
DEFAULT_THEME = "official"

BACK_LABELS = {
    "black": "Black", "blue": "Blue", "green": "Green", "orange": "Orange",
    "purple": "Purple", "red": "Red",
    "casino-red": "Casino red", "casino-blue": "Casino blue", "8bit": "8bit",
}
DEFAULT_BACK = "black"

# A card set brings a Jester pair that suits it; the player can pick any two faces
# from the catalogue in img/jokers/ (built by tools/assets/build_joker_set.py).
# Simple and Standard have no fixed pair: the client picks two at random from the
# pool that excludes the set-specific faces.
JOKERS_DIR = IMG / "jokers"
THEME_JOKERS = {
    "official": ["official", "official"],
    "pokemon": ["ditto", "porygon"],
}
DEFAULT_JOKER = "jester"

CARD_IDS = [f"{r}{s}" for s in "CDHS" for r in list("A") + [str(n) for n in range(2, 11)] + ["J", "Q", "K"]]


def faces(theme: str) -> int:
    return len(list((IMG / theme).glob("*.webp")))


def build(check: bool) -> int:
    problems: list[str] = []
    themes = []
    for theme, (label, has_text, pixelated) in THEMES.items():
        count = faces(theme)
        have = {p.stem.upper() for p in (IMG / theme).glob("*.webp")}
        missing = [c for c in CARD_IDS if c not in have]
        if count < 54 or missing:
            problems.append(f"theme {theme}: {count}/54 faces, missing {missing[:4]}")
            continue
        entry = {"id": theme, "label": label, "hasText": has_text, "dir": f"/img/{theme}"}
        preferred = THEME_BACK.get(theme)
        if preferred and (IMG / "backs" / f"{preferred}.webp").exists():
            entry["defaultBack"] = preferred
        elif (IMG / theme / "back.webp").exists():
            entry["defaultBack"] = theme
        if pixelated:
            entry["pixelated"] = True
        themes.append(entry)

    backs = []
    for path in sorted((IMG / "backs").glob("*.webp")):
        back_id = path.stem
        backs.append({"id": back_id, "label": BACK_LABELS.get(back_id, back_id.title()), "path": f"/img/backs/{back_id}.webp"})
    # A card set may ship a matching back of its own (pokemon does).
    for theme in themes:
        own = IMG / theme["id"] / "back.webp"
        if own.exists():
            backs.append({"id": theme["id"], "label": f"{theme['label']} back", "path": f"{theme['dir']}/back.webp"})
    if not backs:
        problems.append("no card backs in img/backs")

    furniture = {p.stem: f"/img/furniture/{p.name}" for p in sorted((IMG / "furniture").glob("*.webp"))}

    # The joker catalogue: labels come from the builder's index so the menu, the
    # registry and the credits table cannot disagree.
    jokers = []
    index_path = JOKERS_DIR / "index.json"
    labels = {}
    order = []
    if index_path.exists():
        for j in json.loads(index_path.read_text()).get("jokers", []):
            labels[j["id"]] = j.get("label", j["id"].title())
            order.append(j["id"])
    on_disk = {p.stem: p for p in JOKERS_DIR.glob("*.webp")}
    # The builder's order is the design order: the shared default first, then the
    # faces each deck shipped, then the historical sheet. Anything on disk but not
    # in the index is appended alphabetically so nothing silently disappears.
    for jid in order + sorted(set(on_disk) - set(order)):
        if jid not in on_disk:
            problems.append(f"joker {jid} listed in index.json but missing on disk")
            continue
        jokers.append({"id": jid, "label": labels.get(jid, jid.replace("-", " ").title()), "path": f"/img/jokers/{on_disk[jid].name}"})
    if not jokers:
        problems.append("no jokers in img/jokers")
    joker_ids = {j["id"] for j in jokers}
    for jid in [DEFAULT_JOKER, *[j for pair in THEME_JOKERS.values() for j in pair]]:
        if jid not in joker_ids:
            problems.append(f"joker {jid} is missing from img/jokers")
    for theme in themes:
        pair = THEME_JOKERS.get(theme["id"])
        if pair:
            theme["defaultJokers"] = pair

    if problems:
        for p in problems:
            print(p, file=sys.stderr)
        return 1
    if check:
        if not OUT.exists():
            print("themes.json missing", file=sys.stderr)
            return 1
        current = json.loads(OUT.read_text())
        # Compare the whole generated document: the theme list alone would let a
        # stale defaultBack or a dropped back slip through.
        if (
            current.get("themes") != themes
            or [b["id"] for b in current.get("backs", [])] != [b["id"] for b in backs]
            or [j["id"] for j in current.get("jokers", [])] != [j["id"] for j in jokers]
        ):
            print("themes.json is stale: regenerate with tools/assets/build_theme_registry.py", file=sys.stderr)
            return 1
        print(f"themes.json ok ({len(themes)} themes, {len(backs)} backs, {len(jokers)} jokers)")
        return 0

    ids = {t["id"] for t in themes}
    data = {
        "version": 1,
        "default": DEFAULT_THEME if DEFAULT_THEME in ids else themes[0]["id"],
        "themes": themes,
        "defaultBack": DEFAULT_BACK if any(b["id"] == DEFAULT_BACK for b in backs) else backs[0]["id"],
        "backs": backs,
        "defaultJoker": DEFAULT_JOKER if DEFAULT_JOKER in joker_ids else jokers[0]["id"],
        "jokers": jokers,
        "furniture": furniture,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(data, indent=1, ensure_ascii=False) + "\n")
    print(f"themes.json: {len(themes)} themes ({', '.join(t['id'] for t in themes)}), {len(backs)} backs, {len(jokers)} jokers")
    return 0


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    raise SystemExit(build(ap.parse_args().check))
