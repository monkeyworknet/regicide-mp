#!/usr/bin/env python3
"""Slice the 16-joker reference sheet into individual cards.

The sheet (artifacts/joker-sheet.jpg) is a 4 x 4 contact sheet of historical joker
designs. It is not game art — the designs belong to their own decks — so this tool
only measures how cleanly such a sheet separates: it finds the gutters, cuts each
cell, trims the light margin, and writes the crops plus a contact sheet for review.

Run:  python3 tools/assets/slice_joker_sheet.py --src artifacts/joker-sheet.jpg --out artifacts/joker-sheet
"""
from __future__ import annotations

import argparse
import pathlib

from PIL import Image, ImageChops

ROWS = 4
COLS = 4


def gutter_lines(img: Image.Image, axis: str, count: int) -> list[tuple[int, int]]:
    """Return `count` equal bands split at the lightest (most uniform) lines."""
    grey = img.convert("L")
    w, h = grey.size
    span = w if axis == "x" else h
    step = 1
    profile = []
    for i in range(0, span, step):
        if axis == "x":
            band = grey.crop((i, 0, min(i + step, w), h))
        else:
            band = grey.crop((0, i, w, min(i + step, h)))
        stat = ImageChops.difference(band, band.point(lambda v: v))  # placeholder, cheap
        del stat
        pixels = list(band.getdata())
        mean = sum(pixels) / len(pixels)
        # Uniform gutters are bright and have no peaks; a card has ink.
        profile.append((mean, i))
    # Split into `count` bands of near-equal length, then snap each boundary to the
    # brightest row/column within 6% of the nominal position.
    nominal = [round(span * k / count) for k in range(count + 1)]
    cuts = [0]
    for k in range(1, count):
        target = nominal[k]
        window = int(span * 0.06)
        best = max(
            (mean, pos)
            for mean, pos in profile
            if abs(pos - target) <= window
        ) if any(abs(pos - target) <= window for _, pos in profile) else (0, target)
        cuts.append(best[1])
    cuts.append(span)
    return [(cuts[k], cuts[k + 1]) for k in range(count)]


def trim(img: Image.Image, threshold: int = 245) -> Image.Image:
    """Drop a light, border-coloured margin around a card."""
    grey = img.convert("L")
    mask = grey.point(lambda v: 255 if v < threshold else 0)
    box = mask.getbbox()
    return img.crop(box) if box else img


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", default="artifacts/joker-sheet.jpg")
    ap.add_argument("--out", default="artifacts/joker-sheet")
    args = ap.parse_args()

    src = pathlib.Path(args.src)
    out = pathlib.Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    sheet = Image.open(src).convert("RGB")
    cols = gutter_lines(sheet, "x", COLS)
    rows = gutter_lines(sheet, "y", ROWS)
    print(f"sheet {sheet.size}, columns {cols}, rows {rows}")

    crops = []
    for r, (y0, y1) in enumerate(rows):
        for c, (x0, x1) in enumerate(cols):
            cell = sheet.crop((x0 + 3, y0 + 3, x1 - 3, y1 - 3))
            card = trim(cell)
            name = f"joker-sheet-{r * COLS + c + 1:02d}.png"
            card.save(out / name)
            crops.append(card)
            print(f"  {name}: {card.size} (cell {cell.size})")

    # Contact sheet, so a human can judge the cuts at a glance.
    tw = max(c.width for c in crops)
    th = max(c.height for c in crops)
    pad = 12
    board = Image.new("RGB", (COLS * (tw + pad) + pad, ROWS * (th + pad) + pad), (24, 32, 30))
    for i, card in enumerate(crops):
        r, c = divmod(i, COLS)
        board.paste(card, (pad + c * (tw + pad), pad + r * (th + pad)))
    contact = out / "contact.png"
    board.save(contact)
    print(f"{len(crops)} cards -> {out}/, contact sheet {contact} {board.size}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
