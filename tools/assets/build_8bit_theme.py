#!/usr/bin/env python3
"""Build the '8bit' card theme (operator rename of the Jorel deck).

Source (96x128 RGBA PNGs, one per card):
  Clubs/club_000N.png, Diamonds/diamond_000N.png, Hearts/heart_000N.png,
  Spades/spade_000N.png   -> 52 faces
  card_back.png           -> back (500x700) and the two joker faces (no joker art in pack)

Outputs into src/regicide/server/web/img/:
  8bit/<cardid>.webp   (54 files: 52 + j1 + j2)
  backs/8bit.webp

Usage:
  python3 tools/assets/build_8bit_theme.py
  python3 tools/assets/build_8bit_theme.py --check
"""
from __future__ import annotations

import argparse
import pathlib
import sys
import zipfile

from PIL import Image

ROOT = pathlib.Path(__file__).resolve().parents[2]
ZIP = ROOT / "artifacts/Jorel's Card Pack.zip"
WORK = ROOT / "build/8bit"
OUT_FACES = ROOT / "src/regicide/server/web/img/8bit"
OUT_BACKS = ROOT / "src/regicide/server/web/img/backs"

RANKS = ["A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"]
SUITS = {"Clubs": ("C", "club"), "Diamonds": ("D", "diamond"), "Hearts": ("H", "heart"), "Spades": ("S", "spade")}

FACE_WIDTH = 300
FACE_HEIGHT = 400
BACK_WIDTH = 500
BACK_HEIGHT = 700
QUALITY = 88

EXPECTED_IDS = [f"{r}{s}" for s in ("C", "D", "H", "S") for r in RANKS] + ["J1", "J2"]


def unzip() -> pathlib.Path:
    if not ZIP.exists():
        raise SystemExit(f"missing {ZIP}")
    if not WORK.exists():
        WORK.mkdir(parents=True)
        with zipfile.ZipFile(ZIP) as zf:
            zf.extractall(WORK)
    return WORK


def convert(src: pathlib.Path, dest: pathlib.Path, width: int, height: int) -> int:
    dest.parent.mkdir(parents=True, exist_ok=True)
    with Image.open(src) as im:
        im = im.convert("RGBA")
        im = im.resize((width, height), Image.LANCZOS)
        im.save(dest, "WEBP", quality=QUALITY, method=6)
    return dest.stat().st_size


def jester_from_back(src: pathlib.Path, dest: pathlib.Path) -> int:
    """Draw a jester card: the pack's back art with a gold seal and JESTER banner."""
    from PIL import ImageDraw, ImageFont

    im = Image.open(src).convert("RGBA").resize((FACE_WIDTH, FACE_HEIGHT), Image.LANCZOS)
    draw = ImageDraw.Draw(im, "RGBA")
    cx, cy = FACE_WIDTH // 2, FACE_HEIGHT // 2
    r = FACE_WIDTH // 3
    draw.ellipse((cx - r, cy - r - 10, cx + r, cy + r - 10), fill=(250, 244, 226, 245),
                 outline=(150, 32, 36, 255), width=5)
    draw.ellipse((cx - r + 12, cy - r + 2, cx + r - 12, cy + r - 22), outline=(150, 32, 36, 160), width=2)
    font = ImageFont.load_default(size=max(16, FACE_WIDTH // 8))
    text = "JESTER"
    box = draw.textbbox((0, 0), text, font=font)
    draw.text((cx - (box[2] - box[0]) // 2, cy - 14 - (box[3] - box[1]) // 2), text,
              font=font, fill=(150, 32, 36, 255))
    small = ImageFont.load_default(size=max(11, FACE_WIDTH // 15))
    sub = "wild"
    box = draw.textbbox((0, 0), sub, font=small)
    draw.text((cx - (box[2] - box[0]) // 2, cy + r // 2), sub, font=small, fill=(90, 70, 40, 220))
    dest.parent.mkdir(parents=True, exist_ok=True)
    im.save(dest, "WEBP", quality=QUALITY, method=6)
    return dest.stat().st_size


def build(check: bool) -> int:
    if check:
        missing = []
        for cid in EXPECTED_IDS:
            path = OUT_FACES / f"{cid.lower()}.webp"
            if not path.exists() or path.stat().st_size == 0:
                missing.append(f"8bit/{cid.lower()}.webp")
        back = OUT_BACKS / "8bit.webp"
        if not back.exists() or back.stat().st_size == 0:
            missing.append("backs/8bit.webp")
        if missing:
            print("missing 8bit assets: " + ", ".join(missing), file=sys.stderr)
            return 1
        total = sum(p.stat().st_size for p in OUT_FACES.glob("*.webp")) + back.stat().st_size
        print(f"jorel assets: {len(EXPECTED_IDS)} faces + 1 back present ({total/1024/1024:.2f} MB)")
        return 0

    work = unzip()
    total = 0

    # 52 suited faces.
    for folder, (letter, prefix) in SUITS.items():
        for n, rank in enumerate(RANKS, start=1):
            src = work / folder / f"{prefix}_{n:04d}.png"
            if not src.exists():
                print(f"missing source: {src}", file=sys.stderr)
                return 1
            cid = f"{rank}{letter}"
            total += convert(src, OUT_FACES / f"{cid.lower()}.webp", FACE_WIDTH, FACE_HEIGHT)
    print("8bit: 52 suited faces")

    # Joker art: none shipped in this pack (spritesheet is exactly 13x4 = 52 tiles).
    joker_sources = sorted(
        p for p in work.rglob("*.png") if "joker" in p.name.lower()
    )
    if len(joker_sources) >= 2:
        for cid, src in zip(("J1", "J2"), joker_sources[:2]):
            total += convert(src, OUT_FACES / f"{cid.lower()}.webp", FACE_WIDTH, FACE_HEIGHT)
        print(f"8bit: 2 joker faces from {joker_sources[0].name}, {joker_sources[1].name}")
    else:
        back_src = work / "card_back.png"
        if not back_src.exists():
            print("missing card_back.png for joker fallback", file=sys.stderr)
            return 1
        # No joker art in the pack. Reuse the back art, but stamp a jester seal on
        # it so the jester tray never looks like a plain card back.
        for cid in ("J1", "J2"):
            total += jester_from_back(back_src, OUT_FACES / f"{cid.lower()}.webp")
        print("8bit: no joker art in pack; generated J1 and J2 from card_back.png + jester seal")

    # Back.
    back_src = work / "card_back.png"
    if not back_src.exists():
        print("missing card_back.png", file=sys.stderr)
        return 1
    total += convert(back_src, OUT_BACKS / "8bit.webp", BACK_WIDTH, BACK_HEIGHT)
    print("8bit: 1 back")

    print(f"wrote jorel theme assets ({total/1024/1024:.2f} MB)")
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
