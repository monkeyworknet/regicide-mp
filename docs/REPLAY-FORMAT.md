# The game log: format, playback, and what it is for

Status: **implemented** (player records every game; `cmd/replay` verifies and exports).
Code: `engine/hash.go`, `engine/actioncodec.go`, `replay/`, `server/replay.go`.

A replay answers three questions with one file:

1. **What happened?** Seed, rules, seats, and every action in order, with each player's
   hand size after every move.
2. **Is it true?** The file carries a state hash after each action. Playback recomputes
   the game from the seed and compares: a replay does not merely claim a game happened,
   it proves it.
3. **What can be learned from it?** A training corpus of `(position, legal actions,
   action taken, outcome)` decisions, one per move, exportable with one flag.

## File shape

JSONL — one JSON object per line, `kind` says which. Appended action by action, so a
crash loses at most the move in flight. Line 1 is the header, the last line is the
result; everything between is a step.

```jsonl
{"kind":"header","header":{"format":"regicide.replay","version":1,"engine":2,"mode":"coop","room":"2023E","seed":"XNJBY0CTAAS3","seedRandom":true,"theme":"simple","rules":{"soloJester":false,"exactDamage":true,"allowSoloYield":false,"allowOverpayDiscard":false,"chaos":false},"numPlayers":3,"players":[{"seat":0,"name":"George"},{"seat":1,"name":"Dagobert"},{"seat":2,"name":"Pepin"}],"startedAt":"2026-11-16T02:31:04Z"}}
{"kind":"step","step":{"seq":5,"seat":0,"action":{"type":"play","cardIds":["AD"]},"hash":"6ae9970c2d5aaa61ef6a6285d1bfd822","turn":1,"phase":"step1","hands":[5,6,6]}}
{"kind":"result","result":{"phase":"lost","won":false,"lostReason":"cannot_act","lostSeat":1,"turnNo":8,"castleLeft":8,"jestersUsed":0}}
```

### Header

| Field | Meaning |
|---|---|
| `format`, `version` | `regicide.replay`, currently `1`. A reader refuses anything else. |
| `engine` | `engine.Version` at record time. A rules change bumps it; playback refuses a mismatch rather than pretending old semantics. |
| `mode`, `room` | `solo` or `coop`, plus the room code (`""` for solo). |
| `seed`, `seedRandom` | The deal. Everything reconstructs from this; `seedRandom` records that the *server* chose it (so the file is analysis evidence, not a leaderboard claim). |
| `theme`, `rules`, `numPlayers` | The other setup inputs. |
| `players[]` | Seat number and the name the server assigned. |
| `startedAt` | Wall clock, for filing. Nothing replays from it. |

### Step

| Field | Meaning |
|---|---|
| `seq` | The session sequence number the action was applied under. Gaps mean a lost line. |
| `seat` | The seat that acted (a spectator cannot appear here). |
| `action` | The wire action exactly as the engine consumed it. |
| `hash` | `engine.StateHash` **after** the action: 32 hex characters. |
| `turn`, `phase`, `hands` | Denormalised convenience: turn number, phase name, cards per seat after the move. An analysis tool can chart the table without a rules engine. |

### Result

`phase`, `won`, `lostReason`, `lostSeat`, `turnNo`, `castleLeft`, `jestersUsed`. Written
when the game ends, by whichever action ended it.

## Invariants

- **Determinism.** `NewGame(seed, rules, numPlayers)` plus the ordered actions reproduces
  the game exactly: the shuffle is ChaCha8 seeded by SHA-256 of the seed, and the stream
  is resumed from `rngDraws` (`engine.NewRandAt`), never from a serialised PRNG state.
- **The hash chain.** `StateHash` covers every field that affects play — players' hands,
  piles in order, counters, phase, turn, RNG offset — and deliberately excludes the
  history log, which is derived. One edited action, one edited hash, one truncated file
  fails at the step where they diverge.
