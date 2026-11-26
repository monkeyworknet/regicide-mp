# Rules coverage ledger

Purpose: bind every rulebook clause to the Go tests that prove it. `scripts/gate.sh` runs
`tools/assets/check_rules_coverage.sh`, which asserts:

1. every `R-0xx` id in `docs/research/01-rules-dossier.md` Appendix B appears in this file;
2. every id in this file appears in at least one Go test name (`grep -r "TestR0xx" src/`);
3. no row cites a test that does not exist: `tools/assets/check_rules_coverage.sh` resolves every
   literal `TestName` in this file against `func Test…` in `src/`.

The rulebook text quoted here lives in `docs/research/rules-raw.txt` (source: the official A4 rules PDF).

## Setup

| Rulebook clause (abridged) | R-IDs |
|---|---|
| "Shuffle the 4 Kings […] and put them facedown … Queens on top … Jacks on top. This is the Castle deck." | R-002, R-003 |
| "turn the top card face up to reveal a Jack. This will become the current enemy." | R-003 |
| "Shuffle together all the cards numbered 2 to 10 with the 4 Animal Companions and a number of Jesters" | R-002 |
| Player table: 1 player → 0 Jesters, max hand size 8 | R-001, R-005 |
| "Deal cards to each player up to their maximum hand size" | R-001 |
| Enemy stats: Jack 10/20, Queen 15/30, King 20/40 | R-030 |
| "Solo: place the two Jesters to the side … single hand limited to 8 cards" | R-001, R-047 |

## Turn structure — Steps 1–4

