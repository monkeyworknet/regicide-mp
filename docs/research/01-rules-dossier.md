# 01 — Rules Dossier: Regicide (solo mode) — normative implementation spec

Status: **normative draft v1** — this file is the spec the TDD rules tests are written against.
Scope: solo (1 player) Regicide, base game, as published by Badgers From Mars (2020/2nd ed.).
Multiplayer rules are included only where needed for forward compatibility.

---

## 0. Provenance, authority ranking, notation

### 0.1 Authority ranking used in this document

| Rank | Source | What it is |
|---|---|---|
| 1 (primary) | `docs/research/rules-raw.txt` = official rules PDF, `regicidegame.com/site_files/33132/upload_files/RegicideRulesA4.pdf?v=2` | Complete official rulebook text, 2 pages, layout-preserved. **All `[R]` citations refer to this.** |
| 2 (official clarifications) | BGG rules threads answered by Badgers From Mars / the designers | Not directly fetchable in this run (BGG returns HTTP 403 to our fetcher). Only reachable via a secondary aggregator — see rank 4. |
| 3 (reference implementations) | Board Game Arena `Gamehelpregicide`; the `regicide` Rust crate on docs.rs | Second-hand but *executable* readings of the rules; useful for edge cases the rulebook leaves open. Never authoritative. |
| 4 (secondary restatements) | `howto.cards`, `ludex.dev`, Zatu review, rulespal rulebook mirror | Restatements/aggregations. `rulespal` is a verbatim copy of the official text (useful cross-check). `ludex.dev` aggregates BGG rulings and labels some "Official"; it is AI-ish prose and contains at least one demonstrable error (see §8.0). |
| 5 (rejected) | `pagat.com/invented/regicide.html` | **Different game** (a 19th-c. trick-taking game also called Regicide). Do not use. |

### 0.2 Evidence tags used below

- `[R]` — directly stated in the official rulebook text.
- `[R-impl]` — strict logical consequence of two or more `[R]` statements (no interpretation added).
- `[C]` — community/official-forum ruling, transmitted via a secondary source (link given).
- `[I]` — my interpretation where the rulebook is silent or genuinely ambiguous.
- `[O]` — OPERATOR-DECISION: player-facing choice; needs an explicit call and belongs in the settings screen.

**No ruling in this document is invented.** Every `[C]` claim carries a URL; anything without a URL is `[I]`.

---

## 1. Card and deck data model

### 1.1 Card inventory — exactly 54 cards

| Group | Count | Notes |
|---|---:|---|
| Rank cards 2–10, four suits | 36 | Tavern deck. Attack = printed number. |
| Animal Companions (Ace of each suit) | 4 | Tavern deck. Attack/discard value = 1. |
| Jesters (Jokers) | 2 | Solo: **both set aside**, 0 in Tavern deck. |
| Royally-suited enemies: J×4, Q×4, K×4 | 12 | Castle deck. Become playable cards only if recruited. |
| **Total** | **54** | 52 standard + 2 jokers. |

`[R]` "COMPONENTS: 52-CARD DECK + 2 JESTERS"; setup text enumerates 2–10 + 4 ACs + N jesters.

**Uniqueness invariant (important):** there is exactly **one** card per (rank, suit) pair. Consequence: a number-combo (all cards same rank) can never contain two cards of the same suit — at most one per suit, max 4 cards. This kills several would-be ambiguities (double-clubs, double-spades in one play). `[R-impl]`

### 1.2 Values: when played vs when discarded

| Card | Attack value when *played* | Value when *discarded to cover damage* | Suit power applies? |
|---|---:|---:|---|
| Animal Companion (A) | 1 | 1 | Yes |
| 2–10 | printed number | printed number | Yes |
| Jack (recruited) | **10** | **10** | Yes |
| Queen (recruited) | **15** | **15** | Yes |
| King (recruited) | **20** | **20** | Yes |
| Jester / Joker | **0** | **0** | No — special power instead |

`[R]` "Jacks in hand count as a 10, Queens in hand count as a 15 and Kings in hand count as a 20. These values are applied when either playing them as an attack card or discarding them from hand to suffer damage. Their suit power is applied as normal when played."
`[R]` "Animal Companions (A) have a value of 1 and the Jester a value of 0 when discarded to cover damage."
`[R]` "The Jester has an attack value of 0."

Note the asymmetry that does **not** exist: a recruited Jack/Queen/King keeps its 10/15/20 for *both* uses. There is no separate "health" for a recruited royal — BGA states this explicitly ("Royals retain their attack value when players play them (J10, Q15, K20) and that is also the amount of damage they can take… they do not have double health") `[C]`.

### 1.3 Enemy stats

| Enemy | Attack (per turn damage) | Health (total damage to defeat) |
|---|---:|---:|
| Jack | 10 | 20 |
| Queen | 15 | 30 |
| King | 20 | 40 |

`[R]` (explicit table in Step 3). Enemy count = 12; order is all 4 Jacks, then all 4 Queens, then all 4 Kings. `[R]`

### 1.4 Deck construction

**Castle deck** (built face-down, bottom → top):
1. Shuffle the 4 Kings → face-down pile.
2. Shuffle the 4 Queens → face-down **on top of the Kings**.
3. Shuffle the 4 Jacks → face-down **on top of the Queens**.
4. Place in the centre; flip the top card face up = current enemy (always a Jack). `[R]`

So the *draw order* is: Jacks (4, shuffled), Queens (4, shuffled), Kings (4, shuffled). **Three independent shuffles** at setup (matters for seeded reproducibility — see Appendix C).

**Tavern deck** (draw pile): all 2–10 (36) + 4 Animal Companions + N Jesters, shuffled. `[R]`

| Players | Jesters in Tavern deck | Max hand size |
|---|---:|---:|
| **1 (our scope)** | **0** | **8** |
| 2 | 0 | 7 |
| 3 | 1 | 6 |
| 4 | 2 | 5 |

**Solo-specific deviations** `[R]`:
- Both Jesters are **set aside** (not in the Tavern deck) and are used as one-shot powers (§5.2).
- Tavern deck at start = 36 + 4 = **40 cards**.

**Discard pile**: shared, starts empty, face-up, beside the Tavern deck. `[R]`

### 1.5 Solo setup sequence (deterministic)

1. Shuffle Kings; shuffle Queens onto them; shuffle Jacks onto them → Castle deck (12, face-down).
2. Reveal top Castle card → current enemy = a Jack (face-up).
3. Shuffle 36 rank cards + 4 Aces → Tavern deck (40).
4. Jesters: 2 set aside face-up, `jestersRemaining = 2`, `jestersUsed = 0`.
5. Deal 8 cards to the player. Hand = 8, Tavern = 32, Discard = 0, Castle face-down = 11, enemy face-up = 1.
6. First player: solo = the human (`[R]`: "The player to have most recently committed regicide goes first"; solo therefore always starts with the player).
7. Turn 1 begins at Step 1 (no enemy state yet: `damageDealt = 0`, `shieldTotal = 0`, `immunityCancelled = false`).

### 1.6 Canonical types (language-neutral; Go-flavoured)

