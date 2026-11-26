# The co-op bot

P7. A policy that plays Regicide, a bench that measures it, and a bot seat a host can
hand to an absent player.

```
go build -o bin/coopbench regicide/cmd/coopbench
go build -o bin/botlab    regicide/cmd/botlab
./bin/coopbench -n 200 -seats 3 -policy human-engine
go test regicide/bot -race
```

## Current status (2026-11-18, build 0.9.0)

A real bot seat plays **`HumanEngine`** (`bot.DefaultPolicy()`). On 2,000 fixed 3-seat
seeds it scores **26.5% wins and 9.48 royals** (CAPCHECK); on four unseen prefixes it
scores **25.9–29.6% / 9.41–9.63**; 2-seat **16.7% / 8.82**, 4-seat **10.9% / 8.22**.
The old `HumanBestOf` weights, on the same corrected evaluator, score 8.4% / 8.27; the
old shipped 0.8.9 numbers were 4.0% / 7.77 because the evaluator itself was wrong.

Reproduce with:

```bash
./bin/coopbench -n 2000 -seats 3 -seed CAPCHECK -policy greedy
./bin/coopbench -n 2000 -seats 3 -seed CAPCHECK -policy human-bestof
./bin/coopbench -n 2000 -seats 3 -seed CAPCHECK -policy human-engine
```

The fixed seed prefixes (`HUMANCHECK`, `CAPCHECK`, `FINAL7`, `RAND7`, `T7VAL`,
`T7FINAL`) are the comparison suite. Change a policy, run the tuning prefix and at least
one unseen prefix, and compare wins and royals; do not tune on one prefix only. `botlab`
runs many weight variants over one prefix in one process:

```bash
./bin/botlab -n 2000 -seats 3 -seed CAPCHECK -base human-engine -configs round.json
```

`round.json` is `[{"name":"x","overrides":{"Shield":0.3}}]`; every float64 field of
`bot.Weights` is settable. `-dump` prints the base weights.

## The rule that shapes everything

**A bot may only know what the seat it plays knows.** `bot.Observe(game, seat)` and
`bot.FromView(projection)` are the only constructors, and neither can see another hand,
the tavern's order, or the seed. The bench builds the same restricted view for every
seat, so its numbers measure play and not a cheat (T7.5).

`bot.FromView` is how the seat in the server works: it takes the seat's own
`engine.ClientState` — the exact struct its browser receives — so the privacy boundary is
the projection, not a promise in the bot.

## What the policy does

`Greedy` is policy v1, and it is economy-aware in one specific sense: **the resource that
runs out is cards.** Every turn the active seat pays the royal's attack out of its hand,
the tavern never reshuffles (R-021), and the only ways cards come back are the Hearts
power and an exact-kill recruit. So a play is judged by what the whole turn costs:

```
cost  = cards played + the cheapest payment the rest of the hand can still make
score = damage / cost * 10
      + 26 if it ends the royal
      + shield (the payment it removes now, and for the rest of that royal's life)
      + hearts and diamonds by what they put back in the deck
      - a Queen or King spent as damage
```

Three rules are not weights, they are mistakes the bench caught:

1. **Pay with the cheapest cover, not the biggest cards.** `paymentCards` is a subset
   sum: least total value that covers the bill, then fewest cards. Paying a King for a
   10-damage bill wastes ten points of a finite deck.
2. **Keep the last card only when there is a bill.** With nothing to pay, spending it is
   free; the earlier version refused, and then had no legal action left at all.
3. **A Jester's "who goes next" blocks everything else.** The engine offers only
   `ChooseNextSeat` while a choice pends; a policy that ignores it asks for actions the
   rules refuse. The bot gives the turn to the seat with the most cards, which is public
   information (D-44).

## Measured, 2026-11-16

200 seeds, three seats, `-race`:

| Policy | Royals cleared | Wins | Turns (max) | Losses |
|---|---|---|---|---|
| `random` | 2.73 / 12 | 0 % | 9.0 (19) | cannot_defend 192 |
| `greedy` (v1) | **5.41 / 12** | **0 %** | 12.9 (25) | cannot_defend 193 |