| Clause | R-IDs |
|---|---|
| "Step 1 – Play a card or yield"; "the number of the card determines the attack value" | R-006, R-036 |
| "Step 2 – Activate the suit power" — mandatory | R-019, R-027 |
| "Step 3 – Deal damage to the enemy and check to see if the enemy is defeated" | R-030 |
| "If the enemy is defeated, do the following: (i) place the enemy in the discard pile. If … exactly equal … place it facedown on top of the Tavern deck instead." | R-031, R-032 |
| "(ii) Place all cards played by players against the enemy in the discard pile." | R-033 |
| "(iii) Turn the next card of the Castle deck face up." | R-034, R-046 |
| "(iv) The player who has just defeated the enemy skips Step 4 and begins a new turn" | R-034, R-035 |
| "Step 4 – Suffer damage … by discarding cards … total value at least equal to the enemy's attack value" | R-039, R-040 |
| "Discard the cards one at a time" (+ stop-at-threshold ruling, dossier §8 #2) | R-039 |
| "If the player cannot discard enough cards to satisfy the damage, they die" | R-040 |
| "It's ok to have an empty hand." | R-042 |
| "After suffering damage, the next player … begins their turn" | R-041 |

## Suit powers

| Clause | R-IDs |
|---|---|
| "Suit powers are mandatory and cannot be skipped." | R-019, R-027 |
| Hearts: "Shuffle the discard pile then count out a number of cards facedown equal to the attack value played. Place them under the Tavern deck … return the discard pile faceup." | R-015, R-016, R-017 |
| "Any time where both a Hearts power and Diamonds power are resolved together, resolve the Hearts healing before drawing with Diamonds." | R-014 |
| Diamonds: "The current player draws a card. The other players follow … until … equal to the attack value … Players that have reached their maximum hand size are skipped." | R-019, R-020 |
| "There is no penalty for failing to draw cards from an empty Tavern deck." | R-021 |
| Clubs: "damage dealt by clubs counts for double." | R-022 |
| Spades: "reduce the attack value of the current enemy by the attack value played … cumulative … remain in effect until the enemy is defeated." | R-023, R-024, R-025, R-026 |
| Played cards are staged until the enemy dies (dossier §8 #8, BGA) | R-033, R-018 |

## Combos and Animal Companions

| Clause | R-IDs |
|---|---|
| "sets of 2, 3 or 4 of the same number as long as the combined total … equals 10 or less" | R-012 |
| "all suit powers are resolved at the total attack value" | R-013 |
| "Animal Companions cannot be added to a combo or played as a combo on their own" | R-011 |
| "can be played on their own, but may also be paired with one other card (except the Jester)" | R-006, R-007, R-010 |
| "paired with one other Animal Companion" | R-008 |
| "If you play an Animal Companion with another card of the same suit, you only apply the suit power once." | R-009 |
| "Animal Companions (A) have a value of 1" | R-007, R-036 |

## Jester

| Clause | R-IDs |
|---|---|
| "the Jester may be played (always on its own) … attack value of 0" (multiplayer path) | R-049 |
| "negates the immunity of the enemy" | R-052 |
| "skip steps 3 and 4 … the player of the Jester chooses any player to go next" | R-049 (`TestCoopJesterPlayAsksWhoGoesNext`, `TestCoopChooseNextSeatSkipsPlayers`, `TestCoopChooseNextSeatRejectsUnknownSeat`) |
| Solo: "Discard your hand and refill to 8 cards – this does not count as drawing for the purpose of enemy diamond immunity." | R-047, R-051, R-052 |
| Solo: "You are allowed to use the Jester power a) at the start of Step 1 … or b) at the start of Step 4" | R-048, R-049 |
| Solo: "Since you have two Jesters this can be done twice per game." | R-050 |
| Solo: "Flipping the Jesters in this way does not cancel enemy immunity." | R-052 |
| Solo: "0 Jesters = Gold, 1 = Silver, 2 = Bronze" | R-055 |
| Window reopens after a kill (dossier §8 #12) | R-054 |
| Flip with an empty tavern still spends the Jester (dossier §8 #13/#23) | R-053 |

## Immunity

| Clause | R-IDs |
|---|---|
| "Each enemy is immune to the suit powers of cards played against them which match their suit." | R-027, R-028, R-029 |
| "(however the number is still added to the damage total)" | R-027, R-028 |
| "The Jester can be played to cancel an enemy's immunity." | R-052 |
| Spades played before a Jester still reduce attack (dossier §6.1) | R-023, R-029 |
| Clubs played before a Jester do not double (dossier §6.1) | R-022, R-028 |

## Yielding, communication, game end, solo

| Clause | R-IDs |
|---|---|
| "A player may not yield if every other player has yielded on their last turn." | R-044 |
| Yield "skipping Steps 2 and 3" | R-045 |
| "The game ends when the players win by defeating the last King" | R-046 |
| "or when the players lose because a player is unable to satisfy the damage" | R-040 |
| "The players also lose if any player is unable to play a card or yield on their turn." | R-043 |
| "Jacks in hand count as a 10, Queens as 15, Kings as 20 … Their suit power is applied as normal when played." | R-036, R-037, R-038 |
| Solo: "You play with a single hand limited to 8 cards." | R-001, R-005 |

## Derived invariants (not stated in the rulebook; engineering guarantees)

| Invariant | R-IDs |
|---|---|
| Card conservation (54 distinct ids across zones) | R-004 |
| Hand never exceeds the limit | R-005 |
| Counters never negative | R-059 |
| Same seed + same actions ⇒ identical state | R-057, R-058 |
| No solo-only shortcuts in the engine (seat parameter round-trip) | R-056 |
| Random legal play always terminates (hard cap 10 000 transitions) | R-060 |
| Overkill is not carried to the next enemy (per-enemy health) | R-035 |
| Step-4 requirement of 0 needs no discards | R-041 |

## Optional rules (Settings, frozen per run)

These are operator-requested variants. They are implemented in the engine and covered by named
tests rather than matrix rows, because they are not in the rulebook. The R-ID column is the matrix
row each variant *modifies*, so a reader can see which rulebook clause a setting bends; the gate
still asserts only matrix ids (`tools/assets/check_rules_coverage.sh`), which is why no variant id
appears in the ledger above.

| Variant | Modifies | Behaviour | Tests |
|---|---|---|
| Solo Jester (`soloJester`, on by default) | R-018 – R-022 | Flip a Jester to discard the hand and refill; twice per game; sets the gold/silver/bronze tier. | `TestR018`…`TestR022`, `TestR047_FlipDiscardsHandAndRefills`, `TestR048_FlipAtStepFourLetsNewHandPay` |
| Exact damage (`exactDamage`, on by default) | R-031, R-032 | An exactly-killed enemy goes on top of the tavern deck and is recruited. | `TestR030`, `TestR031` |
| Yield (`allowSoloYield`, off) | R-044, R-045 | Enables the yield action in solo play (skips steps 2–3). In co-op yielding is always available, because R-043 makes it the escape hatch from an empty hand. | `TestR044`, `TestR045`, `TestCoopCannotActNeedsYieldToo`, `TestCoopYieldRingIsExact` |
| Overpay discard (`allowOverpayDiscard`, off) | R-039, R-041 | Continue discarding past the step‑4 threshold, ending with an explicit payment end. | `TestR041`, `TestEndPayment…` |
| **Chaos mode** (`chaos`, off) | R-002, R-003, R-034 | The twelve royals are shuffled into one castle instead of the pinned Jacks→Queens→Kings ramp, so a King can be the first enemy. The shuffle uses the same stream and draw count, so a seed still replays exactly. Chaos runs are not eligible for the leaderboard, since the difficulty is not comparable. | `TestChaosCastle`, `TestChaosIsReproducible`, `TestStandardCastleUnaffected` |
