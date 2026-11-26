#!/usr/bin/env python3
"""Build img/jokers/ — the catalogue of Jester faces the player can choose from.

Sources:
  * the official deck's Jester, the "Original" face;
  * the shared default face ("Jester");
  * the Pokémon pack's two Jesters (Ditto and Porygon);
  * eight historical designs from the 4 x 4 reference sheet, cropped by hand so
    the corner symbols and the JOKER/Jester lettering are never cut.

Every face is normalised to the game's card size (500 x 700), fitted (never
stretched) and padded with the card's own paper colour.

Run:  python3 tools/assets/build_joker_set.py
"""
from __future__ import annotations

import json
import pathlib
import sys

from PIL import Image, ImageOps

ROOT = pathlib.Path(__file__).resolve().parents[2]
WEB = ROOT / "src/regicide/server/web"
IMG = WEB / "img"
OUT = IMG / "jokers"
ART = ROOT / "artifacts"
W, H = 500, 700

# id -> (label, source path). The order is the Settings order: the set faces
# first, then the historical sheet.
JOKERS = [
    ("official", "Original", IMG / "official/j1.webp"),
    ("jester", "Jester", ART / "joker-default.jpg"),
    ("ditto", "Ditto", IMG / "pokemon/j1.webp"),
    ("porygon", "Porygon", IMG / "pokemon/j2.webp"),
]

# The eight faces kept from the reference sheet. The boxes are hand-measured on
# artifacts/joker-sheet.jpg (905 x 1280): each one contains the whole card, including
# its corner symbols and the printed word, with a little paper around it. `rotate` is
# the correction that makes the design read upright (the "the Joker" card was scanned
# upside down). The sheet's other eight cells were retired (see docs/DECISIONS.md).
# The sheet is a 4 x 4 grid of ~226 x 320 cells. Several of the original boxes
# crossed a cell boundary by 2-12 px, which dragged a sliver of the neighbouring
# card into the face (visible on Card Table, Puppet, Banner and Staff). The boxes
# now stop one pixel inside the cell edge. `shadow` and `ribbon` keep their full
# height: their own design runs to the card edge, and the old crop was right.
SHEET_FACES = [
    ("bower", "Bower", (6, 6, 225, 314), 0),
    ("crown", "Crown", (456, 6, 672, 316), 0),
    ("card-table", "Card Table", (679, 8, 890, 314), 0),
    ("banner", "Banner", (0, 321, 225, 648), 180),
    ("shadow", "Shadow", (453, 321, 670, 643), 0),
    ("staff", "Staff", (230, 642, 451, 943), 0),
    ("ribbon", "Ribbon", (463, 642, 661, 940), 0),
    ("puppet", "Puppet", (228, 961, 451, 1258), 0),
]


def paper_of(card: Image.Image) -> tuple[int, int, int]:
    """The card's own paper, sampled just inside its top-left corner."""
    patch = card.crop((3, 3, 15, 15)).resize((1, 1), Image.BOX)
    return patch.getpixel((0, 0))


def fit(card: Image.Image, pad: tuple[int, int, int] | None = None) -> Image.Image:
    """Fit a card into 500x700 with a small margin, never stretching it."""
    colour = pad if pad is not None else paper_of(card)
    return ImageOps.pad(card, (W, H), color=colour, method=Image.LANCZOS)


def whiten(card: Image.Image) -> Image.Image:
    """Turn the sheet's cream/grey paper into white, leaving the artwork alone.

    The scan's paper is not white (cream, grey, or yellowed), and the crop may include
    a little of the white gutter, so a corner sample is not reliable. The paper is the
    median of the light pixels instead; pixels close to it are blended to white with a
    soft edge, so anti-aliased line work keeps its shape while the paper goes clean.
    """
    import numpy as np

    arr = np.asarray(card.convert("RGB")).astype(np.float32)
    lum = arr.mean(axis=2)
    # `>=` matters: a card that is already white has its 65th percentile at 255, and
    # `>` would select nothing and make the median NaN (which rendered black).
    light = arr[lum >= np.percentile(lum, 50)]
    paper = np.median(light, axis=0) if len(light) else np.array([255.0, 255.0, 255.0])
    if not np.isfinite(paper).all():
        paper = np.array([255.0, 255.0, 255.0])
    dist = np.sqrt(((arr - paper) ** 2).sum(axis=2))
    alpha = np.clip((dist - 6.0) / 24.0, 0.0, 1.0)  # 0 on paper, 1 on ink
    white = np.full_like(arr, 255.0)
    out = arr * alpha[..., None] + white * (1.0 - alpha[..., None])
    # The scan shadows the outermost millimetres of a card; a light pixel in the
    # 3 px rim is paper, not ink, so send it to white too. Corner symbols are dark
    # and are left alone.
    out = np.clip(out, 0, 255)
    lum2 = out.mean(axis=2)
    for sl in (np.s_[0:3, :], np.s_[-3:, :], np.s_[:, 0:3], np.s_[:, -3:]):
        region = out[sl]
        region[lum2[sl] > 200] = 255.0
    return Image.fromarray(out.astype(np.uint8), "RGB")


def normalise(src: Image.Image) -> Image.Image:
    """Trim a deck-sourced card to its edge and fit it."""
    card = src.convert("RGB")
    grey = card.convert("L")
    box = grey.point(lambda v: 255 if v < 247 else 0).getbbox()
    if box:
        card = card.crop(box)
    return fit(card)


def sheet_face(name: str, box: tuple[int, int, int, int], rotate: int) -> Image.Image:
    src = ART / "joker-sheet.jpg"
    if not src.exists():
        raise FileNotFoundError(src)
    sheet = Image.open(src).convert("RGB")
    card = sheet.crop(box)
    if rotate:
        card = card.rotate(rotate, expand=True)
    return fit(whiten(card), (255, 255, 255))


def save(img: Image.Image, joker_id: str) -> pathlib.Path:
    out = OUT / f"{joker_id}.webp"
    img.save(out, "WEBP", quality=88, method=6)
    return out


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    # Retired faces: the build owns the directory, so an old file cannot reappear in
    # the registry (and in Settings) after its id is removed from the index.
    retired = {
        "letterpress", "house-clown", "filigree", "high-wheel", "minstrel",
        "vaudeville", "starlight", "harlequin", "bell", "tambourine", "ruff",
    }
    for jid in retired:
        (OUT / f"{jid}.webp").unlink(missing_ok=True)

    index = []
    for joker_id, label, src in JOKERS:
        if not src.exists():
            print(f"missing source for {joker_id}: {src}", file=sys.stderr)
            return 1
        path = save(normalise(Image.open(src)), joker_id)
        print(f"{joker_id:12s} {label:14s} <- {src.name} ({path.stat().st_size // 1024} KB)")
        index.append({"id": joker_id, "label": label, "source": src.name})

    for joker_id, label, box, rotate in SHEET_FACES:
        card = sheet_face(joker_id, box, rotate)
        path = save(card, joker_id)
        print(f"{joker_id:12s} {label:14s} <- joker-sheet.jpg {box} rot {rotate} ({path.stat().st_size // 1024} KB)")
        index.append({"id": joker_id, "label": label, "source": "joker-sheet.jpg"})

    (OUT / "index.json").write_text(json.dumps({"jokers": index}, indent=1, ensure_ascii=False) + "\n")
    print(f"{len(index)} joker faces -> {OUT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