Training and validation agree (5.74 vs 5.46 on disjoint seed sets), so the weights are
not overfitted to the bench. By seat count, greedy clears 5.07 / 4.13 / 4.50 / 4.17 of
12 for 1–4 players.

### The targets in the plan were not met, and here is why

T7.2 asks for ≥ 8/12 castles; T7.3 asks for ≥ 15 % wins from a beam search. Measured:
5.4 castles and **zero wins**. Three things say this is a policy ceiling, not a
measurement mistake:

- **The arithmetic is brutal.** A three-seat table holds 44 cards in total and the twelve
  royals ask for 180 points of payment plus ~200 points of damage. It is winnable only by
  cancelling most payments with spades and recycling with hearts — a long-horizon plan,
  which one-ply scoring cannot see.
- **Simpler policies are worse, not better.** Biggest card 3.83, spade-first 3.19,
  kill-first 3.94 castles. The economy model is doing real work; it is just not enough.
- **Every loss is `cannot_defend`.** The table does not die of damage, it dies of a hand
  that cannot pay. That is a card-budget problem, and a better search is the only lever
  that moves it.

Not built, deliberately deferred to P7b: policy v2 (beam over play+payment macro-actions
with a node budget), and the imitation baseline from the `-training` corpus (T7.4). Both
need a state model that survives hidden information — the bot cannot simulate a tavern it
cannot see, so v2 has to search over *expected* draws, and that is a design of its own.

## The bot seat

`POST /api/rooms/{code}/bot` with `{"seat": 1, "on": true}`, host only.

- The manual route is for a seat the table has already lost (`away`, 90 s of silence); the
  store refuses a present seat with `seat_present`. **Bot fill** (on by default, D-62) is the
  automatic route: any seat silent for **60 s** is handed to a bot, and the owner takes it
  back by touching it. A bot never takes a turn from someone who is at the table.
- The seat is **labelled** for everyone, not just the host: the rail says `bot` in the
  state word and marks the chair with a dashed edge.
- It plays one action per **2.4 s** tick (D-68, amended by D-73), long enough for the
  table reveal (1.4 s hold + 0.42 s flight) plus a 0.5 s beat, so one bot move is one
  readable reveal.
- It stops when the host takes the seat back, when the table is cancelled, or when the
  session is deleted.

Tests: `TestBotSeatRequiresHost`, `TestBotSeatObservesOnlyOwnHand`,
`TestFromViewMatchesObserve`, smoke `coop.botSeatLabelled`.

## Gate

`bot bench` is a gate stage: 200 three-seat games run the **shipped** policy
(`human-engine`) and must clear **9.0 royals and 18 % wins**. On the suite the gate runs
it measures **9.58 royals and 33.5 % wins** (2026-11-18); the floor is well below the
measured rate (about five standard deviations at n=200). The number is a floor under the current policy, not a
target — it fails only when the policy has regressed.

## 0.9.0: the evaluator was wrong

The 0.8.9 policy was not weak because the weights were wrong; the model of the rules was.
Five mismatches each changed a play choice, and fixing them moved the shipped line from
4.0% to 8.4% before a single weight changed:

1. **A Jester play skipped step 4.** The engine hands the turn on after a Jester (no
   damage, no payment), but the evaluator charged it the whole bill, so the escape card
   looked like the most expensive play. `need = 0` for a Jester.
2. **Suit powers resolve at the play's TOTAL attack value.** `9♠+9♣` shields 18, not 9;
   `3♥+3♣` heals 6, not 3. The evaluator summed only the suited cards, undervaluing every
   mixed-rank pair with a spade or a heart.
3. **Effective shield.** `Observe` used `g.Shield` (raw banked points) instead of
   `g.effectiveShield()`; a spades royal's shield was credited as if it discounted the
   bill. `SnapshotFor` never filled `ClientState.EffectiveShield` (or `ImmunityCancelled`)
   at all, so the live bot seat saw zero shield and no unlocked suit while the bench saw
   both. Both are fixed, and
   `TestFromViewMatchesObserve` now pins bench/server parity.
