#!/usr/bin/env python3
"""Generate web/data/cardtext.json — the middle text of every modern card.

Content is derived from docs/research/01-rules-dossier.md (§1.2 values, §3 suit
powers), never copied from the printed art. One entry per card id, 54 total.
"""
import json
import pathlib

SUITS = {"C": ("Clubs", "♣"), "D": ("Diamonds", "♦"), "H": ("Hearts", "♥"), "S": ("Spades", "♠")}
POWERS = {
    "C": "Damage from this play is doubled.",
    "D": "Draw cards equal to this play's total.",
    "H": "Shuffle the discard pile, then put that many cards under the tavern deck.",
    "S": "Reduce this enemy's attack by this play's total.",
}
RANKS = {
    "A": ("Animal Companion", 1),
    "2": ("Two", 2), "3": ("Three", 3), "4": ("Four", 4), "5": ("Five", 5),
    "6": ("Six", 6), "7": ("Seven", 7), "8": ("Eight", 8), "9": ("Nine", 9), "10": ("Ten", 10),
    "J": ("Jack", 10), "Q": ("Queen", 15), "K": ("King", 20),
}


def entry(rank_key: str, suit_key: str) -> dict:
    rank_name, value = RANKS[rank_key]
    suit_name, symbol = SUITS[suit_key]
    royal = rank_key in ("J", "Q", "K")
    if rank_key == "A":
        title = f"Ace of {suit_name} — Animal Companion"
        note = "Play alone, or pair with one other card (never a Jester)."
    elif royal:
        hp = {"J": 20, "Q": 30, "K": 40}[rank_key]
        title = f"{rank_name} of {suit_name}"
        note = f"Also a castle enemy: attack {value}, health {hp}. Kill it exactly to recruit it."
    else:
        title = f"{rank_name} of {suit_name}"
        limit = 10 // value if value else 0
        note = (
            f"Combos must total 10 or less — up to {limit} of these."
            if limit >= 2
            else "Too high to combine: play it alone."
        )
    return {
        "title": title,
        "value": f"{value} to attack · {value} when discarded",
        "power": POWERS[suit_key],
        "note": note,
        "suit": suit_name,
        "symbol": symbol,
    }


def main() -> None:
    cards = {}
    for suit_key in SUITS:
        for rank_key in RANKS:
            cards[f"{rank_key}{suit_key}"] = entry(rank_key, suit_key)
    cards["J1"] = {
        "title": "Jester",
        "value": "0 to attack · 0 when discarded",
        "power": "Cancels the current enemy's suit immunity (multiplayer).",
        "note": "Solo: flip a Jester to discard your hand and refill to 8.",
        "suit": "none",
        "symbol": "★",
    }
    cards["J2"] = dict(cards["J1"])
    out = {"version": 1, "source": "docs/research/01-rules-dossier.md", "cards": cards}
    target = pathlib.Path("src/regicide/server/web/data/cardtext.json")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(out, indent=1, ensure_ascii=False) + "\n")
    print(f"wrote {target} with {len(cards)} cards")


if __name__ == "__main__":
    main()