```
Suit    = C | D | H | S            // clubs, diamonds, hearts, spades
Rank    = A | 2..10 | J | Q | K
Kind    = Number | Companion | Royal | Jester

Card {                                 // immutable, one per physical card
  ID        string      // stable, e.g. "S10", "HJ", "CJ0"(jester1)
  Kind      Kind
  Suit      Suit | None              // None for Jester
  Rank      Rank | None
  PlayValue int                      // A=1, 2..10=n, J=10, Q=15, K=20, Jester=0
  DiscValue int                      // identical to PlayValue for every card
}

Play {                                 // one legal step-1 action
  Cards      []CardID                // 1 card, or Ace+one card, or 2-4 same-rank
  AttackTotal int                    // sum of PlayValue; the parameter for ALL suit powers
  SuitSet    set[Suit]               // distinct suits present
}

EnemyState {
  Card            Card               // one of the 12 royals
  BaseAttack      int                // 10 / 15 / 20 — NEVER mutated
  Health          int                // 20 / 30 / 40
  DamageDealt     int                // cumulative across all turns
  ShieldTotal     int                // unconditional sum of spade-play AttackTotals against THIS enemy
  ImmunityCancelled bool
  AttackArea      []CardID           // cards played against this enemy, not yet in discard
}

GameState {
  Castle      []Card                 // face-down remainder (top = last)
  Enemy       *EnemyState            // face-up current enemy, or nil after final win
  Tavern      []Card                 // top = last element
  Discard     []Card
  Hand        []Card
  Jesters     { Total, Remaining, Used }
  HandLimit   int                    // 8 (solo)
  Phase       Phase                  // §2
  TurnNo      int
  Status      Playing | Won | Lost{Reason}
}
```

### 1.7 Conservation invariant (assert in every test)

`len(Hand) + len(Tavern) + len(Discard) + len(Castle) + (1 if Enemy != nil) + len(Enemy.AttackArea) + JestersRemaining + JestersUsed == 54`

Solo start state: `8 + 32 + 0 + 11 + 1 + 0 + 2 + 0 = 54`. ✔

Second invariant: `len(Hand) <= HandLimit` at all times, and `len(Hand)` only changes via diamond draws, hearts (never), solo-jester refill, and discards.

---

## 2. The turn as a deterministic state machine

### 2.1 Phases

```
        ┌──────────────────────────────────────────────────────────────┐
        v                                                              │
 [S1 TURN_START] --(lose-check; jester window A)--> [S2 STEP1_AWAIT]   │
                                                        │              │
              play(cards) ──────────────────────────────┤ yield ───────┼──> [S6 STEP4_PRE]
                                                        v              │
                                              [S3 STEP2_POWERS]        │
                                                        │              │
                                                        v              │
                                              [S4 STEP3_DAMAGE]        │
                                                 │           │         │
                                        defeated │           │ not     │
                                                 v           v         │
                                     [S5 DEFEAT_RESOLVE]  [S6 STEP4_PRE]
                                                 │                     │
                                    (new turn S1)│                     v
                                                 └──────────> [S7 STEP4_DISCARD]
                                                                       │
                                                        [S8 TURN_END] <┘
                                                              │
                                                              └──> [S1 TURN_START]
```

### 2.2 Transition table

| ID | From | Trigger / precondition | Effect | To |
|---|---|---|---|---|
| T1 | S1 TURN_START | `len(Hand) == 0` and yield not legal | `Status = Lost{CannotAct}` | terminal |
| T2 | S1 | always | open **jester window A** (solo only, §5.2) | S2 |
| T3 | S2 STEP1_AWAIT | player plays a legal `Play` | move cards Hand→AttackArea; `AttackTotal` computed; remember `SuitSet`; no powers yet | S3 |
| T4 | S2 | player yields (legal only per §2.4) | nothing staged, no damage | S6 |
| T5 | S2 | player flips a solo jester (window A only) | §5.2 | S2 (window stays open) |
| T6 | S3 STEP2_POWERS | always, automatic | resolve suit powers in the fixed order of §3.1 | S4 |
| T7 | S4 STEP3_DAMAGE | always, automatic | `dmg = AttackTotal * (2 if clubs-active else 1)`; `DamageDealt += dmg` | S5 if `DamageDealt >= Health`, else S6 |
| T8 | S5 DEFEAT_RESOLVE | auto after T7 | run §2.3 (i)→(ii)→(iii) | if last King: `Status = Won` (terminal). Else **(iv)**: same player, new turn | S1 (new turn, Step 1) |
| T9 | S6 STEP4_PRE | incoming = `effectiveAttack` (§3.5); open **jester window B** (solo only) | — | S7 |
| T10 | S7 | `remaining == 0` | — | S8 |
| T11 | S7 | hand total < `remaining` | `Status = Lost{CannotDefend}` | terminal |
| T12 | S7 | player discards one card | card Hand→Discard; `remaining -= DiscValue`; if `remaining <= 0` → S8 (forced stop) | S7 / S8 |
| T13 | S8 TURN_END | always | next player clockwise (solo: same player), `TurnNo++` | S1 |

