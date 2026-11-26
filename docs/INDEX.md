# Docs index

One role per file. A **live** file must match the code on `main`; a **reference** file is
normative for its subject. When a reference file and the code disagree, the code wins and the
file is a bug to fix.

Some decision entries name a planning or review note that is not part of this repository; the
decision they produced is, and that is what binds.

## Live

| File | What it is |
|---|---|
| `DECISIONS.md` | Binding decisions (ADR-001…, D-1…); statuses note amendments and supersessions. |
| `MP-PLAN.md` | The co-op design: rooms, seats, transport, board, spectating. |
| `REPLAY-FORMAT.md` | The log format, verification, and the viewer contract. |
| `rules-coverage.md` | Rulebook clause → R-ID → test ledger. Checked by `tools/assets/check_rules_coverage.sh`. |
| `CREDITS.md` | Asset provenance; the gate checks it against the files on disk. |
| `BOT.md` | The bot: policies, bench numbers, what won and lost, and the shipped default. |
| `BOT-PLAN.md` | Bot design record: the search variants considered, what shipped, what is unbuilt. |
| `PLAN.md` | The v0 solo plan (historical): the contract ADR-008 and the engine rules were written against. |
| `PROCESS.md` | Branches, the merge gate, ports, and what a change must prove before it lands. |
| `AUDIO-PLAN.md` | Event inventory, current cues, gaps, and options for new sound. |
| `layout-baseline.json` | Board geometry baseline for the 390 px and 1440 px layout checks. |
| `INDEX.md` | This file. |
| `../README.md` | What the project is and how to run it. |

## Reference (normative for its subject)

| File | What it is |
|---|---|
| `research/01-rules-dossier.md` | The rulebook matrix `R-001…R-060` (dossier v1). |
| `research/rules-raw.txt` | The rulebook text the dossier was built from. |
| `screens/` | Screenshots used by the docs. |

## Keeping this honest

A live file may not state a version, gate count, test count, path or feature status without a
check against the repo. Update this index in the same change that adds or retires a document.
