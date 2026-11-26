# BOT-PLAN — a high-level Regicide solo bot

**Status:** historical design. The M1–M3 search below is still unbuilt; 0.9.0 shipped a
corrected policy (`HumanEngine`, 26.5% / 9.48 on the fixed suite) and any search slice must
start there. See `docs/BOT.md` for the shipped policy and its numbers.
**Scope:** one solo bot that plays the shipped rules (`engine.DefaultRules()`, 1 seat) clearly better than
greedy, plus a headless evaluation harness. No engine changes, no new dependencies (strict GOPATH mode:
`GO111MODULE=off GOPATH=$PWD`, stdlib only).

**Recommendation (one line):** primary = **(b) turn-level beam search over macro-actions with a
progress+tempo+resource evaluation, weights fitted by self-play tuning**; fallback =
**(c) MCTS/UCT over the same macro-actions**. Everything else (a, d, e, f) is either a component of this or
a research spike; reasons in §3.

---

## 1. Problem statement

### 1.1 What "high level" means here

The bot must (i) win whole games, not just defeat more enemies than greedy, (ii) win them **without**
reading the tavern/castle order when the seed is meant to be hidden, if it is ever used against the live
server, and (iii) be reproducible: same seed, same line, same result on every machine.

Regicide solo is an **economy** problem, and the arithmetic is unforgiving:

| quantity | value | source |
|---|---|---|
| tavern play value (2–10 ×4 suits = 216, + 4 Aces) | 220 | `engine/game.go:84-105`, `engine/card.go:90-104` |
| castle play value (4×10 + 4×15 + 4×20) | 180 | `engine/card.go:152-163` |
| total playable value | 400 | asserted by `engine/selfplay_test.go:68-81` |
| castle health (4×20 + 4×30 + 4×40) | 360 | `engine/card.go:152-163` |
| slack | **40** | `engine/selfplay_test.go:80` |

So a win needs 90 % of every card in the game, *and* the 180-point royal half of that budget is only
reachable by **exactly** killing enemies so the royal goes on top of the tavern deck
(`engine/transition.go:158-190`, recruit at `engine/transition.go:163-165`), *and* the value lost to
step-4 payments must be
recycled by hearts (`engine/transition.go:109-122`). "High level" therefore means: **the bot must plan card
conservation over ~40 turns** — not play the best card this turn.

### 1.2 Why the existing bots fail (code + measurements)

- The only bot in the tree is the `steps%len(legal)` random walk in
  `engine/selfplay_test.go:34-52`. Its own comment concedes the win rate is "near zero by design"
  (`engine/selfplay_test.go:20-23`).
- The early greedy/beam calibration bots used for the first measurements are **not in the repo** — they
  were throwaway. Their reported plateau (6–8 of 12 enemies) is the number the new bot must beat, and it is
  reproducible (see §1.4).
