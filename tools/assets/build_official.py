#!/usr/bin/env python3
"""Build the official Regicide deck sheet into a web card theme.

Source (both supplied by the operator, not committed):
  artifacts/reg-official.png       the official uncut sheet: a 9 x 6 grid of
                                   750 x 1050 cells, read left to right, top to
                                   bottom — 52 cards in suit order C, D, H, S
                                   (A, 2..10, J, Q, K each), then the two Jesters.
  artifacts/reg-official-back.png  the matching card back (581 x 813).

Output into src/regicide/server/web/img/:
  official/<cardid>.webp   52 faces + j1, j2 (400 x 560, the game's face size)
  official/back.webp       the deck's own back; build_theme_registry.py lists it as
                           the theme's default back and as a selectable back

The slice is a straight grid cut: the sheet has no cut marks, and 9 x 750 = 6750
and 6 x 1050 = 6300 exactly, so each cell is one card as printed. Run order:
this builder, then build_joker_set.py (it takes img/official/j1.webp as the
theme's own Jester face), then build_theme_registry.py.

Usage:
  python3 tools/assets/build_official.py
  python3 tools/assets/build_official.py --check
"""
from __future__ import annotations

import argparse
import pathlib
import sys

from PIL import Image

ROOT = pathlib.Path(__file__).resolve().parents[2]
ART = ROOT / "artifacts"
SHEET = ART / "reg-official.png"
BACK = ART / "reg-official-back.png"
OUT = ROOT / "src/regicide/server/web/img/official"

COLS, ROWS = 9, 6
CELL_W, CELL_H = 750, 1050
FACE_WIDTH = 400      # the same face width the other decks ship (2x-plus a hand card)
QUALITY = 80

SUIT_ORDER = "CDHS"
RANKS = ["A"] + [str(n) for n in range(2, 11)] + ["J", "Q", "K"]
# Two Jesters close the sheet: a theme folder needs its own j1/j2 so the deck is
# complete on its own, even though the board draws a Jester through img/jokers/.
JESTERS = ["J1", "J2"]


def card_ids() -> list[str]:
    return [f"{rank}{suit}" for suit in SUIT_ORDER for rank in RANKS] + JESTERS


def build(check: bool) -> int:
    ids = card_ids()
    problems: list[str] = []

    if check:
        missing = [cid for cid in ids if not (OUT / f"{cid.lower()}.webp").exists()]
        if not (OUT / "back.webp").exists():
            problems.append("back.webp")
        if missing:
            problems.append(f"{len(missing)} missing face(s): {missing[:4]}")
        for path in sorted(OUT.glob("*.webp")):
            try:
                with Image.open(path) as im:
                    if im.width != FACE_WIDTH:
                        problems.append(f"{path.name} is {im.width}px wide, want {FACE_WIDTH}")
                        break
            except OSError as err:
                problems.append(f"{path.name}: {err}")
                break
        if problems:
            print("official theme: " + ", ".join(problems), file=sys.stderr)
            return 1
        total = sum(p.stat().st_size for p in OUT.glob("*.webp"))
        print(f"official theme: {len(ids)} faces + back present ({total / 1024 / 1024:.2f} MB)")
        return 0

    for src in (SHEET, BACK):
        if not src.exists():
            print(f"missing {src}", file=sys.stderr)
            return 1

    OUT.mkdir(parents=True, exist_ok=True)
    saved = 0
    with Image.open(SHEET) as sheet:
        sheet = sheet.convert("RGBA")
        if sheet.width != COLS * CELL_W or sheet.height != ROWS * CELL_H:
            print(
                f"{SHEET.name} is {sheet.width}x{sheet.height}; the 9 x 6 grid of "
                f"{CELL_W}x{CELL_H} needs {COLS * CELL_W}x{ROWS * CELL_H}",
                file=sys.stderr,
            )
            return 1
        for i, cid in enumerate(ids):
            col, row = i % COLS, i // COLS
            cell = sheet.crop((col * CELL_W, row * CELL_H, (col + 1) * CELL_W, (row + 1) * CELL_H))
            height = round(CELL_H * FACE_WIDTH / CELL_W)
            cell = cell.resize((FACE_WIDTH, height), Image.LANCZOS)
            dest = OUT / f"{cid.lower()}.webp"
            cell.save(dest, "WEBP", quality=QUALITY, method=6)
            saved += 1
    print(f"official: {saved} faces ({FACE_WIDTH}px)")

    with Image.open(BACK) as back:
        back = back.convert("RGBA")
        height = round(back.height * FACE_WIDTH / back.width)
        back = back.resize((FACE_WIDTH, height), Image.LANCZOS)
        back.save(OUT / "back.webp", "WEBP", quality=QUALITY, method=6)
    print("official: back.webp")
    print("next: python3 tools/assets/build_joker_set.py && python3 tools/assets/build_theme_registry.py")
    return 0


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="verify the theme on disk, build nothing")
    sys.exit(build(ap.parse_args().check))
