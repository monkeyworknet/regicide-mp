#!/usr/bin/env python3
"""Verify the embedded UI data files that the browser needs at runtime.

This exists because a `.gitignore` pattern once excluded `web/data/` from git,
so the files existed in a worktree but not in a fresh checkout: the UI silently
degraded to fallback card labels and silence. Fail loudly instead.
"""
from __future__ import annotations

import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
WEB = ROOT / "src/regicide/server/web"


def main() -> int:
    problems: list[str] = []

    cardtext = WEB / "data/cardtext.json"
    if not cardtext.exists():
        problems.append("missing data/cardtext.json")
    else:
        cards = json.loads(cardtext.read_text()).get("cards", {})
        if len(cards) != 54:
            problems.append(f"cardtext.json has {len(cards)} cards, want 54")
        for cid, entry in cards.items():
            for field in ("title", "value", "power", "note"):
                if not entry.get(field):
                    problems.append(f"cardtext.json {cid} missing {field}")

    audio = WEB / "data/audio.json"
    if not audio.exists():
        problems.append("missing data/audio.json")
    else:
        manifest = json.loads(audio.read_text()).get("files", {})
        required = {"play", "draw", "damage", "defeat", "enemy_attack", "victory"}
        missing_events = required - set(manifest)
        if missing_events:
            problems.append("audio.json missing events: " + ", ".join(sorted(missing_events)))
        for event, files in manifest.items():
            for name in files:
                if not (WEB / "sfx" / name).exists():
                    problems.append(f"audio.json {event} points at missing sfx/{name}")

    for required in ("index.html", "app.js", "style.css", "cards.js", "audio.js",
                     "img/pokemon/back.webp", "img/pokemon/manifest.json"):
        if not (WEB / required).exists():
            problems.append(f"missing {required}")

    if problems:
        for p in problems:
            print("FAIL: " + p, file=sys.stderr)
        return 1
    print("web data: cardtext 54/54, audio manifest complete, required assets present")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