4. **Hearts are capped by the discard.** A 9♥ into an empty discard returns zero, not
   nine; the evaluator now credits `min(attack, discard)`.
5. **Shield cannot discount more than the bill.** A 20-point shield into a 10-attack Jack
   was credited in full; the score and the payment now use `min(shield, attack)`.

Then a new heuristic, **`DrawCover`**: diamonds resolve *before* step 4, so the cards they
draw can pay the same turn's bill. The payment search cannot see them, so the evaluator
credits each card drawn into the acting seat's hand with 6 points of payment capacity
(the deck's average discard value). This is the single largest lever: it turns diamonds
from "damage cards" into the table's refill-and-pay engine.

The final weights (`human-engine`) were tuned over ~20 rounds on the fixed CAPCHECK suite,
with several independent proposal rounds and an audit pass reading the evaluator against
`engine/transition.go`. Only the `ShieldShadow` knob was removed: it was
declared and set by every persona but read by no code path.

| Policy | CAPCHECK 2k | FINAL7 | RAND7 | T7VAL | T7FINAL | 2 seats | 4 seats |
|---|---|---|---|---|---|---|---|
| greedy | 0.1% / 6.46 | — | — | — | — | — | — |
| human-bestof (old weights) | 8.4% / 8.27 | 7.6% / 8.28 | 7.4% / 8.23 | — | — | 3.4% / 7.67 | 2.0% / 7.04 |
| **human-engine** | **26.5% / 9.48** | **29.6% / 9.63** | **28.1% / 9.50** | **25.9% / 9.41** | **28.4% / 9.50** | **16.7% / 8.82** | **10.9% / 8.22** |

`human-bestof` remains in `Policies` as the pre-fix line, so the improvement is
reproducible rather than a claim. The beam/rollout teacher (BOT-PLAN M1–M3) is still
unbuilt; the policy is now strong enough that the next search slice should start from
`HumanEngine`, not from `Greedy`.

## P7b: measured, not met — and why (2026-11-17)

The policy now prices two things it ignored, both derived from the observation rather than
being constants: a shield is worth its value **on every remaining turn against that royal**
(estimated from the health left and a realistic damage-per-turn), and hearts are worth
several times their face value when the tavern is thin, because that is the only way cards
come back.

Measured after that change: **5.46/12 royals, 0 % wins** on 200 three-seat seeds (was
5.41). Four seats: 4.66/12.

A traced game shows the wall the policy hits:

```
t1  s0 royal JS hp20 atk10  hand 6 val32 tavern23   play
t3  s1 royal JH hp20 atk10  hand 5 val14 tavern23   play   <- hand already thin
t8  s1 royal JD hp20 atk10  hand 4 val11 tavern28   play
t9  s1 royal QS hp30 atk15  hand 2 val 1 tavern29   yield  -> cannot_defend
```

Three seats means a six-card hand worth roughly 39 points. The twelve royals ask for ~180
points of payment *and* ~200 points of damage out of a 44-card table. Payment is mandatory
every turn and the value paid is gone for ever; the only levers that make the arithmetic
close are spades (a shield discounts every remaining payment against that royal) and hearts
(they put the discard back under the tavern). Both are long-horizon: a one-ply policy can
see that a 20-point shield against a King saves ~60, but it cannot plan the three turns of
setup that make the King affordable.

Two things were measured and rejected as "just tune it": a spade-first policy clears 3.19/12
and a biggest-card-first policy 3.94/12, both worse than the economy policy. The ceiling of
this class of policy looks like ~6/12.

What P7b needs, in order: a search over **expected** draws (the bot cannot simulate a
tavern it cannot see, so the model has to be distributional), a two-turn horizon so a
shield can be valued against the payment plan it enables, and then the imitation baseline
(T7.4) to say how much of the gain is search and how much is the corpus.

## Personas and the operator's suit order (2026-11-18)

`Greedy` is the baseline heuristic, not a personality. Four personas plus a `human`
family now live in `bot/personas.go` and are selectable with
`coopbench -policy <name>`.

The operator's strategy: hearts are the weakest cards and should pay the attack first;
clubs next; spades are worth keeping for their shield; diamonds are the most important
cards; Aces are the last thing spent; combos are rare.

That maps to three new weight families:
- `PayHeart/PayClub/PaySpade/PayDiamond/PayAce` scale a card's cost when it is used to
  pay an attack, so the search protects diamonds and Aces.
- `HeartPlay` penalises spending a heart as damage.
- `ComboPenalty` prices multi-card plays (the `human-nocombos` variant).

Fixed-seed results (500 seeds per seat count, royals cleared out of 12):

| Seats | greedy | human | human-nocombos | human-tuned |
|---|---|---|---|---|
| 2 | 5.62 | 6.30 | 5.78 | **6.45** |
| 3 | 5.64 | 6.43 | 6.09 | **6.70** |
| 4 | 4.79 | 5.25 | 5.08 | **5.38** |

Ablation at 3 seats: payment order +0.29, heart-play penalty +0.18, draw weight +0.37;
combined +0.79 and the first wins. `human-tuned` (draw 11, stronger payment order and
heart penalty) is +1.03 on 2,000 fresh seeds (6.71 vs 5.68). The operator's strategy is
measurably better than the baseline; combos do help, so the no-combo instinct costs
about 0.3 royals.

The server runs `bot.DefaultPolicy()` = `HumanBestOf` since 0.8.5; the bench and the seat
use the same policy, so this section records how the policy got there, not a pending
switch.

### Jokers on diamond royals

The operator's second insight: a Joker is most useful on a diamond royal, because
cancelling its immunity lets the hand's diamonds draw. `HumanJoker` prices that:
a low base `Unlock` keeps the Joker off other suits, and `DiamondUnlock` adds value per
diamond point still in hand.

Measured on 2,000 fresh 3-seat seeds: **6.66** royals and 0.3% wins versus `human-tuned`
at **6.71** and 0.2%. The difference is noise. The reason is visible in the corpus:
when the bot actually plays a Joker on a diamond royal it holds only 1.1 diamonds on
average, and 27 of 91 such plays hold none. The precondition the insight needs — a
diamond hand waiting behind a diamond royal — is rare, and the Joker's tempo cost
offsets the draws when it is not. The preference is encoded for the games where it does
apply; it is not a lever that moves the bench yet.

### Castle rank odds and blocked-suit payment

The castle is a pinned ramp (Jacks, then Queens, then Kings; suits shuffled inside each
rank). After two jacks fall, the next royal is one of the two remaining jack suits with
a 50/50 chance, not a uniform 1-in-4. `ClientState.NextSuits` now carries that block's
public suit counts; `CastleSuits` carries the whole castle's composition. Neither
reveals the order.

Two experiments followed:

- **Holding against the next block** (`HumanHedge`, `HumanHedge2`: penalise keeping a
  suit whose next-royal probability is above 1/4, reward suit diversity): **6.60 and
  6.42** royals vs 6.73 for `human-tuned` on 2,000 seeds. Negative. Suit powers apply
  only on the turn a card is played, so they cannot be banked across royals; the next
  block changes *when* a power is worth using, not the card's value in hand. Exploiting
  it needs a multi-turn hold plan, not a one-step weight.
- **Blocked-suit payment** (`BlockedPay`): if the royal on the table blocks a suit, that
  suit's "keep it" premium is void, so it becomes ordinary payment. Diamonds against a
  diamond royal are payment cards, not the draw engine. This is the useful reading of
  the same idea, because it acts on the royal you can see.
  Result: **6.73** royals and **0.5% wins** vs 6.71 and 0.2% without it (2,000 seeds),
  4-seat 5.43 vs 5.38. Small but consistent and principled; kept in the human family.

Best current policy: `human-tuned` (6.73 royals, 0.5% wins on fresh 3-seat seeds) vs
greedy 5.68 and 0.1%. The remaining gap to a win is a planning problem, not a weight.

### Hearts to recover diamonds

The discard is public, so the bot now counts its diamonds (`DiscardDiamonds`) and prices
a hearts play by the expected diamonds it returns: `healCards × diamonds/discard`.
Because those cards go *under the tavern* and are drawn later, the value is discounted.

Measured on 2,000 fresh 3-seat seeds:

| HealDiamonds | Royals | Wins |
|---|---|---|
| 0 (before) | 6.73 | 0.5% |
| **2.5 (kept)** | **6.75** | **0.8%** |
| 6 | 6.63 | 0.9% |
| 12 | 6.39 | 0.4% |

The full price overvalues the play: the returned diamonds are random, they sit under the
tavern, and spending a hearts turn costs a card and tempo. A mild 2.5 is neutral-to-
positive and encodes the strategy; aggressive pricing loses royals. Best current line:
`human-tuned` 6.75 royals / 0.8% wins (3 seats), 6.55 (2 seats), 5.38 (4 seats), against
greedy 5.68 / 5.62 / 4.79.

### Complete play enumeration (the biggest single gain)

The play generator used to try only the first N cards of a same-rank group in hand order,
and only two Animal Companion partners (cheapest and strongest). The suits inside a combo
decide which powers are spent — a pair of clubs doubles, a pair of diamonds draws, a pair
of spades shields — so a prefix is not the same play as the best subset.

`plays` now enumerates every same-rank subset of 2–4 cards and every Ace+partner pair,
plus every single (an Animal Companion is legal alone as well as paired). Hands are at
most eight cards, so the search stays tiny.

Fixed-seed results, 2,000 fresh 3-seat seeds:

| Policy | Royals | Wins |
|---|---|---|
| greedy, before enumeration | 5.68 | 0.1% |
| greedy, full enumeration | 5.81 | 0.1% |
| human-tuned, before | 6.73 | 0.5% |
| **human-tuned, full enumeration** | **7.00** | **1.4%** |

2 seats: 6.96 royals / 1.0% wins; 4 seats: 5.51 / 0% (greedy 5.81/0.2% and 4.97/0%).
Best current line is `human-tuned`: **+1.19 royals and 14× the win rate of greedy** on
fresh 3-seat seeds. The lesson is that the candidate set, not the weights, was the
bottleneck for most of the remaining gap.

### Diamond draws are capped by every hand

The bot used to price a diamond play as "one draw per diamond card". The engine draws
`attack` cards round-robin from the tavern, starting at the acting seat, and skips any
seat at its hand limit. A big diamond into full hands draws little; a small diamond can
be worth more, and the big one is saved.

`HumanCapacity` models that loop exactly: capacity is `MaxHand - held` for every seat,
the acting seat's count after the played cards, and the tavern count caps the total.
`DrawCards` values each card actually drawn, `DrawSelf` the acting seat's share. The old
per-card `Draw` proxy is zeroed for this persona.

Sweep on 2,000 3-seat seeds (`HUMANCHECK`), capacity-aware draw price:

| DrawCards / DrawSelf | Royals | Wins |
|---|---|---|
| 0 (human-tuned) | 7.00 | 1.4% |
| 0.7 / 1.0 | 6.78 | 0.9% |
| 1.1 / 1.6 | 7.23 | 1.6% |
| 1.6 / 2.3 | 7.54 | 2.1% |
| **2.3 / 3.2** | **7.62** | **2.4%** |
| 3.2 / 4.2 | 7.59 | 1.9% |

Fresh-suite confirmation (`CAPCHECK`, 2,000 seeds): **7.53 royals / 3.0% wins** vs
greedy **5.77 / 0.0%**. 2 seats: 7.53 / 1.2% vs 5.81 / 0.2%. 4 seats: 5.81 / 0% vs
4.96 / 0%.

`human-capacity` is now the best policy: **+1.8 royals and ~30× the win rate of greedy**
on fresh seeds. The operator's intuition was the largest single gain after the play
enumeration: the draw is a capped resource, not a count of diamond cards.

### Exact kills and safe setup turns

Two more operator insights, both measured on the capacity-aware base:

- **Exact kill.** The engine puts the royal on top of the tavern only when the killing
  damage equals its remaining health. `ExactKill` prices that face card. Alone it moved
  3-seat tuning seeds from 7.62 to 7.74 royals and 2.4% to 2.6% wins; at 14 the win rate
  was 3.5%.
- **Safe setup turn.** When the royal's attack is fully shielded, killing it immediately
  throws away a turn that could draw cards and put discard cards back under the tavern.
  `DelayKill` subtracts from a killing play only while `incoming == 0`. Alone it was
  neutral (7.63 / 2.5%); combined with the exact-kill price it adds up.
- **Combined** (`HumanPlan`, exact 22, delay 45): **7.78 royals / 4.2% wins** on the
  tuning seeds, **7.70 / 3.3%** on fresh seeds. 2 seats 7.49 / 2.2%; 4 seats 6.15 / 0%
  (capacity alone 5.81 / 0%). Delaying past 45 was worse (7.70 / 3.3% at 60), and exact
  above 22 lost a little royals for win rate.

`human-plan` is the current best: **+1.93 royals over greedy (5.77) and 3.3% vs 0.0%
wins on fresh seeds**, with 4-seat royals up from 4.96 to 6.15. These are bench results and the shipped policy;
`TestBotIntervalIsWatchable` and the browser checks confirm the seat behaves the same way
at a table.

### Commandments that did not survive the bench

The operator's strategic list was tested against the plan policy on the same 2,000
3-seat seeds:

- **Protect the last high diamond / value diamonds kept** (`LastDiamond` 12,
  `DiamondRest` 0.8): **7.40 royals / 2.5% wins** vs plan 7.77 / 4.3%. Negative. The
  draw model already prices a diamond by the cards it actually draws; adding a second
  "keep it" premium double-counts and loses damage/tempo. Diamonds are valuable because
  they draw, not because they are diamonds.
- **Diminishing returns past six cards** (`DrawPast` 0.4 on own-hand draws): **7.77 /
  4.3%**, identical to plan. The capacity cap already catches the full-hand case; the
  extra soft cap changes nothing measurable.
- **Exact kill** and **safe setup turn** did survive (see above).

The remaining commandments — diamond Q/K burst, next-enemy anticipation, 5-combo
priority, heart-face caution, spades-first, parity — are either already represented
(exact damage, spade shadow, heart-play penalty) or need multi-turn search rather than a
one-step weight. They are the right material for a 2-ply/rollout teacher.

### The ten commandments in combination

All ten at once (`HumanCommandments`) scored **7.37 royals / 2.1% wins** on 2,000
3-seat seeds — worse than the plan policy (7.77 / 4.3%). The rejected levers drag the
rest down: the last-diamond guard and next-royal hedge cost tempo, and the heart-face
penalty is already covered by the heart-play price.

Keeping only the levers that survived their own test (`HumanBestOf`: exact kill, safe
setup, full enumeration, capacity draws, 5-combo value, spades first, parity, burst
against diamond Queens/Kings) gives the best result measured so far:

| Policy | Tuning (2k) | Fresh (2k) | 2 seats | 4 seats |
|---|---|---|---|---|
| greedy | 5.81 / 0.1% | 5.77 / 0.0% | 5.81 / 0.2% | 4.96 / 0.0% |
| human-plan | 7.77 / 4.3% | 7.70 / 3.3% | 7.49 / 2.2% | 6.15 / 0.0% |
| **human-bestof** | **7.88 / 4.3%** | **7.77 / 4.0%** | **7.79 / 2.6%** | **6.18 / 0.2%** |

`human-bestof` was the best line at the time: **+2.0 royals and 4.0% vs 0.0% wins over
greedy** on fresh 3-seat seeds, and the first wins at four seats. It was superseded by
`human-engine` in 0.9.0 (see the top of this file). The remaining commandments
(diamond Q/K burst planning, next-enemy identity, heart exceptions) are search
problems, not weights.
