#!/usr/bin/env python3
"""Build the Pokémon theme assets from the print-and-play sheets.

Reads the hand-verified mapping in tools/assets/pokemon_mapping.json, resolves it
against the extracted faces (via the committed inventory) and writes one WebP per
card id plus a card back into the embedded web tree.

The build is intentionally strict: if the extraction changes (different hashes or
counts), it fails and asks for the mapping to be re-verified rather than shipping
the wrong art on the wrong card.

Usage: python3 tools/assets/build_pokemon.py [--check]
"""
from __future__ import annotations

import argparse
import json
import pathlib
import shutil
import subprocess
import sys

from PIL import Image

ROOT = pathlib.Path(__file__).resolve().parents[2]
MAPPING = ROOT / "tools/assets/pokemon_mapping.json"
INVENTORY = ROOT / "tools/assets/pokemon_inventory.json"
WORKDIR = ROOT / "build/pokemon"
OUT = ROOT / "src/regicide/server/web/img/pokemon"
SIZE = 480
BACK_W, BACK_H = 500, 700
QUALITY = 78
BACK_HASH_MIN_COPIES = 40


def ensure_extraction() -> dict:
    """Extract the sheets if needed and return the inventory."""
    if not INVENTORY.exists():
        subprocess.run([sys.executable, str(ROOT / "tools/assets/extract_pokemon.py")], check=True)
        shutil.copy2(WORKDIR / "inventory.json", INVENTORY)
    return json.loads(INVENTORY.read_text())


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="verify outputs exist without rewriting")
    args = ap.parse_args()

    mapping = json.loads(MAPPING.read_text())

    if args.check:
        # A checkout without artifacts/ still has the committed WebP outputs, so
        # verification must not require re-extraction from the source PDFs.
        cards = mapping["cards"]
        missing = [c["id"] for c in cards.values() if not (OUT / f"{c['id'].lower()}.webp").exists()]
        if not (OUT / "back.webp").exists():
            missing.append("back")
        if missing:
            print("missing pokemon assets: " + ", ".join(sorted(missing)), file=sys.stderr)
            return 1
        total = sum(p.stat().st_size for p in OUT.glob("*.webp"))
        print(f"pokemon assets: {len(cards)} faces + back present ({total/1024/1024:.2f} MB)")
        return 0

    inventory = ensure_extraction()
    singles = sorted(h for h, e in inventory.items() if e["count"] == 1)
    repeated = {h: e["count"] for h, e in inventory.items() if e["count"] > 1}

    if len(singles) != 58:
        print(f"ERROR: expected 58 unique singletons, got {len(singles)}. Re-verify the mapping.", file=sys.stderr)
        return 1

    back_hash = max(repeated, key=repeated.get)
    if repeated[back_hash] < BACK_HASH_MIN_COPIES:
        print(f"ERROR: the repeated card back appears only {repeated[back_hash]} times.", file=sys.stderr)
        return 1

    cards = mapping["cards"]
    if len(cards) != 54:
        print(f"ERROR: mapping covers {len(cards)} cards, expected 54.", file=sys.stderr)
        return 1

    faces_dir = WORKDIR / "faces"
    if not faces_dir.exists():
        print("ERROR: extracted faces missing; run tools/assets/extract_pokemon.py", file=sys.stderr)
        return 1

    OUT.mkdir(parents=True, exist_ok=True)

    manifest = {"version": 1, "size": SIZE, "back": "back.webp", "cards": {}}
    total_bytes = 0
    for idx, entry in sorted(cards.items(), key=lambda kv: int(kv[0])):
        h = singles[int(idx)]
        src = faces_dir / f"{h}.png"
        if not src.exists():
            print(f"ERROR: {src} missing", file=sys.stderr)
            return 1
        with Image.open(src) as im:
            im = im.convert("RGB")
            im.thumbnail((SIZE, SIZE * 2), Image.LANCZOS)
            target = OUT / f"{entry['id'].lower()}.webp"
            im.save(target, "WEBP", quality=QUALITY, method=6)
        total_bytes += target.stat().st_size
        manifest["cards"][entry["id"]] = {"file": target.name, "name": entry["name"]}

    # The card back is taken from page 2 of the first sheet, which prints a full
    # page of backs at 300 dpi (750x1050 per back). That is crisper than the copy
    # that happens to appear among the face sheets.
    back_src = None
    page2 = WORKDIR / "page2-back.png"
    if not page2.exists():
        pdf = ROOT / "artifacts/Pokemon_RageIced_20230425_1_of_4.pdf"
        if pdf.exists():
            tmp = WORKDIR / "_page2"
            tmp.mkdir(parents=True, exist_ok=True)
            subprocess.run(["pdfimages", "-f", "2", "-l", "2", "-png", str(pdf), str(tmp / "b")], check=False)
            first = sorted(tmp.glob("b-*.png"))
            if first:
                shutil.copy2(first[0], page2)
    if page2.exists():
        back_src = page2
    if back_src is None:
        back_src = faces_dir / f"{back_hash}.png"
    with Image.open(back_src) as im:
        im = im.convert("RGB")
        im.thumbnail((BACK_W, BACK_H), Image.LANCZOS)
        im.save(OUT / "back.webp", "WEBP", quality=QUALITY, method=6)
    total_bytes += (OUT / "back.webp").stat().st_size

    (OUT / "manifest.json").write_text(json.dumps(manifest, indent=1) + "\n")
    print(f"wrote {len(cards)} faces + back to {OUT} ({total_bytes/1024/1024:.2f} MB)")
    if total_bytes > 2_400_000:
        print("WARNING: asset budget exceeded (2.4 MB)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