- **One writer.** Steps are appended under the session lock, in application order.
  Retried requests hit the idempotency cache before the recorder, so a retry never
  duplicates a step.
- **Bounded.** A 100-action game is ~15 KB. The file dies with its session (delete or GC).

## Where it lives, and who may read it

`<data>/replays/<sessionID>.jsonl` on the server.

- `GET /api/sessions/{id}/replay` — solo, whoever holds the session id (the id is already
  the credential for that game).
- `GET /api/rooms/{code}/replay` — co-op, **host only**.
- Never served to a spectator: unlike the live state, a replay contains every hand at
  every moment. It is an analysis artefact and is treated as one.

## Playing one back

```bash
./bin/replay -verify  game.jsonl     # rebuild from the seed, compare every hash
./bin/replay -summary game.jsonl     # outcome, per-seat actions and damage
```

```
coop simple seed=XNJBY0CTAAS3 players=3 steps=15 -> lost
verified: 15 steps, final phase lost, castle 8 left, turn 8
  seat 0 George        5 actions,  27 damage dealt
  seat 1 Dagobert      5 actions,  13 damage dealt
  seat 2 Pepin         5 actions,  17 damage dealt
```

`-verify` is not optional sugar: `-summary` and `-training` verify first, because a
summary of a file that does not replay is a summary of nothing.

## Training a bot from it

```bash
./bin/replay -training corpus.jsonl game.jsonl
```

One JSON object per decision:

```json
{"seq":5,"seat":0,"seatName":"George","turn":1,"phase":"step1",
 "state":{…engine.ClientState, the seat's own view…},
 "legal":[…every ActionView the gate offered…],
 "chosen":{"type":"play","cardIds":["AD"]},
 "hands":[5,6,6],"incoming":10,"enemy":"J♠","wonTable":false,"stepsLeft":10}
```

That is the shape imitation learning and offline evaluation both want:

- **Imitation** — `state` (features) → `chosen` (label), with `legal` telling the model
  what was *possible*, so a policy head can be masked to legal moves rather than learning
  to avoid illegal ones.
- **Offline evaluation** — replay a candidate policy against the same seeds and compare
  `wonTable`, `castleLeft` and turns-to-defeat, which the corpus already carries.
- **Regret analysis** — because `legal` holds every option including its server-priced
  attack, a stronger policy can be scored against the recorded choice without re-running
  the game: this is how the seat script in `scripts/coop-seat.py` was tuned, and how the
  solo bot plan (`docs/BOT-PLAN.md`) would measure a candidate.
- **Table-level features** — `hands` and `incoming` are recorded per step, so card
  economy (cards spent per royal defeated) is a directly computable metric.

Limits, stated plainly: a replay records decisions, not hesitation (no timestamps per
step), no cursor or presence data, and no hidden information beyond the hands the server
already knew. It is a record of a game, not a recording of a room.

## Generating a corpus from self-play

`coopbench` can record the games it measures, so a policy change produces evidence as
well as a number:

```bash
./bin/coopbench -n 1000 -seats 3 -policy greedy \
  -corpus corpus/selfplay-3/replays \
  -training corpus/selfplay-3/decisions.jsonl
```

Each corpus file is a normal replay, read back and `Verify`d before the run continues;
a game that fails to replay fails the bench. The training file holds one record per
decision in the shape above. Seeds are `-seed` plus the game number, so a corpus is
reproducible by command line alone. This is the cheap way to build thousands of
verifiable games; browser sessions are for human play, which the corpus cannot
manufacture.

## Not built yet

- ~~A web viewer (scrub the log in the browser).~~ Built: `/replay?replay=<ticket>`
  (`web/replay.html`, `web/replay-viewer.js`, D-47). Co-op hosts mint the ticket from
  the result sheet; a solo session can mint one too (`POST
  /api/sessions/{id}/replay/ticket`) and the solo end sheet now offers **Watch the
  replay** and **Download the log**.
- Multi-file corpora and dedup across games.
- A canonical "rating" of a move, which needs a reference policy — that is the bot's job.
