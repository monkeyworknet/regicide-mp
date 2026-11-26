#!/usr/bin/env python3
"""Extract the RageIced print-and-play sheets into individual card images.

Why pdfimages and not cropping: every card on every sheet is a separate raster
XObject at 750x1050 px (2.5x3.5 in @ 300 dpi), so `pdfimages -png` gives us the
cards exactly as authored -- no grid guessing, no cut marks, no bleed maths.

Outputs (all under --out):
  faces/<sha1>.png       unique images, deduplicated by content hash
  inventory.json         {hash: {w,h,count,pages[],indices[]}}
  contact-<n>.png        labelled contact sheets for hand-verification
  report.txt             counts and sanity checks

The mapping from hash to card id (rank+suit) is authored by hand into
mapping.json -- see docs/PLAN.md Appendix C. This script only reports.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
import sys
from pathlib import Path

from PIL import Image, ImageDraw

PDF_GLOB = "Pokemon_RageIced_*_of_4.pdf"
EXPECTED_FACES = 54
EXPECTED_BACK_COPIES = 54


def run(cmd: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, capture_output=True, text=True, check=True)


def extract(pdf: Path, dest: Path) -> list[Path]:
    dest.mkdir(parents=True, exist_ok=True)
    run(["pdfimages", "-png", str(pdf), str(dest / "img")])
    return sorted(dest.glob("img-*.png"))


def content_hash(path: Path) -> str:
    with Image.open(path) as im:
        small = im.convert("RGB").resize((128, 179))
        return hashlib.sha1(small.tobytes()).hexdigest()[:16]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--artifacts", default="artifacts", help="directory holding the source PDFs")
    ap.add_argument("--out", default="build/pokemon", help="working directory for extracted images")
    ap.add_argument("--keep-raw", action="store_true", help="keep the per-pdf intermediate PNGs")
    args = ap.parse_args()

    root = Path(args.artifacts)
    pdfs = sorted(root.glob(PDF_GLOB))
    if not pdfs:
        print(f"no {PDF_GLOB} under {root}", file=sys.stderr)
        return 1

    out = Path(args.out)
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)

    raw = out / "_raw"
    inventory: dict[str, dict] = {}
    order: list[str] = []

    for pdf in pdfs:
        images = extract(pdf, raw / pdf.stem)
        order.append(pdf.stem)
        for idx, img in enumerate(images):
            h = content_hash(img)
            with Image.open(img) as im:
                size = im.size
            entry = inventory.setdefault(h, {"w": size[0], "h": size[1], "count": 0, "pages": []})
            entry["count"] += 1
            entry["pages"].append({"pdf": pdf.stem, "index": idx, "file": img.name})

    faces_dir = out / "faces"
    faces_dir.mkdir()
    for h, entry in inventory.items():
        src = raw / entry["pages"][0]["pdf"] / entry["pages"][0]["file"]
        shutil.copy2(src, faces_dir / f"{h}.png")

    # Contact sheets: 12 unique images per sheet, labelled with hash + index.
    unique = list(inventory.items())
    per_sheet = 12
    for sheet_no in range(0, len(unique), per_sheet):
        chunk = unique[sheet_no : sheet_no + per_sheet]
        cols, thumb_w, thumb_h = 4, 180, 252
        rows = (len(chunk) + cols - 1) // cols
        sheet = Image.new("RGB", (cols * (thumb_w + 8) + 8, rows * (thumb_h + 26) + 8), "white")
        draw = ImageDraw.Draw(sheet)
        for i, (h, entry) in enumerate(chunk):
            r, c = divmod(i, cols)
            x, y = 8 + c * (thumb_w + 8), 8 + r * (thumb_h + 26)
            with Image.open(faces_dir / f"{h}.png") as im:
                sheet.paste(im.convert("RGB").resize((thumb_w, thumb_h)), (x, y))
            draw.text((x, y + thumb_h + 4), f"{sheet_no + i}: {h[:8]} x{entry['count']}", fill="black")
        sheet.save(out / f"contact-{sheet_no // per_sheet + 1}.png")

    (out / "inventory.json").write_text(json.dumps(inventory, indent=1, sort_keys=True))

    multi = {h: e for h, e in inventory.items() if e["count"] > 1}
    lines = [
        f"pdfs: {', '.join(order)}",
        f"images extracted: {sum(e['count'] for e in inventory.values())}",
        f"unique images: {len(inventory)}",
        f"repeated images: {len(multi)} -> " + ", ".join(f"{h}x{e['count']}" for h, e in multi.items())[:200],
        f"unique singletons: {len(inventory) - len(multi)}",
        "",
        "Expectation check:",
        f"  card back copies: {max((e['count'] for e in multi.values()), default=0)} "
        f"(expected {EXPECTED_BACK_COPIES})",
        f"  unique faces    : {len(inventory) - len(multi)} "
        f"(expected {EXPECTED_FACES} + info/tracker/extra pages)",
        "",
        "Card ids (rank+suit) are assigned by hand in mapping.json; see docs/PLAN.md Appendix C.",
    ]
    (out / "report.txt").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))

    if not args.keep_raw:
        shutil.rmtree(raw)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