- Why they plateau, concretely:
  1. **They optimise damage, the game charges you cards.** Every turn ends in step 4 with an incoming
     attack of up to 20 that must be paid from hand values (`engine/game.go:167-201`,
     `engine/legal.go:63-74`). A damage-maximising policy empties the hand and dies of
     `LostCannotDefend` (`engine/state.go:43-49`).
  2. **Overkill is pure waste, exactness is a 2× resource bonus.** Only `Damage == EnemyHealth` recruits the
     royal (`engine/transition.go:159-167`); every point over is gone.
  3. **Shield is the only free defence and it is per enemy** (`engine/transition.go:131-136`,
     `engine/game.go:140-156`): greedy never buys shield for the *next* turn because the shield resets on
     reveal, so a myopic policy systematically under-shields before Kings (attack 20).
  4. **Hearts is the recycling engine** — and it is only worth playing when the discard pile is non-empty
     (`engine/transition.go:109-122`, dossier §7 #11). Greedy treats hearts as a low-damage card.
  5. **A one-ply policy cannot see "hold this club for the King".** Suit powers scale with the total attack
     value of a play (`engine/legal.go:89-112`, ADR-006 §3), so the same card is worth 2× or 0× depending on
     pairing and enemy immunity (`engine/transition.go:308-316`).

### 1.3 Metric

Primary metric: **win rate over random seeds** (the standard Crockford seed set produced by the
`RandomSeedFrom` derivation, `engine/selfplay_test.go:9-18`), reported with tier split
(`engine/state.go:100-110`), average enemies defeated, average turns, and wall-clock per game.

- **Target: ≥ 70 % wins at 500 seeds**, ≥ 35 % gold (`JestersUsed == 0`), median game ≤ 60 turns.
- Intermediate, mandatory-to-report metrics: win rate, `avg enemiesDefeated` (must exceed the 7.9/12 beam
  baseline from §1.4), `LostCannotAct` vs `LostCannotDefend` split (which failure mode dominates drives the
  next milestone), and games that stall (> 150 turns = bug).
- Guard against regressions: the harness must fail the build if `winRate` drops below the last committed
  value and if any line is illegal or non-reproducible.

### 1.4 Measured baselines (calibration done for this plan, throwaway code outside the repo)

Played with `engine.LegalActions`/`engine.Transition` exactly as production does, 500 seeds for the two
whole-game rows, 40 seeds for the search rows (search is slower):

| policy | wins | gold | avg defeated | avg turns |
|---|---|---|---|---|
| random legal action (`selfplay_test.go` style) | 0 of 500 | 0 | 1.84/12 | 8.0 |
| 1-ply greedy, damage-first with exact-kill bonus | 0 of 500 | 0 | 5.02/12 | 13.2 |
| beam over raw transitions, width 1 / 4 / 16 | 0 of 40 | 0 | 6.80 / 7.12 / 7.65 | — |
| **turn-level macro beam**, width 1 / 4 / 8 / 32 | 0 / 0 / 0 / **1** of 40 | 0 | 4.70 / 5.67 / 6.30 / **7.90** | — |
| the same, 7 hand-picked weight sets at width 16 | ≤ 1 of 40 | 0 | 5.95–7.12 | — |
| fight-scoped DFS (depth 6, budget eval only) | 0 of 24 | 0 | 2.46–3.00 | — |

Three conclusions that drive the whole plan:

1. **The reported 6–8/12 plateau is real and reproducible**, and search alone does not break it: width 32
   buys +1.1 enemies and 2.5 % wins over greedy.
2. **Weight tuning alone does not move it** (0–1 wins across 7 weight sets) ⇒ the bottleneck is *search
   structure + value accuracy*, not a missing feature.
3. **The fight-scoped experiment regressed to 2.5/12** because its evaluation had no progress or tempo term,
   so the search learned to stall and hoard. Any evaluation **must** include progress and a turn cost; this
   is a measured trap, not a theory.

Engine throughput measured on the dev box: **114 k transitions/s** including a `NewRandAt` rebuild per call
(≈ 9 µs each; `NewRandAt(seed, 65)` alone is 1.6 µs — `engine/rng.go:29-38`). That sets the search budget:
a 200-seed run at 0.3 s/game is ≈ 30 k transitions/game.

---

## 2. State-space analysis

### 2.1 Phases and branching

Three resting phases exist; turn start resolves inside a transition (`engine/state.go:12-22`).

| phase | legal actions | bound | mean (measured) |
|---|---|---|---|
| `Step1Await` (`engine/legal.go:54-62`) | flip (0/1) + yield (off by default) + every `LegalPlays` entry | hand ≤ 8 singles + companion pairs + same-rank combos with `RankValue·size ≤ 10` (`engine/legal.go:132-204`); measured max 31 | 6.6 |
| `Step4Pre` (`engine/legal.go:63-74`) | flip (0/1) + one `PayDamage` per hand card, only if the hand covers the attack | ≤ 9 | ~6 |
| `Step4Discard` (`engine/legal.go:75-79`) | one `PayDamage` per hand card (+ `EndPayment`, off by default) | ≤ 8, and the sequence is ≤ 8 long | ~5 |

Effective per-turn branching (`Step1Await` × payment path) is **≈ 6–10**, not 30: the payment sub-sequence is
a subset-sum over ≤ 8 cards whose target is ≤ 20, so it can be collapsed into one macro-decision (§4.2).

### 2.2 Horizon

Measured: doomed games last 8 turns / 19 transitions (random) and 13 turns / 34 transitions (greedy). A
*won* game is longer — 12 enemies, ~3 turns each, plus one payment sequence per turn: **35–60 decisions,
~40 turns** (estimated from the measured 34 transitions for a doomed game). Depth-first search over 7^40 is hopeless; the horizon must be shortened by structure (§4.2) or
by a value function (§4.3).

### 2.3 Hidden information — both variants stated explicitly

The engine is *deterministic* given the seed: setup shuffles are pinned (`engine/game.go:52-105`,
`engine/rng.go:143-149`), and the only in-game randomness is the hearts shuffle of the discard, which is
reproduced exactly from `(seed, RGNDraws)` (`engine/transition.go:116`, `engine/rng.go:29-38`). Therefore:

- **Variant A — perfect-information oracle (seed + draw pile known).** This is a single-agent,
  perfect-information game: `bot.Next(g, seed)` may call
  `engine.Transition(g, a, engine.NewRandAt(seed, g.RGNDraws))` and every imagined future is the real one.
  Legitimate for offline analysis, golden tests, and the *ceiling* measurement. **Not leaderboard-legal**:
  the snapshot hides the seed while a random run is live (`engine/snapshot.go:181-186`), and a bot that is
  handed the seed would be cheating by the ADR-001/ADR-009 contract.
- **Variant B — realistic belief-state bot (no seed).** The bot sees only `engine.Snapshot`-equivalent
  information: hand, enemy, damage, shield, counts of tavern/castle/discard, and the visible log
  (`engine/snapshot.go:113-187`). Unseen tavern cards are a distribution — and hearts makes it worse,
  because the shuffle picks *which* discards go under the deck (`engine/transition.go:109-122`), so the
  tavern *composition* becomes uncertain, not just its order.

Both variants must ship from the same search code (§4.7): the difference is only whether the successor
function uses the true tavern or a sampled one. Ship target = **Variant B**, ceiling report = Variant A.

---

## 3. Candidate approaches, ranked

| # | approach | verdict | reason (evidence) |
|---|---|---|---|
| 1 | **(b) beam search over macro-actions + tuned evaluation** | **PRIMARY** | Already reaches 7.90/12 at width 32 (70 ms/game at width 8) with hand weights (§1.4) — the engineering is de-risked, the open problem is value accuracy, which tuning attacks directly on the real metric. Deterministic, stdlib-only, trivially `-race`-clean. |
| 2 | **(c) MCTS/UCT with determinisation** | **FALLBACK** | Removes the need for a globally consistent eval (rollouts only need the terminal reward), and determinisation is *the* standard answer to Variant B. Costs: a rollout is ~34 transitions ≈ 0.3 ms, so ~10× fewer nodes per second than beam; 200 seeds < 60 s is tight. Adopt only if (b) plateaus below target. |
| 3 | (d) alpha-beta / expectimax + TT | **component, not a bot** | This is a single-agent game — there is no adversarial bound to prune with, so "expectimax" degenerates into DFS/beam and its strength is again the leaf evaluation. Worth keeping as the *exact fight-scoped solver* (§4.6) where the horizon is short (≤ 6 turns) and a TT pays off. Use `ChaCha8.MarshalBinary/UnmarshalBinary` (Go 1.26, `math/rand/v2`) for O(1) stream forks instead of `NewRandAt` if node counts exceed ~10⁵/decision. |
| 4 | (a) greedy + hand-tuned evaluation | **rejected as the answer** | Measured: 5.02/12, 0/500 wins; adding lookahead with 7 weight sets still gives ≤ 1/40 wins. Greedy is the *component* the harness compares against. |
| 5 | (f) ILP/CP-SAT for the turn's card selection | **rejected** | No external deps allowed (ADR-001/ADR-004: GOPATH, zero deps) and the game is not a static optimisation: hearts **shuffles**, so the future deck is a distribution. Only a *sub-problem* is a clean optimisation — step-4 payment (a ≤ 8-card, target ≤ 20 subset-sum) — and that needs a 20-line DP, not a solver (§4.2). |
| 6 | (e) policy learned from self-play | **research spike / later** | Hard exploration: the measured baseline wins 0 games, so there is no positive signal to bootstrap from. Viable only after the oracle (Variant A) can generate winning lines to imitate, and only if a tiny MLP is written in stdlib Go. Keep as the M6 stretch. |

---

## 4. Design of the recommended approach

### 4.1 Layout (new code only; the engine is untouched)

```
src/regicide/bot/
  bot.go        // Next(g engine.Game, o Options) engine.Action  — the public entry
  macro.go      // TurnOutcomes(...)  — macro-action generator; PayKnapsack(...)
  eval.go       // Weights struct + Eval(g *engine.Game, w Weights) float64
  key.go        // Key(g engine.Game) string — canonical, order-insensitive where legal
  beam.go       // Search(g engine.Game, o Options) engine.Action — beam over macro-actions
  fight.go      // (M3) exact fight-scoped solver + transposition table
  tune.go       // (M4) hill-climbing weight fit over a training seed set
  bot_test.go   // goldens, legality, determinism, hard-coded traps
  bot_test.go   // win-rate harness (env-gated)
src/regicide/cmd/coopbench/
  main.go       // CLI: -seats -seed -policy -n ; mirrors the rngref layout
```
`src/regicide/tools/` is the existing home for tooling (`rngref`, `src/regicide/tools/rngref/main.go`), and
`go vet ./src/...` / `go test -race ./src/...` (`scripts/gate.sh:28-30`) pick both packages up for free.

### 4.2 Macro-action definition (the structural fix)

A **macro-action** is *one whole turn*, from `Step1Await` to the next `Step1Await` (or terminal):

```
Macro = (optional Jester flips at windows A/B, one Play|Yield, the step-4 payment schedule)
```

`TurnOutcomes(g, seed) []engine.Game` returns the ~6–10 reachable turn-boundary states:

1. Enumerate `Play` (and `FlipJester`, capped at `JestersLeft`, and `Yield` if enabled) from
   `engine.LegalActions` (`engine/legal.go:46-86`), applying each with
   `engine.Transition(g, a, engine.NewRandAt(seed, g.RGNDraws))`.
2. If the play kills the enemy, the defeat cascade already lands on a new turn
   (`engine/transition.go:158-190`) — emit and stop for that line.
3. If the transition lands in `Step4Pre`/`Step4Discard`, resolve the payment **here, not in the search**,
   with `PayKnapsack(hand, required)`: a DP over ≤ 8 cards × total ≤ 24 that returns the discard set with
   the smallest total ≥ `required` (ties: fewest cards). Rationale: overpayment is wasted
   (`engine/transition.go:254-288`) and the sequence length is bounded, so the sub-game has no strategic
   content beyond *which* cards to keep — that belongs in the evaluation, not the search depth.
   Paying is applied with the same `Transition` calls, so `Step4Paid`, `endTurn` and
   `enterTurnStart` behave exactly as in production.
4. The emitted state is always at a turn boundary ⇒ all beam entries are mutually comparable, which is
   precisely what the transition-level beam in §1.4 got wrong (it mixed `Step1Await` and step-4 states and
   scored them on the same scale).

### 4.3 Evaluation features (starting weights)

`Eval` is called only on turn-boundary states + terminals. Weights live in one struct so M4 can fit them.

| feature | start weight | why (cite) |
|---|---|---|
| `won` / `lost` | ±1e9 | terminal (`engine/state.go:12-22`) |
| `enemiesDefeated` = `11 - len(Castle)` (12 if won) | +1000 | the actual objective |
| `castleHealthRemaining` = Σ `EnemyStats().health` for castle + `EnemyHealth()-Damage` | −1.0 | a King is 2× a Jack, 4× the health (`engine/card.go:152-163`) |
| `damageProgress` = `Damage/EnemyHealth` | +400 | never stall a fight |
| `handValue` = Σ `PlayValue` | +3.0 | hand is both damage and step-4 payment (`engine/game.go:230-236`) |
| `handCards` | +3.0 | card *count* gates step 4 and combos; measured trap (§1.4) |
| `tavernValue` | +1.5 | only reachable via diamonds (`engine/transition.go:124-129`) |
| `discardValue` | +0.5 | hearts fuel (`engine/transition.go:109-122`, dossier §7 #11) |
| `jestersLeft` | +150 | two hand resets per game (`engine/state.go:238`, `engine/transition.go:192-230`) |
| `effectiveShield` (`engine/legal.go:114-120`) | +3.0 | free prevention; resets per enemy — do not credit it against a *future* enemy |
| `incomingIfNothing` = `max(0, EnemyAttack - effectiveShield)` (`engine/legal.go:122-130`) | −3.0 | tempo/defence pressure |
| `turnNo` | −2.0 | **tempo**: without this the search stalls (measured §1.4, fight-DFS row) |
| `atRisk` = `handValueTotal < incoming` (bool) | −5000 | near-loss; mirrors `LostCannotDefend` |
| `recruitReady` (exact kill available this turn) | +25 | 180-point budget only reachable this way (`engine/transition.go:159-167`) |
| `suitDiversity` (distinct suits in hand) | +1.5 | suit powers fire once per suit per play; a mono-suit hand wastes value |

Notes: no weight is holy — they are the M2 starting point and M4 replaces them with fitted values. The
measured lesson stands: drop `damageProgress` or `turnNo` and the bot learns to stall.

### 4.4 Search

`Search(g, Options{Width, Budget, PerfectInfo, Seed}) engine.Action`:

- Beam over macro-actions, re-planned at every turn boundary (open-loop within a turn is illegal — the plan
  can be invalidated by a payment shortfall, see `engine/legal.go:63-74`).
- Dedupe by `Key`; keep the best `Width` by `Eval`; stop on `Won`/`Lost`.
- **Limits:** `Width` 8 default (measured 70 ms/game ⇒ 200 seeds ≈ 14 s), 32 for offline sweeps
  (measured 330 ms/game ⇒ 66 s, above the CI budget). Hard caps: `Budget` = 2000 transitions per decision,
  20 000 per game; depth cap 80 turns (a stall is a bug, §1.3).
- Return the **first engine action** of the best line's first macro-action, not a macro object, so the bot
  is a drop-in for `engine.Transition` and stays auditable.
- Variant B (no seed): the unseen tavern tail is sampled once per *decision* (`determinisation`) and the
  same sample is shared by the whole search that follows it (fixed-sample, not root-parallel averaging);
  the action is chosen by mean score over `Determinisations` samples (start 4, cap 16). Sampling can never
  propose an illegal move — legality reads only hand/enemy/phase (`engine/legal.go:46-86`) — it can only
  *misvalue* one, which is exactly what the extra samples average out.

### 4.5 Using the engine without changing it

Only two engine entry points are needed: `engine.LegalActions` (`engine/legal.go:46`) and
`engine.Transition` (`engine/transition.go:11`), plus value helpers (`engine/card.go:82-104`,
`engine/game.go:230-247` are unexported — the bot must re-derive `EnemyAttack`/`handValueTotal` from
`engine.EnemyStats`, `engine.RankValue` and the exported `Players[0].Hand`; a 6-line helper, no engine edit).
`Transition` clones on entry (`engine/transition.go:337-354`), so search copies never alias; `Game` is a
plain value, so a node can be stored in a slice by value. One optimisation: search copies may set
`g.Log = nil` before use — the log is bounded at 512 events (`engine/transition.go:48-69`) and dominates
copy cost in deep lines; the returned action never depends on it.

### 4.6 M3 extra: exact fight-scoped solver

Because a fight ends when the enemy dies (`engine/transition.go:158-190`), its horizon is ≤ ~6 turns and its
branching ≤ 10. `fight.go` adds an exhaustive DFS over macro-actions **within one enemy**, with a
`map[Key]float64` table and iterative deepening; the leaf value is `Eval` at the post-fight boundary. This
is approach (d) restricted to where it is exact and affordable, and it is the cheapest way to buy the
"shield before the King" and "hold the club" behaviours greedy lacks. Budget: 20 k nodes/fight.

### 4.7 Determinism and reproducibility

- The bot is a pure function of `(Game, Options)`: no `math/rand`, no map iteration order in scoring paths
  (`Key` must sort hand IDs and card multisets because Go map iteration order varies between runs), no time-based cutoffs
  (budget is counted in transitions, never in milliseconds).
- Every `Transition` inside the search passes `engine.NewRandAt(seed, g.RGNDraws)` (`engine/rng.go:29-38`),
  exactly as `engine/selfplay_test.go:44-46` does. This is what makes hearts-shuffled futures identical to
  the real ones; using a plain `NewSeededRand(seed)` would silently desynchronise after the first hearts.
- Golden tests assert the first 10 actions on fixed seeds, so any behavioural change is deliberate.
- `engine.Version` (`engine/state.go:193`) is *not* touched: the bot is an observer, not a rules change.

### 4.8 Seeds

- Games: `engine.NewGame(engine.SetupOptions{Seed: s, SeedRandom: true, Rules: engine.DefaultRules(),
  NumPlayers: 1}, engine.NewSeededRand(s))`.
- Benchmark seed set: `bot.SeedFor(i)` reimplements the `RandomSeedFrom` derivation
  (`engine/selfplay_test.go:9-18`) so bot numbers are directly comparable with engine test numbers.
  It is duplicated rather than moved because that helper lives in a `_test.go` file and importing it would
  mean touching the engine package (**open question 7.3**).
- Training/holdout split for M4: seeds 0–399 fit, 400–499 held out and reported separately.

---

## 5. Evaluation harness

`src/regicide/bot/bot_test.go` + `src/regicide/cmd/coopbench/main.go`:

```
TestWinRate        // env REGICIDE_BOT_BENCH=1 gates it (default: skip) — runs Options.SeedCount seeds
BenchmarkBotSolo   // b.N seeds; reports wins, gold/silver/bronze, avg defeated, avg turns, ns/game
TestBotIsLegal     // every returned action ∈ LegalActions(g); never nil while legal actions exist
TestBotIsDeterministic // same seed twice ⇒ identical action + event sequence
TestBotBeatsGreedy // wins > 0 and win rate ≥ greedy + 5 pts on the 200-seed set
```

Contract:
- Runs headless to completion via `engine.LegalActions`/`engine.Transition`; no HTTP, no chromium.
- Reports one line per bot: `wins/seed, gold%, avg defeated/12, avg turns, µs/game`, plus the
  `LostCannotAct` / `LostCannotDefend` split (`engine/state.go:43-49`).
- **Runtime budget: 200 seeds < 60 s** ⇒ ≤ 300 ms/game. Width 8 measured at 70 ms/game, so the default is
  width 8 with `Budget` 2000/decision; the harness fails if the run exceeds 60 s (`time.Since` guard).
- `-race` safe: no shared mutable state, no goroutines; if M5 parallelises seeds it uses a worker pool with
  one `Game` per goroutine and a results channel (`-race` then stays clean by construction).
- **Gate integration:** `scripts/gate.sh:30` runs `go test -race -cover ./src/...` under a 180 s timeout, so
  the heavy test must stay skipped by default (env gate) and only the fast unit tests run in the gate; the
  60 s budget applies to the explicit `botbench` invocation.

---

## 6. Milestones

| # | milestone | acceptance test | expected win rate (500 seeds) | expected avg defeated |
|---|---|---|---|---|
| M0 | **Harness + baselines.** `bot_test.go`, `cmd/coopbench`, greedy + random reference policies, JSON output. | `botbench -seeds 500 -bot random` reproduces 0 % / 1.8 defeated ± 0.2; 200-seed run < 60 s; `-race` clean. | 0 % | 1.8 (random) / 5.0 (greedy) |
| M1 | **Macro-action machinery + naive eval.** `macro.go`, `key.go`, `eval.go` (§4.2/4.3 weights). | `TestBotIsLegal`, `TestBotIsDeterministic`; beam width 1 ≥ greedy's 5.02 defeated. | 0–1 % | ≥ 5.5 |
| M2 | **Beam search (width 8/32).** `beam.go` with dedupe + budget. | 200 seeds at width 8 < 60 s; width 32 ≥ 7.5 defeated (the measured beam ceiling). | 1–3 % | ≥ 7.9 |
| M3 | **Fight-scoped exact solver** (`fight.go`) + tuned payment DP. | On a 24-seed set, wins ≥ 3/24 (the M2 baseline measures ~1/24 at width 32) and no game exceeds 100 turns. | 12–25 % | ≥ 9.5 |
| M4 | **Fitted weights** (`tune.go`, hill-climb on seeds 0–399) + holdout 400–499. | Holdout win rate ≥ training win rate − 3 pts (no overfit); both reported. | 35–50 % | ≥ 11 |
| M5 | **Variant A oracle + Variant B ship.** Perfect-information search reports the ceiling; determinised search ships as the default bot. | Ceiling report: `oracleWins ≥ shipWins`; ship ≥ 70 % on the holdout; `botbench` JSON archived under `docs/`. | **≥ 70 %** | ≥ 11.8 |
| M6 (stretch) | **Self-play policy distillation** (§3 #6) or MCTS fallback if M5 misses. | A/B against M5 on the same seeds; keep the winner, document the loser. | — | — |

Expected-gain sources: M1→M2 is search structure (+2.5 defeated measured), M3 is horizon + exactness (+1.5–2),
M4 is the value function (+1.5), M5 is perfect information for the ceiling and belief handling for the ship
rate. If M5 lands below 70 %, the decision point is M6 (MCTS fallback) — not more weight tuning (§1.4 shows
tuning alone moves ≤ 1 win in 40).

---

## 7. Risks and open questions

1. **Is ≥ 70 % even achievable?** The 40-point budget slack says a win is a tight line; whether > 70 % of
   seeds are winnable *at all* is unknown. Mitigation: M5's Variant-A ceiling answers this before any
   promise is made. If the ceiling is e.g. 60 %, the target must be restated as "≥ 90 % of the oracle's
   win rate", and that is a decision for the operator, not for the implementer.
2. **Does the bot need the tavern order?** It must not, for the shipped variant (§2.3, `engine/snapshot.go:181-186`).
   But a bot that is only ever run offline (golden tests, research) can legitimately use the seed — hence
   the explicit `Options.PerfectInfo` switch and two reported numbers.
3. **`RandomSeedFrom` lives in a `_test.go` file** (`engine/selfplay_test.go:9`). Either duplicate it in
   `bot` (planned) or export it from the engine (an engine edit — needs approval; it would also touch
   `engine.Version` discipline if it ever changed). Duplication risks divergence; a one-line comment in both
   places must point at the other.
4. **Overfitting to the 500-seed set.** Mitigated by the 0–399 / 400–499 split and by reporting width-1
   greedy on the same split, but a bot fitted to seeds is still fitted to the seed distribution. If the
   leaderboard ever lets players type their own seed, adversarial seeds are a real (if out-of-scope) risk.
5. **Search changes the meaning of the leaderboard.** A bot that wins 70 % of gold runs would sit on top of
   every tier; the board is per-tier, per ADR ("one global board", `docs/DECISIONS.md` Part C D-4). Decide
   *before* M5 whether bot runs are (a) excluded, (b) a separate "reference line" list, or (c) allowed. This
   is a product decision, not an engineering one.
6. **Search cost vs the 60 s budget.** Width 32 already exceeds it (330 ms/game). If more strength needs
   wider search, the budget must move to a per-game node count and the CI run must shrink to 100 seeds, or
   the harness must parallelise (safe, see §5) — but `go test -race` is already at 180 s in the gate.
7. **Evaluation blind spots.** The eval cannot see that a hand of two 5s is worth more than 10 (a combo
   plays both for 10 damage *and* one suit power, `engine/legal.go:132-204`, dossier §7 #12). Add a
   `comboPotential` feature in M4 if the fitted weights saturate the current set.
8. **Stalling as a failure mode.** Measured once already (§1.4, fight-DFS). The harness's "no game > 100
   turns" assertion is the guard; keep the `turnNo`/`damageProgress` terms even if fitting wants to zero them.
9. **Two Jesters are worth a tier.** `jester * 150` is a guess; a gold run (`engine/state.go:100-110`) is
   strictly better than a silver one, so M4 should fit against a *tier-weighted* objective
   (win = 1, gold = 1.5) rather than a flat win rate, once wins exist to weight.

---

## 8. Effort estimate

| milestone | effort | notes |
|---|---|---|
| M0 harness + baselines | 3 h | mostly extracting `selfplay_test.go`'s random walk into a reusable reference policy |
| M1 macro-actions + naive eval | 4 h | the payment DP is the fiddly part |
| M2 beam + dedupe + budget | 6 h | `Key` must be canonical or the beam collapses |
| M3 fight-scoped solver | 8 h | TT, iterative deepening, stall guards |
| M4 weight fitting | 6 h | hill climbing over ~500-seed evaluations; needs the runtime budget to hold |
| M5 oracle + determinised ship | 12 h | two search modes, two reports, holdout discipline |
| M6 stretch (MCTS fallback or distillation) | 10 h | only if M5 misses 70 % |
| **total to M5** | **39 h** | +10 h stretch, +2 h doc/ADR write-up (`docs/DECISIONS.md` ADR-010 + `docs/BOT.md`) |

---

## Appendix A — how the baselines in §1.4 were measured

Throwaway `main` packages outside the repo (`/tmp/gp/src/probe*`), importing `regicide/engine` via a
symlinked GOPATH (`GOPATH=/tmp/gp`, `GO111MODULE=off`). Policies: random = `legal[step%len(legal)]`
(`engine/selfplay_test.go:40-52`); greedy = damage-first with an exact-kill bonus and cheapest-first
payment; beams as described in §1.4, with dedupe on a canonical key and `Eval` per §4.3. Numbers are from
the dev box, single-threaded; they are calibration, not a contract. The full harness proposed in §5 replaces
these probes.

## Bulk self-play corpus

`coopbench` now writes the games it measures:

```bash
./bin/coopbench -n 1000 -seats 3 -policy greedy \
  -corpus corpus/selfplay-3/replays \
  -training corpus/selfplay-3/decisions.jsonl
```

Every replay is verified on write; the training file has `state`, `legal`, `chosen`,
`wonTable` and `stepsLeft` per decision. Use it for offline evaluation and regret
analysis against `bot.DefaultPolicy()` (currently `HumanBestOf`). Imitating the current
bot only reproduces it: the value is the baseline, the legal-action mask, and a large set
of seeds to compare candidates on. Human games remain the scarce input; browser replays
are archived under `corpus/`.
