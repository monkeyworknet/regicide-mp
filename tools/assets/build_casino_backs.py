#!/usr/bin/env python3
"""Build the two casino card backs from artifacts/casino-assets.zip.

The casino faces were dropped when the theme list shrank to modern/simple/pokemon/8bit;
its two lattice backs stay available to every theme."""
from __future__ import annotations

import argparse
import pathlib
import sys
import zipfile

from PIL import Image

ROOT = pathlib.Path(__file__).resolve().parents[2]
ZIP = ROOT / "artifacts/casino-assets.zip"
WORK = ROOT / "build/casino"
OUT = ROOT / "src/regicide/server/web/img"

FACE_SIZE = (500, 700)
DIE_SIZE = (256, 256)
QUALITY = 88

RANKS = {
    "Ace": "A", "Two": "2", "Three": "3", "Four": "4", "Five": "5",
    "Six": "6", "Seven": "7", "Eight": "8", "Nine": "9", "Ten": "10",
    "Jack": "J", "Queen": "Q", "King": "K",
}
SUITS = {"Clubs": "C", "Diamonds": "D", "Hearts": "H", "Spades": "S"}

FACE_IDS = [f"{r}{s}" for s in "CDHS" for r in
            ["A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"]] + ["J1", "J2"]


def unzip() -> pathlib.Path:
    if not ZIP.exists():
        raise SystemExit(f"missing {ZIP}")
    if not WORK.exists():
        WORK.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(ZIP) as zf:
            zf.extractall(WORK)
    return WORK


def process(src: pathlib.Path, dest: pathlib.Path, size: tuple[int, int]) -> int:
    dest.parent.mkdir(parents=True, exist_ok=True)
    with Image.open(src) as im:
        im = im.convert("RGBA")
        bbox = im.getbbox()
        if bbox:
            im = im.crop(bbox)
        im = im.resize(size, Image.LANCZOS)
        im.save(dest, "WEBP", quality=QUALITY, method=6)
    return dest.stat().st_size


def build(check: bool) -> int:
    if check:
        missing = []
        for name in ("casino-red.webp", "casino-blue.webp"):
            if not (OUT / "backs" / name).exists():
                missing.append(f"backs/{name}")
        if missing:
            print("missing casino assets: " + ", ".join(missing), file=sys.stderr)
            return 1
        back_count = sum(1 for name in ("casino-red.webp", "casino-blue.webp") if (OUT / "backs" / name).exists())
        total = sum((OUT / "backs" / n).stat().st_size for n in ("casino-red.webp", "casino-blue.webp"))
        print(f"casino backs: {back_count} present ({total/1024/1024:.2f} MB)")
        return 0

    unzip()
    total = 0
    backs = 0
    for src_name, out_name in (("CardBackRed.png", "casino-red.webp"), ("CardBackBlue.png", "casino-blue.webp")):
        src = WORK / src_name
        if not src.exists():
            print(f"ERROR: source missing {src}", file=sys.stderr)
            return 1
        total += process(src, OUT / "backs" / out_name, FACE_SIZE)
        backs += 1

    # The pack also ships six dice, but the counter style draws the count on a
    # poker chip (see build_gfx_themes.py), so they are not converted: the shared
    # furniture/ dice set stays the one from Card_Game_GFX.
    print(f"casino backs: {backs} ({total/1024/1024:.2f} MB)")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true")
    args = ap.parse_args()
    if args.check:
        return build(True)
    return build(False)


if __name__ == "__main__":
    raise SystemExit(main())