Notes:
- **T8 (iv) is not a normal turn pass**: the defeating player *skips Step 4* and *begins a new turn*. `[R]` "The player who has just defeated the enemy skips Step 4 and begins a new turn against the enemy just revealed, starting at Step 1." In solo this is the single most important tempo rule: every kill = a free turn (no enemy attack) and a fresh jester window A.
- **T7 doubles the whole play, not just the club card.** `[R]` example: "if a player plays the 3 of Diamonds, Spades and Clubs together, players will draw 9 cards, reduce the enemy's attack value by 9 and deal 18 damage" — total 9 → 18. And the AC example: 8♦+A♣ = 9 → 18 damage. So `damage = AttackTotal × 2` when an *active* club is present. Only one club can be present (uniqueness invariant).
- **T12 forces stop-at-threshold**: on the card that makes the discard total ≥ required, Step 4 ends immediately. `[R]` "discard cards … with a total value at least equal to the enemy's attack value" + `[C]` official ruling "Discard cards one at a time and stop as soon as their total meets or exceeds the attack" (BGG thread 3668137, via https://ludex.dev/regicide/faq/). See §8 #2.
- **`BaseAttack` is immutable.** Step 4 computes `incoming = max(0, BaseAttack − shieldActive)`; do **not** decrement the enemy's Attack field, or the shield will be applied twice on later turns.

### 2.3 Defeat resolution (Step 3 → Step 5), exact order

On `DamageDealt >= Health`:

1. **(i) Enemy card destination** `[R]`
   - if `DamageDealt == Health` **exactly** → enemy card goes **face-down on TOP of the Tavern deck** (it becomes the next card drawn, and can be played later as a 10/15/20).
   - else (`DamageDealt > Health`) → enemy card goes to the **discard pile**.
2. **(ii) All cards in `AttackArea`** → discard pile (these are the cards played against *this* enemy by *any* player, across *all* turns it survived). `[R]` "Place all cards played by players against the enemy in the discard pile." This also terminates all spade shields against it.
3. **(iii) Reveal the next Castle card** → new `EnemyState` with `DamageDealt=0`, `ShieldTotal=0`, `ImmunityCancelled=false`, empty `AttackArea`. `[R]`
   - If the Castle deck is now empty (i.e. the 12th enemy just died) → **the players win**; skip the reveal. `[R]` "The game ends when the players win by defeating the last King."
4. **(iv) Same player takes a new turn** at Step 1; Step 4 of the killing turn is skipped entirely (no damage, no discards). `[R]`

Overkill is **not** carried to the next enemy. `[R-impl]` (damage is compared against "the enemy's health"; each enemy's check starts from zero).

### 2.4 Yielding

`[R]` "During Step 1 it may be beneficial to yield instead of playing a card. To yield simply say 'Yield' and move directly to Step 4 (Suffer damage from the enemy), skipping Steps 2 and 3. A player may not yield if every other player has yielded on their last turn. For example, in a three player game, if two players have yielded in a row the third player may not yield."

- Yield plays no card, deals no damage, triggers no suit power.
- Yield still suffers the enemy's full effective attack (Step 4 is unchanged).
- Legality: not legal if *every other player* yielded on their immediately preceding turn(s). Model as `consecutiveYields < numPlayers - 1` for multiplayer.
- **Solo:** see §8 #1. Default in this spec: **yield is NOT legal in solo** (matches the literal reading "every other player" = empty set → vacuously true → all others have yielded; matches BGA's implementation and howto.cards' reading). Configurable.

### 2.5 Turn passing

- After a normal Step 4 → next player clockwise (solo: the same player again). `[R]`
- After a Jester (multiplayer) → the Jester's player **chooses** the next player; play then continues clockwise from that player. `[R]` + `[C]` (BGG thread 2801027 via https://ludex.dev/regicide/faq/: "Continue clockwise from the player selected by the Jester; intervening players are skipped for that turn order.")
- After a kill → the killer continues (§2.3 iv).

### 2.6 Win and lose conditions

**Win:** the 12th enemy (the last King) is defeated in Step 3. `[R]`

**Lose** — any of:
1. **Unpayable damage**: at Step 4 the player cannot discard cards whose total ≥ required attack ("If the player cannot discard enough cards to satisfy the damage, they die and all players lose the game"). `[R]` Operationally: `sum(DiscValue of hand) < remaining`.
2. **Cannot act**: at the start of a turn the player can neither play a card (empty hand) nor yield. `[R]` "The players also lose if any player is unable to play a card or yield on their turn."
   - Solo with default rules (no yield): an **empty hand at the start of Step 1 is an immediate loss**, regardless of the enemy's attack (even if it is 0).
3. Nothing else ends the game. Running out of Tavern cards is **not** a loss. `[R]`

Terminal statuses must be sticky: once `Won`/`Lost`, all actions are illegal.

---

## 3. Suit powers — precise resolution

### 3.1 Fixed resolution pipeline (this ordering is normative)

For a single `Play` with parameter `X = AttackTotal`:

| Order | Step | Power | Condition |
|---:|---|---|---|
| 1 | Step 2 | **Hearts** | `H ∈ SuitSet` and hearts-active |
| 2 | Step 2 | **Diamonds** | `D ∈ SuitSet` and diamonds-active |
| 3 | Step 3 | **Clubs** doubling flag | `C ∈ SuitSet` and clubs-active → `damage = X * 2` |
| 4 | Step 4 | **Spades** shield | `S ∈ SuitSet` → `ShieldTotal += X` (accumulated unconditionally, §6) |

`[R]` "Any time where both a Hearts power and Diamonds power are resolved together, resolve the Hearts healing before drawing with Diamonds." (stated twice: in COMBOS and in ANIMAL COMPANIONS). Clubs is explicitly a Step-3 effect and Spades an explicitly Step-4 effect, so they cannot interleave with Step 2. `[R]`

"Active" for a suit `s` means: `s != Enemy.Suit || Enemy.ImmunityCancelled` (§6).

**All suit powers are mandatory and cannot be skipped.** `[R]` "Suit powers are mandatory and cannot be skipped." There is no "decline power" action in the UI for any suit. The only thing that suppresses a power is enemy immunity.

### 3.2 Hearts — heal from the discard

`[R]` "Shuffle the discard pile then count out a number of cards facedown equal to the attack value played. Place them under the Tavern deck (no peeking!) then, return the discard pile to the table, faceup."

Formal:
```
n = min(X, len(Discard))
if n > 0:
    shuffle(Discard)          // uses the seeded RNG, consumes the stream
    moved = Discard[:n]       // top of the shuffled pile
    Tavern = moved ++ Tavern  // appended at the BOTTOM (drawn last)
    Discard = Discard[n:]     // returned face-up, still in shuffled order
```
- If `len(Discard) < X` → move **all** of them (`[I]`; corroborated by howto.cards: "count out as many cards as the attack value (or all of them, if there are fewer)").
- If `len(Discard) == 0` → no-op, and **no RNG consumption** (define it this way for deterministic tests).
- **Cards in `AttackArea` are NOT in the discard pile** and cannot be healed. `[R-impl]` — the rulebook puts them there only at defeat, step (ii). Stated explicitly by BGA ("Cards are only discarded once a Royal is defeated…") and howto.cards ("The cards lying in front of you against the current enemy are not on the discard pile yet."). `[C]`
- Order of the moved cards: unspecified `[I]`; recommended = preserve shuffled order, first-removed becomes shallowest (drawn soonest).
- The player may not look at the moved cards (they are face-down inside the deck); the *client* must not render their faces.

### 3.3 Diamonds — draw cards

`[R]` "The current player draws a card. The other players follow in clockwise order drawing one card at a time until a number of cards equal to the attack value played have been drawn. Players that have reached their maximum hand size are skipped. Players may never draw cards over their maximum hand size. There is no penalty for failing to draw cards from an empty Tavern deck."

Solo: the single player draws `min(X, HandLimit - len(Hand), len(Tavern))` cards.

Multiplayer (forward compat) — recommended algorithm (`[I]`, see §8 #4):
```
drawn = 0; seat = currentPlayer
while drawn < X:
    if all players at hand limit: break
    if Tavern empty: break
    if len(hand[seat]) < HandLimit: draw one to seat; drawn++
    seat = (seat+1) % numPlayers          // a full player is *skipped*, not counted
```
- `[C]` Official: the active player draws **first**, then the cycle continues clockwise **including the active player again** (so with X=5 and 4 players with room: P1,P2,P3,P4,P1). BGG thread 3374500 via https://ludex.dev/regicide/faq/.
- Empty Tavern mid-draw → draw what you can and **stop, no penalty**. `[R]`
- Never exceed `HandLimit`. `[R]`

### 3.4 Clubs — double damage

`[R]` "Double damage: During Step 3, damage dealt by clubs counts for double. E.g., The 8 of Clubs deals 16 damage."

- Applies to the **entire play's AttackTotal**, not just the club card's own number (`[R]`, two worked examples both with mixed suits).
- It is a boolean ×2, **not** ×2 per club card. Two club cards in one play is impossible in the standard deck (uniqueness invariant); if a custom deck allowed it, `[I]` recommends ×2 once. §8 #6.
- Not retroactive: if clubs were immune when played, no doubling ever, even after a Jester. `[R]` "clubs played prior to the Jester against a clubs enemy will not count for double."

### 3.5 Spades — shield

`[R]` "Shield against enemy attack: During Step 4, reduce the attack value of the current enemy by the attack value played. The shield effects of spades are cumulative for all spades played against this enemy by any player, and remain in effect until the enemy is defeated."

```
on play (any time, immune or not):   Enemy.ShieldTotal += X          // unconditional
at Step 4:  shieldActive = (Enemy.Suit != S) || Enemy.ImmunityCancelled
            incoming     = max(0, Enemy.BaseAttack - (shieldActive ? Enemy.ShieldTotal : 0))
```
- Cumulative across turns and (multiplayer) across players. `[R]`
- Applies in the **same turn** the spade is played (black powers "take effect in later steps"; Step 4 is later). `[R]`
- Persists until that enemy is defeated; resets for the next enemy (`ShieldTotal` lives on `EnemyState`). `[R]`
- **Floor at 0**: an enemy's attack can never require a negative discard; see §7 #1.
- Spades played *before* a Jester against a spades enemy still count, and begin shielding the moment immunity is cancelled. `[R]`

### 3.6 Combos

`[R]` "In Step 1, instead of playing a single card, players can combine cards together in sets of 2, 3 or 4 of the same number as long as the combined total of the cards played equals 10 or less. … When these cards are played together all suit powers are resolved at the total attack value."

**Exhaustive legality enumeration** (standard deck; `n` = number of cards, all same *rank*):

| Rank | n=2 | n=3 | n=4 |
|---|---|---|---|
| A | ✗ (Aces are Companions, not combos — but see pairing §3.7) | ✗ | ✗ |
| 2 | ✔ total 4 (6 suit-combos) | ✔ total 6 (4) | ✔ total 8 (1) |
| 3 | ✔ total 6 (6) | ✔ total 9 (4) | ✗ total 12 |
| 4 | ✔ total 8 (6) | ✗ total 12 | ✗ |
| 5 | ✔ total 10 (6) | ✗ total 15 | ✗ |
| 6–10 | ✗ total ≥ 12 | ✗ | ✗ |
| J/Q/K | ✗ total ≥ 20 | ✗ | ✗ |
| Jester | ✗ (played alone) | ✗ | ✗ |

So exactly **7 combo shapes** are legal: `22, 222, 2222, 33, 333, 44, 55` (numbers only in 2–5).
`[R]` independently lists them: "a pair of 2s, 3s, 4s, or 5s, triple 2s and 3s, or quadruple 2s."

Rules:
- Cards must be the **same printed number**, not the same suit. Different suits are normal (and forced by uniqueness).
- Every suit power present resolves at the **combined total** `X`.
- A combo may not include an Animal Companion, a Jester, or a royal together with a number. `[R]`
- Multi-player combos are played by **one** player from their own hand. `[R-impl]` (a play is "a card … from their hand"); BGG has an open thread "Can multiple players contribute cards to the combo?" — no official answer found; default = one player only.

### 3.7 Animal Companion pairing

`[R]` "In Step 1, Animal Companions can be played on their own, but may also be paired with one other card (except the Jester). Animal Companions count as 1 towards the attack total and their suit power is also applied. … Animal Companions can also be paired with one other Animal Companion. If you play an Animal Companion with another card of the same suit, you only apply the suit power once."

Legal shape: `[Ace]` alone, or `[Ace + X]` where X is any single non-Jester card (another Ace, a number, or a recruited royal). Never 3+ cards. Never `Ace + Jester`.

`X = 1 + PlayValue(X)`; both suits' powers resolve at `X`; if both cards share a suit, that suit resolves **once** at `X`.
Example `[R]`: 8♦ + A♣ → `X = 9` → draw 9 cards; damage 18 (clubs doubles the whole 9).

### 3.8 Power parameter rule (single sentence, covers all cases)

> Every suit power of the play uses **one** number: the play's total attack value `X` (sum of `PlayValue` over all cards in the play). Doubling applies to `X`; the shield adds `X`; the heal/draw count is `X`.

---

## 4. Animal Companions (Aces) — consolidated

| Property | Value | Evidence |
|---|---|---|
| Played alone | Legal; attack 1; suit power at 1 | `[R]` |
| Paired with one other card | Legal; +1 to that card's value; both powers at the total | `[R]` |
| Paired with another Ace | Legal; attack 2; both powers at 2 each; same suit → once | `[R]` |
| Paired with a Jester | **Illegal** | `[R]` "(except the Jester)" |
| Paired with a recruited Jack/Queen/King | Legal | `[R]` "one other card (except the Jester)" |
| In a number combo | **Illegal** — not as a member, and an Ace cannot be a combo on its own | `[R]` "Animal Companions cannot be added to a combo or played as a combo on their own" |
| Attack contribution | 1 | `[R]` |
| Discard value | 1 | `[R]` |
| Suit power | Always applies (subject to enemy immunity), at the play total | `[R]` |
| Same-suit pairing | Suit power applied **once** at the total | `[R]` |

---

## 5. The Jester

### 5.1 Normal (multiplayer) Jester — forward compatibility only

`[R]` "In Step 1 when playing a card the Jester may be played (always on its own). The Jester has an attack value of 0. The power of the Jester is that it negates the immunity of the enemy … After playing the Jester, skip steps 3 and 4 then, instead of play moving to the next player the player of the Jester chooses any player to go next."

- Played **alone** (cannot be paired, cannot be in a combo, cannot be combined with an Ace).
- Attack 0 → deals 0 damage → cannot defeat an enemy.
- Sets `Enemy.ImmunityCancelled = true` **permanently for that enemy**.
- Skips Steps 3 and 4 → no enemy attack, no `DamageDealt` change, `AttackArea` untouched.
- Step 2: nothing to resolve (a Jester has no suit).
- The Jester's player chooses the next player.
- Communication restriction is relaxed (irrelevant to a server).
- Card destination: `[I]` → discard pile immediately (recommended; see §8 #9).
- Does **not** count as "drawing" for anything.
- In solo, this card is never in hand; solo uses the flip power instead.

### 5.2 Solo Jester power (the only Jester mechanic in scope)

`[R]` "Set up the game as per usual but place the two Jesters to the side. … A Jester can be flipped to activate the following power: 'Discard your hand and refill to 8 cards — this does not count as drawing for the purpose of enemy diamond immunity.' Since you have two Jesters this can be done twice per game. Flipping the Jesters in this way does not cancel enemy immunity. You are allowed to use the Jester power a) at the start of Step 1 before you play a card or b) at the start of Step 4 before you have to take damage."

Formal:
```
legal if: JestersRemaining > 0 and Status == Playing
          and ( phase == STEP1_AWAIT and no card committed this turn
                or phase == STEP4_PRE )
effect:   JestersRemaining--; JestersUsed++
          Discard += Hand (all of it);  Hand = []
          draw min(HandLimit, len(Tavern)) cards from Tavern top → Hand
```
- **Not** a "draw" for enemy diamond immunity: it works against a diamond enemy. `[R]`
  *(Mechanically this only matters because the rulebook pre-empts the "drawing is blocked" reading; enemy immunity blocks the **diamond suit power**, not drawing per se. So there is nothing extra to implement beyond not routing this through the diamond power.)*
- Does **not** cancel immunity. `[R]`
- Works even when the enemy is a diamond enemy. `[R]` (explicitly the point of the clause)
- Twice per game maximum. `[R]`
- Timing windows: start of Step 1 (before playing a card) **or** start of Step 4 (before discarding). Not at any other moment. `[R]`
- A kill grants a *new turn* → a new Step 1 window opens (§2.3 iv).
- Refill is to **8** = `HandLimit`, never above. If the Tavern has fewer than the needed cards, draw all available. `[R-impl]` + `[I]`

### 5.3 Victory tiers (leaderboard relevant)

`[R]`
| Jesters used | Tier |
|---:|---|
| 0 | **Gold** |
| 1 | **Silver** |
| 2 | **Bronze** |

Only reached on a win. A loss has no tier. Jester count is a *use* count, not a "useful" count — flipping a Jester with an empty Tavern still spends it (`[I]`, §8 #13).

---

## 6. Enemy suit immunity

`[R]` "Each enemy is immune to the suit powers of cards played against them which match their suit. For example, players will not draw cards when a diamond is played against the Jack of Diamonds (however the number is still added to the damage total). The Jester can be played to cancel an enemy's immunity."

Model: `immunitySuit = Enemy.Suit`; `immunityCancelled bool`.

| Situation | Power fires? | Damage counts? |
|---|---|---|
| Card suit ≠ enemy suit | **Yes** | Yes |
| Card suit = enemy suit, no Jester used | **No** | **Yes** `[R]` |
| Card suit = enemy suit, after Jester (multiplayer) | **Yes** | Yes |
| Combo containing the enemy's suit among others | Only the matching-suit power is suppressed; the others fire at `X` | Yes, full `X` |
| Ace + card where the *Ace's* suit = enemy suit | Ace's power suppressed; other card's power still fires at `X` | Yes, full `X` |

### 6.1 The two Jester edge cases (verbatim-mandated)

`[R]` "If the Jester is played against a spades enemy, spades played prior to the Jester will begin reducing the attack value of the enemy however clubs played prior to the Jester against a clubs enemy will not count for double."

| Played before the Jester, against a **spades** enemy | Result |
|---|---|
| 6♠ (shield 6, suppressed at the time) | After the Jester: `ShieldTotal` = 6 **becomes active** → enemy attacks for `10/15/20 − 6` from then on. The shield was never applied while suppressed, so nothing is double-counted — it is one accumulator whose *activation* is gated. |

| Played before the Jester, against a **clubs** enemy | Result |
|---|---|
| 8♣ (would be 16) | Damage already resolved at face value 8 in Step 3; **no retroactive doubling**. |

Implementation consequence: `Clubs` doubling must be applied **eagerly in Step 3** (never stored as state), while `Spades` must be **stored unconditionally** and gated at Step 4. Getting these two backwards is the single most likely rules bug in this game. `[R-impl]`

---

## 7. Arithmetic and edge cases (numbered, normative)

1. **Enemy attack below 0** → clamp to 0. `effectiveAttack = max(0, BaseAttack − shieldActive)`. If 0, Step 4 completes with **zero** discards required, and no card may be discarded. `[I]` (rulebook silent; every reference implementation saturates — the Rust crate uses `enemy_attack().saturating_sub(self.shield)`; BGA/board practice is identical). §8 #3.
2. **0-value plays.** A Jester deals 0 damage (multiplayer only). A play that deals 0 damage cannot change `DamageDealt` and therefore cannot defeat anything. No Step-3 division/zero issues. In solo no 0-value card is ever in hand.
3. **Discarding to cover damage — card set.** Any cards; values A=1, 2–10=n, J=10, Q=15, K=20, Jester=0. A Jester discarded covers **0** and is therefore always wasted. Discarding is free-form (no suit/match constraint).
4. **Empty hand at Step 4 is fine** — but only if the requirement is 0. If `required > 0` and the hand is empty, `0 < required` → loss. `[R]` "It's ok to have an empty hand."
5. **Degenerate check** — the affordability test is `sum(DiscValue(Hand)) >= required`. Because all `DiscValue ∈ {0,1..10,15,20}` and the max affordable is the whole hand's sum, this is exact (no subset-sum subtlety: taking *everything* maximises the total, and stopping early is only ever a "stop at threshold" nicety, never a legality issue).
6. **Exact vs overpay in Step 4** — you **must stop** the instant the cumulative total reaches/exceeds the required amount. You may not dump extra cards. `[C]` (BGG 3668137). Note the consequence: you cannot use Step 4 to shed unwanted cards beyond the threshold.
7. **Exact vs overpay in Step 3 (damage)** — you cannot "hold back" damage; damage is applied in full. Overkill is wasted; only exactness (i) changes the enemy card's destination.
8. **Drawing with a full hand** — you are skipped; you never exceed `HandLimit`. `[R]` If the only draw target is full, no drawing happens.
9. **Tavern deck exhaustion** — "There is no penalty for failing to draw cards from an empty Tavern deck." `[R]` The deck is **never** reshuffled from the discard pile. `[R-impl]` — no such instruction exists anywhere in the rulebook; the only way cards return to the Tavern is the Hearts power (from *under* the deck) and an exact-kill royal (on *top* of the deck). Running out is a real, intended failure mode.
10. **Tavern empty + exact kill** — the ex-enemy becomes the entire Tavern deck (1 card). Next draw gets it.
11. **Tavern empty + hearts** — hearts still works if the *discard* is non-empty (hearts reads the discard, writes the Tavern). This is the standard late-game line: hearts refuels an empty Tavern.
12. **Multi-suit combo × immunity** — full damage `X` (doubled if a club is present and active); only the enemy-suit power is suppressed. Worked example for a clubs-enemy: `3♦ 3♠ 3♣` → draw 9, shield +9, **damage 9** (no doubling) — because the only club is immune. If the play were `3♦ 3♠ 3♥` against a clubs enemy → draw 9, shield +9, heal 9, damage 9 (no club present).
13. **Multi-suit combo × Jester** — if a Jester has cancelled immunity earlier in the fight, all the combo's powers fire including the previously-immune suit's.
14. **Two powers of the same suit in one play** — impossible for a combo (uniqueness); possible only as `Ace + card of the same suit`, where the rule is explicit: apply the power **once** at the total. `[R]`
15. **Hearts then Diamonds** — mandatory order. Edge that makes it matter: an empty (or nearly empty) Tavern + `X♥ + X♦` combo → hearts first refills the deck, then diamonds draws from it. Reversed order would draw nothing.
16. **Hearts + Clubs** — hearts resolves (Step 2), then damage doubles (Step 3). The cards hearts moves are *discarded* cards, never the current play's cards (those are in `AttackArea`).
17. **Hearts + defeat in the same turn** — hearts shuffles the discard *before* the enemy/played cards land in it. So the enemy card of the kill (when overkilled) goes to the discard pile *after* the shuffle and is not among the moved cards.
18. **Shield accounting never negative, never double-applied** — see §7 #1 and the warning in §2.2.
19. **Shield vs a new enemy** — `ShieldTotal` resets on reveal; a shield earned against the Jack does nothing against the Queen.
20. **Kill on the last card in hand** — legal; you get a free new turn, draw nothing unless a diamond refills you, and if the Tavern is empty you are very likely lost next turn. Not an immediate loss.
21. **Hand limit and recruited royals** — a recruited King occupies 1 hand slot and is worth 20 for both uses. Hand limit counts cards, not values.
22. **Jester flip with a full hand** — discards 8, draws 8. Legal, consumes the Jester. `[I]` §8 #13.
23. **Jester flip with an empty Tavern** — discards the hand, draws 0, hand is now 0 → if this was done at Step 1 you must then play a card and cannot → loss. Consumes the Jester. (UI should warn.)
23b. **Jester flip at Step 4 then still unable to pay** — the refill happens *before* the discard requirement is evaluated; if the new hand still can't cover it, you lose. `[R]` (window is "before you have to take damage").
24. **Empty hand + 0 required attack** — with default solo rules this cannot arise at Step 1 (T1 fires first). Under the "allow yield" variant it would create a legal infinite loop (§8 #1).
25. **Damage bookkeeping across a kill and re-kill in one turn** — impossible: a kill ends the turn's Step 3 and starts a *new* turn.
26. **Integer arithmetic only** — all values are integers; no rounding anywhere.

---

## 8. Ambiguities, FAQ-grade questions, and rulings

### 8.0 Conflicts between sources (recorded, not silently resolved)

| # | Conflict | Evidence | Resolution used here |
|---|---|---|---|
| C-1 | Where does an **exactly-killed** enemy go: **on top of** the Tavern deck, or **under** it? | Official PDF: "place it facedown **on top of** the Tavern deck instead" (https://www.regicidegame.com/site_files/33132/upload_files/RegicideRulesA4.pdf). BGA: "placed onto the **top** of the Tavern pile … ready to be drawn". howto.cards: "face down **on top of** the tavern deck … you have won it over to your side". Zatu review: "goes on top of the Tavern deck … you will draw it next turn". **Ludex FAQ says "beneath the Tavern"** (https://ludex.dev/regicide/scoring/ , and its FAQ entry citing BGG 2939763). | **On top.** Four sources including the primary rulebook agree; Ludex is a single outlier and its "beneath" reading also contradicts its own "ready to be drawn" logic. Flagged as a likely Ludex error. |
| C-2 | Can you yield in solo? | BGA: "A player may not Yield in solo". howto.cards: "we read the yielding rule literally … so you may **not** yield". The Rust `regicide` crate: `can_yield()` returns `true` for solo. | **No yield in solo** by default; operator-toggleable. §8 #1. |
| C-3 | Clubs doubling: whole play or just the club card? | Rulebook's two worked examples both double the whole total (9 → 18 twice). BGA: "Double the damage inflicted on the Royal." | **Whole play.** |
| C-4 | Is the discard-to-cover-damage permissive (overpay allowed)? | Rulebook: "total value at least equal to". BGG-official via Ludex: "No. Discard cards one at a time and stop as soon as their total meets or exceeds". | **Stop at threshold** (official), with an optional "allow overpay" setting. §8 #2. |

BGG itself returns HTTP 403 to this research environment, so every BGG-sourced ruling below is transmitted via the Ludex aggregator, which labels them "Official". **Validation limitation: I could not read the BGG threads directly.** Treat all `[C]` items as "reported official" rather than verified-official.

---

### 8.1 The numbered ambiguity list

Each entry: **Question** — *tightest defensible reading* — **Recommendation** — flag.

**#1 — Can you yield when playing solo?** `[O]`
- *Reading:* "A player may not yield if every other player has yielded on their last turn." With one player, the set of "other players" is empty, so the condition is vacuously satisfied → **you may never yield in solo**. This is also the only reading that keeps the loss condition "unable to play a card **or** yield" meaningful in solo: if yield were allowed, an empty hand against a 0-attack enemy would produce a legal infinite loop.
- *Evidence for the strict reading:* BGA explicitly ("A player may not Yield in solo"; its Game End section lists "or in solo mode" as a cannot-yield case) — https://en.doc.boardgamearena.com/Gamehelpregicide ; howto.cards reaches the same reading independently — https://howto.cards/en/regicide/ ; BGA/card-game practice.
- *Evidence against:* the Rust crate's `can_yield()` returns `true` when `is_solo()` — https://docs.rs/crate/regicide/latest/source/src/game/query.rs (a third-party implementation choice, not a ruling).
- *No official statement found stating either way in the rulebook.*
- **Recommendation:** default **yield disabled in solo**. Expose `settings.allowSoloYield = false` (optional rule) for players who want it; if enabled, also keep "empty hand at start of Step 1 = loss" so the 0-attack loop cannot happen.
- **FLAG: OPERATOR-DECISION.**

**#2 — May I keep discarding after my total already covers the attack?** `[C]`
- *Reading:* No. Discard one at a time; stop at the first card that brings the total to ≥ the requirement.
- *Official ruling:* reported in https://ludex.dev/regicide/faq/ ("No. Discard cards one at a time and stop as soon as their total meets or exceeds the attack", citing BGG thread 3668137 — thread not directly reachable from here).
- **Recommendation:** enforce auto-stop (Step 4 ends the moment the threshold is met). Provide `settings.allowOverpayDiscard = false` for groups that house-rule it.
- **FLAG: OPERATOR-DECISION** (low impact, but it changes hand contents and therefore the whole game).

**#3 — Can the enemy's attack go below 0?** `[I]`
- *Reading:* The rulebook says "reduce the attack value … by the attack value played" with no floor. Damage you must pay cannot be negative.
- **Recommendation:** clamp at 0 (`max(0, …)`). Never allow negative discards; when the requirement is 0, Step 4 completes with no discards and no interaction.
- *Corroborated by:* the Rust crate (`saturating_sub`), BGA play, universal card-game practice. No official statement found.
- Not flagged — this is a computational necessity, not a rule choice.

**#4 — Diamonds with a full player: is the skip a *lost draw* or a *re-allocated draw*?** `[I]`
- *Reading (tightest):* "until a number of cards equal to the attack value played **have been drawn**" + "Players that have reached their maximum hand size are **skipped**" → a full player is removed from the rotation and the remaining cards go to whoever still has room. Loop terminates when `X` cards have been drawn, the Tavern is empty, or everyone is full.
- *Alternative reading:* each rotation step is consumed whether or not a card was drawn (so a full player "wastes" a draw).
- **Recommendation:** the first (count actual cards drawn). In solo the two readings are identical, so v0 is unaffected.
- **FLAG: OPERATOR-DECISION (multiplayer only).**

**#5 — Does the active player draw first and then continue in the cycle (possibly drawing twice)?** `[C]`
- Yes — the active player draws first, then clockwise continues, including them again.
- *Official:* BGG 3374500 via https://ludex.dev/regicide/faq/. Also the rulebook's wording ("The current player draws a card. The other players follow in clockwise order…").
- **Recommendation:** implement exactly that. Solo: irrelevant (the single player just draws `X`).
- No flag (settled).

**#6 — Two club cards in one play: does damage double twice?** `[I]`
- *Reading:* "damage dealt by clubs counts for double" describes a state, not a stacking multiplier. Also **impossible** in the standard deck (one card per rank+suit; combos need the same rank → distinct suits; an Ace pair is the only same-suit pair and it uses the "apply once" rule).
- **Recommendation:** doubling is a boolean. Assert the uniqueness invariant in the engine so this can never be reached.
- No flag (unreachable in the real deck; documented for custom decks).

**#7 — Two spade cards in one play: does each subtract the full total?** `[I]`
- *Reading:* "all suit powers are resolved at the total attack value" would, read literally, make each spade card add the full total. Also **impossible** in the standard deck (same reasoning as #6); the Ace-pair path is explicitly "only apply the suit power once".
- **Recommendation:** each *distinct-suit* spade present adds `X` once; same-suit duplication adds once. Unreachable in the real deck.
- No flag.

**#8 — Are cards played against the current enemy part of the discard pile?** `[R-impl]` + `[C]`
- No. They sit in a staging area (the "attack area") until the enemy is defeated, then go to the discard pile per step (ii). Consequences: hearts cannot retrieve them; they are visible to all.
- *Evidence:* rulebook step (ii) only fires on defeat; BGA: "Cards are only discarded once a Royal is defeated, this is mainly because any Spades played previously or by other players will stack" — https://en.doc.boardgamearena.com/Gamehelpregicide ; howto.cards states it parenthetically — https://howto.cards/en/regicide/ .
- **Recommendation:** model explicitly as `Enemy.AttackArea`. No flag.

**#9 — Where does a (multiplayer) Jester card go after being played?** `[I]`
- *Reading:* it is a card played against the enemy → it belongs with the staged cards and ends in the discard pile at step (ii). But steps 3–4 are skipped, so nothing clears the staging area that turn.
- **Recommendation:** put the Jester straight into the discard pile when played (it deals no damage and is never part of a damage total, so it does not need to persist). Irrelevant in solo (Jesters are set aside).
- **FLAG: OPERATOR-DECISION (multiplayer only, low impact).**

**#10 — Does playing a Jester reset the yield counter / turn cycle?** `[I]`
- *Reading:* the Jester interrupts the turn order ("instead of play moving to the next player…"), so the "every other player yielded on their last turn" condition should be evaluated afresh.
- **Recommendation:** reset the consecutive-yield counter when a Jester is played. Multiplayer only.
- **FLAG: OPERATOR-DECISION (multiplayer only).**

**#11 — Can both solo Jesters be flipped in the same window?** `[I]`
- *Reading:* "You are allowed to use the Jester power a) at the start of Step 1 … or b) at the start of Step 4". Arguably one activation per window; nothing forbids two.
- **Recommendation:** allow repeated activations inside a window (each spends one Jester). It is strictly weaker than saving one for Step 4, so it grants no exploit (you discard 8, draw 8, discard those 8, draw 8 — no cherry-picking).
- **FLAG: OPERATOR-DECISION (low impact).**

**#12 — Does the "start of Step 1" Jester window reopen after a kill?** `[R-impl]`
- Yes: a kill begins a **new turn** at Step 1, so window (a) is available again.
- **Recommendation:** model the window as "phase == STEP1_AWAIT and no card committed this turn" so it reopens automatically.
- No flag.

**#13 — Does flipping a Jester with nothing to gain still count toward the medal tier?** `[I]`
- *Reading:* the tier counts *Jesters used/flipped*, not "useful flips". Any activation spends one.
- **Recommendation:** count any activation, even a no-op (empty Tavern / hand already 8). Show a confirmation because it is irreversible.
- **FLAG: OPERATOR-DECISION (trivially, but players will ask).**

**#14 — Does the solo Jester refill draw from the top of the Tavern deck, and what if there are fewer than 8?** `[R-impl]` + `[I]`
- Top of the deck; refill to `min(8, current + Tavern)`. Hand limit is never exceeded.
- **Recommendation:** as above; UI must not present the refill as a "draw" for any immunity purpose, and must not let the player keep any of the discarded cards.
- No flag.

**#15 — Is the damage "exactly equal to health" test on cumulative damage or on the killing blow?** `[R-impl]`
- Same thing: cumulative damage is the running total, and the check is `DamageDealt == Health` at the moment of the kill. Overshoot (e.g. 21 vs 20) forfeits the recruit.
- **Recommendation:** `exact = (DamageDealt == Health)` after adding this turn's damage; assert `DamageDealt >= Health`.
- No flag.

**#16 — Is overkill damage carried over to the next enemy?** `[I]`
- No. Each enemy's health is checked against damage dealt to *it*.
- **Recommendation:** reset `DamageDealt` to 0 on reveal.
- No flag.

**#17 — Does the spade shield apply in the same turn the spade is played?** `[R]`
- Yes — "powers from black suits take effect in later steps", and Step 4 is later in the same turn.
- **Recommendation:** compute `incoming` at Step 4 from `ShieldTotal` *including* this turn's spade.
- No flag.

**#18 — What happens if there is no legal play but the player has cards?** `[R-impl]`
- Impossible: any single card is always a legal Step-1 play. The "cannot play a card" loss can only occur with an empty hand.
- **Recommendation:** implement `legalPlays(hand) == []` iff `len(hand) == 0`.
- No flag.

**#19 — Can a player decline a mandatory suit power (e.g. to avoid drawing, or avoid a hearts shuffle)?** `[R]`
- No. "Suit powers are mandatory and cannot be skipped."
- **Recommendation:** no UI affordance to skip; only enemy immunity suppresses a power.
- No flag.

**#20 — Does the hearts power shuffle consume RNG / is it observable?** `[I]`
- The shuffle must be a real shuffle of the discard pile (it changes future draw order), executed by the server with the seeded PRNG.
- **Recommendation:** `if len(Discard) > 0 { rng.Shuffle(Discard) }`. Document the RNG consumption order (Appendix C) so seeded replays are byte-identical.
- **FLAG: OPERATOR-DECISION (only about "replay determinism" vs "shuffle algorithm freedom").**

**#21 — Order of the cards moved under the Tavern deck by hearts.** `[I]`
- Unspecified; they are face-down and unknown to the player.
- **Recommendation:** preserve the shuffled order (first removed → shallowest).
- No flag (unobservable to a correct client).

**#22 — Are hearts cards visible to the player?** `[R]`
- "no peeking!" → no.
- **Recommendation:** server must not send those card identities to the client (they move directly from discard into the deck; the discards were face-up public information in multiplayer, so a client *could* know them — but the order must not be revealed).
- No flag.

**#23 — Is a Jester discardable to cover damage?** `[R]`
- Yes, value 0 (so it never helps). Multiplayer only (solo Jesters are set aside).
- No flag.

**#24 — Multiplayer only: can several players contribute cards to one combo?** `[I]`
- No — a play comes from one player's hand. (BGG has an open thread with this title; no official answer reachable.)
- **Recommendation:** single-player-sourced plays only.
- No flag.

**#25 — What happens on the very last kill if the damage is exact?** `[R-impl]`
- The enemy would go on top of the Tavern deck, but the game is already won. State must transition to `Won` and stop (do not reveal a new castle card; the castle is empty).
- **Recommendation:** order the win check before the reveal in step (iii).
- No flag.

**#26 — Does the enemy's suit still matter after the Jester (multiplayer)?** `[R]`
- Only for immunity, which is now cancelled. Its attack/health are unchanged. A captured royal still has its own suit for future immunity checks.
- No flag.

**#27 — Does an exact-kill royal placed on top of the Tavern deck count as "in the Tavern deck" for hearts/`ShieldTotal` purposes?** `[I]`
- It is a normal deck card from then on; it can be drawn, discarded, healed, etc.
- No flag.

**#28 — Best/necessary behaviour when the Tavern deck is empty and the player must draw from diamonds.** `[R]`
- Draw nothing; the pillar "no penalty" applies. Do not reshuffle the discard.
- **Recommendation:** show a clear "tavern empty" state; never silently reshuffle.
- No flag.

**#29 — Is `HandLimit` fixed at 8 for the whole solo game?** `[R]`
- Yes (it is the hand size from the setup table). Optional house rule of 7/9 exists in the wild (howto.cards variants) — out of scope but easy to expose as a setting.
- No flag.

**#30 — Seeded runs and the leaderboard.** (Product rule, from `docs/DECISIONS.md` Part A #12, restated for the rules tests.) Seeded runs are excluded from the leaderboard; the seed is hidden during play and revealed at the end. Not a rules ambiguity — included so test authors don't conflate the two.

---

## Appendix A — Exhaustive legal-action enumeration (solo, per phase)

| Phase | Legal actions |
|---|---|
| TURN_START (empty hand) | none → `Lost{CannotAct}` |
| STEP1_AWAIT | (a) `flipJester` if `JestersRemaining > 0` (window A); (b) `play(cards)` for every legal `Play` from hand; (c) `yield` **only if** `settings.allowSoloYield` |
| STEP2_POWERS | automatic only |
| STEP3_DAMAGE | automatic only |
| DEFEAT_RESOLVE | automatic only |
| STEP4_PRE | (a) `flipJester` if `JestersRemaining > 0` (window B); (b) `confirmDamage` if required == 0 |
| STEP4_DISCARD | `discard(cardID)` for each card in hand — but only cards; end is forced at threshold |
| TURN_END | automatic |

`legalPlays(hand)` generator (the function to property-test):
```
for c in hand where c.Kind != Jester:                     emit {c}                    // singles
for a in hand where a.Kind == Companion:
    emit {a}                                                                          // Ace alone
    for b in hand where b.ID != a.ID and b.Kind != Jester:
        emit {a, b}                                                                   // Ace + one card
for rank in {2,3,4,5}:
    for combo in subsets(hand[rank], 2..4) where sum <= 10:
        emit combo                                                                    // same-number combos
```
Assertions:
- No play contains a Jester together with another card.
- No play mixes ranks.
- No play mixes a Companion with a combo.
- `AttackTotal == sum(PlayValue)`.
- Every play is `<= 4` cards.

---

## Appendix B — TDD test matrix (rules engine)

IDs are suggestions; each row is one test. Expected values are derived from §1–§7.

| ID | Test | Expected |
|---|---|---|
| R-001 | Solo setup sizes | Hand 8, Tavern 32, Discard 0, Castle 11 + 1 enemy, Jesters 2/0 |
| R-002 | Deck composition | Tavern contains 36 ranks + 4 Aces, no Jesters; Castle = 4J+4Q+4K |
| R-003 | Castle order | First 4 enemies all Jacks, next 4 Queens, last 4 Kings |
| R-004 | Conservation invariant | holds after every action in a random game |
| R-005 | Hand limit | `len(Hand) <= 8` after any action |
| R-006 | A played alone | attack 1, power at 1 |
| R-007 | A + 8♦ | AttackTotal 9, draw 9, damage 18 |
| R-008 | A + A | AttackTotal 2, both powers at 2 |
| R-009 | A of same suit + card | power applied once at the total |
| R-010 | A + Jester illegal | rejected |
| R-011 | A in a combo illegal | rejected |
| R-012 | Combo legality table | exactly the 7 shapes of §3.6 accepted; 2 of 6s, 3 of 4s, 4 of 3s, 5 of 2s (any rank>5) rejected |
| R-013 | 3♦ 3♠ 3♣ | draw 9, shield +9, damage 18 |
| R-014 | 2♠ 2♥ | heal 4 then draw 4 (order enforced) |
| R-015 | Hearts with fewer discards than X | moves all of them; discard becomes empty |
| R-016 | Hearts with empty discard | no-op, no RNG consumed |
| R-017 | Hearts moves cards to the BOTTOM | the moved cards are the last to be drawn |
| R-018 | Hearts cannot take AttackArea cards | staged cards stay staged |
| R-019 | Diamonds in solo | draws exactly X (limit/tavern permitting) |
| R-020 | Diamonds with full hand | draws 0 |
| R-021 | Diamonds with 2 cards in Tavern, X=7 | draws 2, no loss, no penalty |
| R-022 | Clubs doubling whole play | 8♣ alone = 16; 5♣+A♦ = 12 |
| R-023 | Spades cumulative across turns | two spades on separate turns sum |
| R-024 | Spades reduce this turn's Step 4 | enemy attacks for base − X |
| R-025 | Shield does not carry to the next enemy | incoming resets to base attack |
| R-026 | Shield floors at 0 | required discards = 0 |
| R-027 | Immunity suppresses power but not damage | vs J♦, 9♦ → damage 9, draw 0 |
| R-028 | Clubs immune → no doubling | vs J♣, 8♣ → damage 8 |
| R-029 | Spades immune → no shield | vs J♠, 8♠ → incoming 10 |
| R-030 | Damage ≥ health defeats | 20/20 and 21/20 both defeat |
| R-031 | Exact kill → top of Tavern | next draw is that card |
| R-032 | Overkill → discard pile | and not drawable |
| R-033 | AttackArea flushed at kill | all staged cards in discard |
| R-034 | Killer takes a new turn, skips Step 4 | no damage, phase back to Step 1, `TurnNo` advanced |
| R-035 | Overkill not carried | new enemy `DamageDealt == 0` |
| R-036 | Recruited Jack/Q/K values | 10/15/20 when played and when discarded |
| R-037 | Recruited royal's suit power | K♥ heals 20; J♠ shields 10 |
| R-038 | Recruited royal vs same-suit enemy | power suppressed |
| R-039 | Step 4 stop-at-threshold | cannot discard past the threshold |
| R-040 | Step 4 affordable-check | sum(hand values) < required → `Lost{CannotDefend}` |
| R-041 | Step 4 with required 0 | no discards, turn ends normally |
| R-042 | Step 4 empty hand, required 0 | legal, survives |
| R-043 | Empty hand at Step 1 | `Lost{CannotAct}` immediately |
| R-044 | Yield legal? (default solo) | rejected |
| R-045 | Yield (setting on) skips steps 2–3 | no damage dealt, powers not resolved, attack suffered |
| R-046 | Win on the 12th enemy | `Won`, no further castle reveal |
| R-047 | Jester flip discards whole hand and refills to 8 | hand 8, old hand in discard, `JestersRemaining` 1 |
| R-048 | Jester flip at Step 4 | new hand used to pay the damage |
| R-049 | Jester flip at any other phase | rejected |
| R-050 | Jester flip 3rd time | rejected |
| R-051 | Jester flip vs a diamond enemy | allowed, draws |
| R-052 | Jester flip does not cancel immunity | diamond power still blocked afterwards |
| R-053 | Jester flip with empty Tavern | hand becomes 0, Jester consumed |
| R-054 | Jester window opens after a kill | flip allowed at the new Step 1 |
| R-055 | Tier Gold/Silver/Bronze | 0/1/2 jesters used |
| R-056 | Seat/turn model param | `numPlayers=1` works; engine has no solo-only shortcuts |
| R-057 | Determinism | same seed + same action script → identical final state |
| R-058 | Hearts shuffle deterministic | same seed → same moved cards |
| R-059 | Property: no negative counters | `ShieldTotal, DamageDealt, JestersRemaining, remaining >= 0` |
| R-060 | Property: AI/random legal play always terminates or ends in a terminal status | no infinite loop with default rules |

---

## Appendix C — RNG and reproducibility notes (engine-adjacent, affects tests)

- **Three setup shuffles** in order: Kings, Queens, Jacks; then **one** for the Tavern deck. Fix the order or seeded games will differ between implementations.
- Hearts is the only *in-game* shuffle. It must draw from the same PRNG stream.
- Recommended PRNG: a seeded, well-defined generator (e.g. Go `math/rand` `*Rand` with a documented algorithm, or a small PCG/ChaCha implementation) so a seed reproduces a game exactly across runs. If you want cross-version stability, pin the algorithm rather than relying on `math/rand`'s internals (Go's global source changed over time; an explicit `rand.New(rand.NewSource(seed))` is stable *within* a Go version but its stream is not a documented contract).
- **Do not** shuffle in place on the client; keep the deck server-side (already decided in `docs/DECISIONS.md` Part A #10).
- Tests should assert on *invariants* and on *deterministic outcomes with a stub RNG* (e.g. an RNG that returns a fixed permutation) rather than on a specific shuffle output.

---

## Appendix D — Sources

**Kept**
- Official rules PDF (primary, in-repo as `docs/research/rules-raw.txt`) — https://www.regicidegame.com/site_files/33132/upload_files/RegicideRulesA4.pdf — every `[R]` claim.
- Board Game Arena help page — https://en.doc.boardgamearena.com/Gamehelpregicide — reference implementation; explicit "may not Yield in solo", AttackArea/discard timing, exact-kill on top of the deck.
- howto.cards full rules — https://howto.cards/en/regicide/ — independent restatement that matches the rulebook and *states its own readings* (hearts `min()`, hearts-before-diamonds, solo yield illegality, AttackArea note).
- Ludex FAQ (BGG ruling aggregator) — https://ludex.dev/regicide/faq/ — the only reachable carrier of BGG "Official" rulings (stop-at-threshold, diamond cycle including the active player, jester next-player, exact-kill). Treated as `[C]`.
- Ludex gameplay/scoring/solo pages — https://ludex.dev/regicide/gameplay/ , /scoring/ , /solo/ — same caveat; contains the one contradicting claim about exact-kill placement.
- rulespal rulebook mirror — https://www.rulespal.com/regicide/rulebook — verbatim copy of the official text, used to confirm the `rules-raw.txt` transcription.
- Zatu review — https://zatu.com/en-us/blogs/reviews/regicide-review — third independent "on top of the Tavern deck" confirmation.
- Rust crate `regicide` source — https://docs.rs/crate/regicide/latest/source/src/game/query.rs — opposing solo-yield choice + `saturating_sub` shield model; useful cross-check only.

**Rejected / deprioritized**
- https://www.pagat.com/invented/regicide.html — different game entirely.
- wikihow, gamerules.com, boardgamearchive, rulespal FAQ, reddit threads, TV Tropes — redundant restatements or unverifiable anecdotes; used only as search discovery.
- BGG threads (2939763, 3374500, 3668137, 2801027, 3311969, 2500663) — all return HTTP 403 to this fetcher; referenced only via Ludex.
