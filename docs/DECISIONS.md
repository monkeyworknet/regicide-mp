# DECISIONS — project decisions, ADRs, and standing rulings

This file is the **binding record** of scope decisions. `docs/PLAN.md` may never contradict this
file — if it does, this file wins and the plan is the bug.

Two parts:
- **Part A** — the 15 kickoff questions that fixed the scope of v0, each with the answer that bound it.
- **Part B** — ADRs: decisions taken while researching and building, including the rules rulings the
  engine depends on. Numbered `ADR-001…`, append-only.

Part A and Part C quote questions and answers from the project's planning sessions. Where an entry
names the document it came from, that document is an internal working record and is not part of this
publication; the decision it produced is.

---

## Part A — Kickoff questions (operator answered 2026-11-15)

Note appended to the kickoff answers:

> playing cards modern A4.pdf : This is a card set, its standard playing cards with blank area's in the
> middle that you can insert rules into I wouldn't want to put faces in there I'd want that to be for
> text explinations of how that card works in the game.

| # | Question (verbatim from the kickoff review) | Operator answer | Binding meaning |
|---|---|---|---|
| 1 | Stack: **Go stdlib `net/http` (strict GOPATH, zero deps) + vanilla JS, no build step**? — GOPATH+no-modules means no SQLite driver, so a mutex-guarded JSON store with atomic rename for leaderboard + sessions. | default | Go stdlib server, strict GOPATH mode, vanilla JS, zero external deps, JSON file store. |
| 2 | Hosting target: local dev only, or the home server / a real domain? Docker wanted? | default, **port 8123** | Self-hosted binary on port 8123; no Docker; no TLS assumed. |
| 3 | Repo layout: `main` as the git repo + worktree branch for the build, `.gitignore` the 61 MB `artifacts/` (commit only derived assets)? | default | Repo `main` clean; work in worktrees; `artifacts/` never committed; derived assets committed. |
| 4 | Multiplayer shape you ultimately want (online co-op rooms via WebSocket, or hot-seat)? | default — **online co-op room** | Engine must be seat-agnostic from day one; transport later becomes WebSocket rooms, no engine rewrite. |
| 5 | Which official rules go in **Settings** vs always-on? (proposed list: solo Jester toggle, exact-damage rule, confirm-turn, damage preview, animation speed, sound) | default | Always-on: combos, suit powers, immunity, Jester rules, mandatory powers. Settings: the proposed list. |
| 6 | Solo tiers: Bronze/Silver/Gold tracked per win by Jesters used, any win counts, listed under its tier? | default | Three tier lists; tier from Jesters used (0/1/2). |
| 7 | Pokémon: crop the print-and-play sheets into real card images? | default | Full extraction of the supplied card images, not a CSS imitation. |
| 8 | Modern theme: the PDF is a blank template — build clean SVG/CSS card faces? | **"the template has the cards suit and number etc in the corner already, you should only have to build the text in the middle"** | Reproduce the template's corner index pips; author **only** the middle rules text. Verified: the template is exactly the 54-card Regicide deck, rank embossed inside the pip. |
| 9 | UI art direction for the shell | default — dark felt shell, cards stay white/minimal | Dark felt palette; card faces stay white/minimal. |
| 10 | Anti-cheat: server-authoritative game state vs client-side with server checks? | default — server-authoritative | Server owns deck, seed, PRNG, legality; client sends actions only. |
| 11 | Names: free-text nickname on win, no accounts, filtered, top-N per tier? | default | Nickname only, no accounts, filtered, top-N per tier. |
| 12 | Seeds: input at launch, `?seed=` sharing, copy at end, seeded runs excluded? | **yes — but the user must not see the seed they are playing if they chose a random seed, until the end of the game** | Seed hidden during a random run; revealed at game end; `?seed=` supported; seeded/custom runs are leaderboard-ineligible. |
| 13 | Missing SFX: synthesise simple ones ourselves? | default — **be creative with the sounds you have** | Reuse the supplied 20, synthesise the gaps, feel free to pitch/layer. |
| 14 | Test depth: unit + property + API + headless UI smoke + gate script, engine ≥ 90 % coverage? | default | As stated. |
| 15 | Build order: vertical slice first (modern theme, full solo rules, sound, leaderboard), then Pokémon, then polish? | default | As stated. |

---

## Part B — ADRs

### ADR-001 — Server-authoritative engine, client is a renderer
**Status:** accepted (operator answer #10).
`Transition(state, action) → (state, events, error)` is pure and lives on the server. The client holds a
sanitised projection and animates from `events`. Rationale: leaderboard integrity, hidden seed, no
desync class of bugs, and co-op later needs it anyway.

### ADR-002 — Seat-agnostic state model from day one
**Status:** accepted (kickoff answer #4, with the co-op path structured in from the start).
State carries `Players []Player` (each with a hand), `ActiveSeat int`, `ConsecutiveYields int`, and the
action set includes `ChooseNextSeat{}`. v0 runs with exactly one seat. Rationale: retrofitting seats
touches every rule and every test.

### ADR-003 — Deterministic RNG via ChaCha8 + explicit Fisher–Yates
**Status:** accepted (research 03 §2, rules dossier Appendix C).
Seed string is normalised (Crockford Base32, 12 chars, 60 bits) then SHA-256'd into
`rand.NewChaCha8(...)`. We implement Fisher–Yates ourselves (`for i := n-1; i > 0; i--; j := int(r.Uint64() % uint64(i+1))`)
so stream consumption is pinned and testable. Setup shuffle order is fixed: Kings, Queens, Jacks, Tavern;
hearts is the only in-game shuffle. Rationale: `math/rand` v1 internals are not a contract; ChaCha8 is.

### ADR-004 — JSON files with atomic rename, not SQLite
**Status:** accepted (operator answer #1).
No external deps in GOPATH mode ⇒ no SQLite driver. Session snapshots are per-session JSON files written
to a temp file then `os.Rename`d under a process mutex; the leaderboard is one JSON file with the same
discipline and a `schemaVersion`. Rationale: durability and zero deps for a single-process self-host.

### ADR-005 — Two themes share one card component
**Status:** accepted (operator answers #7, #8).
`data-theme` switches the inner renderer; layout, counters and interactions are shared. The modern theme
reproduces the supplied template's corner pips and frame, with authored middle text; the Pokémon theme
draws extracted card images with HP/damage as overlays (never baked into the image).

### ADR-006 — Rules rulings locked for v0
**Status:** accepted (rules dossier §3, §7, §8; see `docs/research/01-rules-dossier.md`).
1. Played cards live in an **attack area**, not the discard pile, until the enemy dies.
2. Hearts resolves **before** diamonds when a play has both.
3. One parameter per play: the total attack value `X`. Clubs double `X` (boolean); spades add `X` to the
   shield **unconditionally** (immunity only gates the step-4 application); hearts/diamonds use `X`.
4. **Immunity suppresses the matching suit's power only**, never the damage total.
5. Shield is per enemy, cumulative across turns, resets on reveal, and the incoming attack floors at 0.
6. **Exact kill** ⇒ enemy goes face-down **on top of the tavern deck**; overkill ⇒ discard pile. Damage
   counters reset per enemy and never carry over.
7. Defeat cascade: (i) enemy away, (ii) attack area → discard, (iii) reveal next castle card, (iv) killer
   starts a **new turn** at step 1 and skips step 4. The win check happens **before** any reveal.
8. Step-4 payment **stops at the threshold** (no dumping extra cards) — `allowOverpayDiscard` off by
   default, and when on it requires an explicit `EndPayment` action.
9. **Empty hand at step 1:** order is *window A (Jester flip) → cannot-act check*. A flip can rescue an
   otherwise empty hand; if the hand is still empty after window A the result is `Lost{CannotAct}`,
   regardless of settings. **Operator delta D-1 is closed (default for all):** if the operator ever prefers
   "immediate loss", the check moves ahead of window A — one-line change, one test.
10. The tavern deck is **never** reshuffled from the discard; running out mid-draw is penalty-free.
11. **Solo yielding is disabled by default** (BGA + howto.cards read the rule as "never in solo"),
    `allowSoloYield` off. When enabled, a yield that would do nothing (required damage already 0) is
    illegal, which keeps termination.
12. Solo Jester flip: window A (start of step 1) and window B (start of step 4), reopens after every
    kill, spends one Jester per activation (including no-op flips), refills to
    `min(HandLimit, len(Tavern))` from the top, never counts as a diamond draw, never cancels immunity.
13. Tier from Jesters used: 0 Gold, 1 Silver, 2 Bronze; only on a win.

### ADR-007 — Asset licences and their consequences
**Status:** accepted (licence texts read in `artifacts/`).
- Dicier: CC BY 4.0 ⇒ credit line required in-app. (The subset woff2 plan was withdrawn by
  D-12; no webfont is bundled. This line is kept as the licence record.)
- CrispCards sampler: embedding allowed, pack redistribution forbidden ⇒ commit only the files we use.
- Pokémon RageIced sheets: personal/non-commercial, "freely distribute unmodified"; cropping and
  re-encoding is a modification ⇒ the theme ships credited and for non-commercial fan use only,
  never sold, and it is the first thing to remove if a rights holder objects.
- Regicide rules: © Badgers from Mars ⇒ credit + non-commercial framing.
`docs/CREDITS.md` holds the provenance table; a gate test asserts every committed asset appears there.

### ADR-008 — Verification substrate before Phase 0
**Status:** accepted (zero-context review findings 2, 6, 10).
UI tests use a **self-driving harness**: `/?test=smoke` runs a scripted scenario in the page against the
real API, writes assertions as JSON into `#result`, and `gate.sh` reads it with
`chromium --headless --virtual-time-budget=8000 --dump-dom`. Screenshots for the two viewports come from
`--screenshot` in the same harness. Engine positions are built by scenario helpers in the Go tests
(`src/regicide/engine/*_test.go`); the `<R-ID>.json` fixture plan was not used. The only
file fixture is `src/regicide/engine/testdata/golden-deal.json`. `docs/rules-coverage.md` is the rulebook-clause →
R-ID ledger, and `gate.sh` asserts every `R-0xx` in the dossier matrix appears both in that ledger and in
a Go test name.

### ADR-009 — Session resume contract
**Status:** accepted (zero-context review finding 11).
`localStorage["regicide.sessionId"]` is the client's source of truth; `?session=<id>` overrides it once
and is then written to storage. `GET /api/sessions/{id}` = 200 renders, **404 ⇒ clear storage and show the
new-game screen silently** (24 h idle GC and restarts both surface as 404). `?seed=<seed>` only prefills
the seed input; the seed of a *random* run never appears in the URL before the game ends.

---

## Part C — Delta decisions (operator, 2026-11-15)

All six deltas from `docs/PLAN.md` §11 were answered **"default for all"**. They are now binding, not open.

| ID | Question | Binding answer |
|---|---|---|
| D-1 | Empty hand at start of step 1 with Jesters remaining | **Window A (Jester flip) runs first**, then the cannot-act check. `Lost{CannotAct}` only if the hand is still empty after window A. |
| D-2 | `allowSoloYield` on: a yield with required damage already 0 | **Illegal** — prevents the zero-damage yield stalemate. Empty hand still loses. |
| D-3 | `allowOverpayDiscard` on: explicit end-of-payment | **Yes**, `EndPayment{}` action, setting off by default. |
| D-4 | Leaderboard shape | **One global board**, theme shown per entry. |
| D-5 | Name entry | **Prompt at win only**; no persistent player name. |
| D-6 | Pokémon assets | **Committed** under the ADR-007 constraints. |

Consequence: PLAN §11 is now a historical record; §3.10 / ADR-006 §9 ordering (window A → cannot-act) is final.

## Round 4–5 decisions (2026-11-15)

### D-13 Card sets: four, not eight
- **Status**: Accepted (superseded by D-72: four sets, Modern retired)
- **Context**: eight sets shipped; half were never chosen in play, and each costs assets, a gate stage and QA surface.
- **Decision**: modern, simple, pokémon, 8bit. The rest are deleted from git; every source pack stays in `artifacts/`.
- **Consequence**: one text-bearing set (modern) exercises the blank-middle text path; 8bit keeps `pixelated` scaling.

### D-14 A tap selects; the peek strip does the reading (phone)
- **Status**: Accepted (supersedes the round-4 "tap opens a read-only sheet")
- **Context**: tap → sheet → "Select for play" → close → Play was four steps for the most common action.
- **Decision**: tap = select/deselect; the selected cards appear at 96 px with their text in a strip above the action bar; long-press (and right-click) opens the full sheet; Play executes.
- **Consequence**: two taps per play; the read-only sheet is only for cards the player is not picking. In step 4 the strip's action reads "Cancel forfeit".

### D-15 Choosing a card set chooses its card back
- **Status**: Accepted (the Jester half is extended to a pair by D-72)
- **Context**: a mismatched back looks unfinished, and the operator asked for a sensible default per set.
- **Decision**: the registry emits `defaultBack` per set (modern→black, simple→red, pokémon→pokémon, 8bit→8bit), emitted only when the file exists. The dropdown still allows any mix.
- **Consequence**: a stale saved back falls back to the set's own back, not to an arbitrary entry.

### D-16 Worktrees and a review gate before merge
- **Status**: Accepted (operator instruction)
- **Decision**: every change happens in a worktree off `main`; a branch merges only when `scripts/gate.sh` is green **and** a review with no stake in the change has read the branch diff (findings fixed or deferred with a reason, recorded in the merge commit), verified at 390 px and 1440 px.
- **Consequence**: slower single changes, far fewer regressions; `docs/PROCESS.md` is the checklist.

### D-17 Local hosting, permanently
- **Status**: Accepted
- **Context**: Cloudflare was evaluated in round 4 (static assets free; a JS/Durable-Objects port free; the Go binary needs Containers at $5–12/mo).
- **Decision**: keep the LAN box. The evaluation document was removed; `-trust-proxy` remains because it is correct for any reverse proxy.

## Round 6 decisions (2026-11-15)

### D-18 The enemy panel reads from the enemy's side
- **Status**: Accepted
- **Context**: four counters mixed two viewpoints — health (the enemy's) and damage (ours), shield (ours) and incoming (the enemy's). On a phone that was four boxes for two facts, and "damage 4, shield 3, incoming 7" made the player do arithmetic to answer "will I die".
- **Decision**: two counters, both counting **down from the enemy's own numbers**: `health left / total` with a progress bar, and `attack landing / base attack` with a `♠ n shielded` note when a shield is up. Damage is the bar, not a box.
- **Consequence**: 20/20 and 10/10 at the start, then 17/20 and 7/10 after a shielded hit — one comparison instead of three. `CardView.Attack` carries the base attack so the client never derives it.

### D-19 Payment counts down
- **Status**: Accepted
- **Context**: step 4 showed `paid/required`, which grows; the player needs "how much is still owed, and what does this card leave me".
- **Decision**: the step hint and the play button both show the remaining amount (`Pay 7 more`, `Discard 6 · 4 left`), and the button shows the remainder *after* the picked card. `Discard 6 · 4 left` on the button, `Discard 6♣ pays 6 — 4 left` in the hint.
- **Consequence**: the number that matters never needs subtracting.

### D-20 Pure UI helpers live in their own module and are unit-tested in node
- **Status**: Accepted
- **Context**: `app.js` cannot be imported outside a browser, so every client behaviour was only covered by the self-driving smoke harness.
- **Decision**: pure functions (`formatDuration`, `discardValueOf`, `paymentLeft`, `paymentAfter`) moved to `web/format.js`; `node --test src/test/js/*.test.mjs` covers them plus the registry and card helpers, and runs as a gate stage. `scripts/install-hooks.sh` installs a pre-commit hook with the fast half of the gate.
- **Consequence**: a broken helper fails in a second instead of after a chromium boot; the browser and the tests import the same file, so there is no build step and no duplicate implementation.

### D-21 Server-side bounds are explicit constants with tests
- **Status**: Accepted
- **Context**: three unbounded structures (leaderboard entries, rate-limiter buckets, the per-session idempotency cache) could grow without limit; the theme whitelist duplicated the generated registry.
- **Decision**: `maxBoardPerTier = 200`, `idleBucketTTL = 10m` with a sweep on new keys, `maxCachedActions = 64` (oldest evicted), and the theme whitelist parsed once from the embedded `themes.json`.
- **Consequence**: adding a card set needs no Go change; abuse cannot grow a map; each bound has a test that fails if it is removed.

### D-22 Rotating screen copy lives in `web/voice.js`
- **Status**: Accepted
- **Context**: the menu and the end sheet said the same thing every run, and the wording read as fantasy boilerplate ("twelve royals stand between you and the dawn", "the tavern deck does not forgive"). The two lines that carry numbers were also prose, so the numbers were easy to miss.
- **Decision**: four five-line pools — headlines, ledes, victory titles, loss headings — live in `web/voice.js`. The setup screen draws one headline and one lede every time it is shown (launcher-tagline rotation), and the end sheet draws one result heading (victory or loss) once per game. The two number lines are fixed templates: `Twelve down in 34 turns. Two Jesters spent.` and `Short by nine.` (the shortfall quoted in words, computed from `step4Required - step4Paid - hand value`, falling back to the plain loss line when a player simply ran out of cards). `index.html` keeps one pool line as its no-JS fallback.
- **Consequence**: changing the copy is a data-only edit in one file; the pools and both templates are unit-tested in node (`src/test/js/voice.test.mjs`), including "no victory title contains the word victory" because it renders directly above `GOLD VICTORY`.

### D-23 A phone starts on a set it can read
- **Status**: Accepted
- **Context**: after round 7 the phone hand was legible, but a 58 px card is still a poor window onto a 500 × 700 painted face. Pokémon art packs a name, an HP bar, two moves and their numbers into that space; the operator put it plainly: "modern and pokemon should warn on mobile that they are small, and default to simple or 8bit, the user can still override."
- **Decision**: three rules.
  1. `phoneThemeDefault()` runs before the first paint. If the viewport is ≤ 559 px **and** the stored theme is `modern` or `pokemon` **and** the player has never picked a set on purpose (`prefs.themeExplicit`), the run starts on Simple, with Simple's own back.
  2. A deliberate choice always wins: tapping a tile on the setup screen or in Settings (or arriving with `?theme=`) sets `themeExplicit`, and the default never fires again. The preference is stored in the same `regicide.prefs` document as everything else.
  2b. **A viewport default is never persisted.** The switch remembers the stored theme and back (`themeStored` / `backStored`) and `savePrefs()` writes those instead, so a phone that silently moved off Modern does not erase a choice made on a desk — resize the same browser and Modern is back.
  3. When a phone *is* on a detailed set, `#theme-note` says so — `Pokémon art is detailed — small on a phone. Use Simple` — with a one-tap switch. It never blocks the run and never appears on a desk.
- **Consequence**: a phone user gets readable cards without a decision, and an enthusiast gets the set they asked for after one tap, still warned. No new rule for the server: this is presentation only.

### D-24 Print nothing on a face that already prints it
- **Status**: Accepted
- **Context**: round 7 answered the unreadable hand by printing our own corner index and a big centre rank on every card. The operator rejected it: "don't add numbers etc to the corners because all the decks already have them… the only deck you need to add text on is the modern cards, and that's just writing out what the power is in the middle." Round 7's overlays were doubling an index the art already carries, and the big centre mark was shouting over art that explains itself.
- **Decision**: no overlays, anywhere. Simple, 8bit and Pokémon are pure art on a phone — their printed index is already huge. The one set with a blank middle (Modern) prints, on a phone only, a two-character ref (`9C`, `10H`, `AS`) above one short line: `Clubs double damage`, `Spades shield`, `Hearts shuffle`, `Diamonds draw`. The full sentence ("2 to attack · 2 when discarded", then the exact power) stays on the desktop face and on the long-press sheet. `card-index` survives only as the stand-in for art that fails to load — a case where there is nothing printed to double.
- **Consequence**: the suit powers have one home, `SUIT_POWERS` / `SUIT_SHORT` in `web/format.js`, used by the legend, the phone face and the tests, so they cannot drift. Nobody reads `9 to attack · 9 when discarded` on a 58 px card again; nobody loses it either, because it is one gesture away.

### D-25 The Jester is a catalogue choice, not a per-set file
- **Status**: Accepted
- **Context**: each deck carried its own `j<id>.webp`, so "the joker" was whatever art happened to sit next to the deck — a blank letterpress card for Modern, a red house joker for Simple, an ornate red card for 8bit, Ditto for Pokémon. The operator supplied one more Joker card and a 16-design reference sheet and asked for them as a choice: "make the jokers an option in the option menu like we do theme and backs… and make sure we have a preview for them in the settings and main menu."
- **Decision**: Jester faces move to a catalogue, `img/jokers/<id>.webp` + `img/jokers/index.json` (labels), published in the registry as `jokers[]` with a `defaultJoker` and a per-theme `defaultJoker`. Both Jesters in a pack share one face, the way a real pack ships one joker design. Settings gets a "Jester face" grid (the faces *are* the choice), the main menu and Settings preview all three choices together (`face · Simple`, `back · Red`, `joker · Jester`), and `prefs.joker` stores the pick.
- **Pairing rule**: like a card back (D-15), a set brings its own Jester — Pokémon brings Ditto, every other set brings the shared "Jester" — and switching set switches the face. The player can then pick any face and keep it.
- **Consequence**: 21 faces on day one (5 the decks already had, 16 sliced from the reference sheet). Adding a face is a PNG→WebP build plus a label; no code change. The catalogue index is generated, so the registry, the menu and the credits table read one source.

### D-26 The top bar keeps what a run needs; the Menu holds the rest
- **Status**: Accepted
- **Context**: the header carried five buttons (History, Rules, Leaderboard, Settings, and Menu on a phone). Leaderboard and Settings are not things a player touches mid-hand; they are visited deliberately. With the version now printed beside "solo", the bar was also the widest thing on a phone screen.
- **Decision**: the top bar keeps **History** and **Rules** only, plus the Menu button on phones. Leaderboard, Settings, Restart and Credits live in the Menu sheet, which both the desktop `Menu` button and the phone nav button open. Focus returns to the Menu button, not to a control that no longer exists.
- **Consequence**: one place to look for anything that is not part of the turn. The smoke test asserts the lean bar (`ui.topbarLean`) so a button cannot quietly creep back.
- **Also**: the header prints the running build (`SOLO v0.5.0`) from `/api/health`, so a screenshot always says which build produced it.

### D-27 The "played" area shows cards, not a count
- **Status**: Accepted
- **Context**: the attack area carried a red chip counting the cards played against the current enemy — a number nobody reads, since the cards themselves are right there, and it made the box as tall as a pile with a deck in it.
- **Decision**: no counter. The zone shows the played cards as minis, is only as wide as its contents, and **does not exist while it is empty** (an empty box was a floating label).
- **Amendment (same day)**: a fourth box squeezed in beside the other three looked wrong, so on a wide screen the piles are a **three-column row** (Tavern, Discard, Jesters) with the played area as a **full-width row beneath them**. It appears when cards land and collapses when they move to the discard, so nothing reflows but the board's height. The pile art grew with the boxes — Jester faces at 46 px showed the frame and nothing else, and they are now 64 px.
- **Consequence**: the piles row is three zones; the played row is extra height only while cards are actually on the table. Nothing is lost, because the cards *are* the state.

### D-28 The royal shares a row with its counters, and the table belongs to the hand
- **Status**: Accepted
- **Context**: the enemy card sat alone above two full-width counters. That block cost ~330 px of a 844 px phone screen before the hand got any, so the second row of cards always needed a scroll; on a desk it kept the hand and the piles small while half the window stayed empty. The operator: "we put the health and attack boxes to the right of the royal card… on mobile that would allow us to move our two rows of cards up… on desktop it would allow us to size up all the cards."
- **Decision**: `.enemy-main` puts the royal and its two counters in one row, counters stacked beside it. The royal shrinks to `clamp(80px, 16vw, 128px)` — a reference, not the subject — which hands the width back to the counters and the height back to everything below. The freed space goes into the cards: hand 58 → 70 px on a phone and up to 104 px on a desk; tavern back 86 px, fan 66 px, Jester faces 76 px.
- **Consequence**: a phone shows the royal, both counters, all three piles, the played row, **both** hand rows and the action bar without scrolling. Sizes live in one place per breakpoint, so the next layout change is a two-number edit.

### D-29 A Modern card prints its rank+suit and its power, nothing else
- **Status**: Accepted
- **Context**: Modern's blank middle carried the full name, the value line ("9 to attack · 9 when discarded"), the power sentence and a footnote. The value is arithmetic the player does from the rank, and the footnote repeats what the step hint says.
- **Decision**: `.card.theme-modern .card-value` and `.card-note` are never rendered; the face carries the name (rank and suit) and the power. The enemy card drops its value line too, because its attack and health are on the counters beside it. The full text stays on the zoom sheet.
- **Consequence**: three lines of text where there were five, at the same card size — which is why the same 104 px card now reads from across a desk.

### D-30 Step 4 takes any number of cards, in one action
- **Status**: Accepted
- **Context**: paying an enemy attack meant one card at a time, each its own round-trip. Covering a 17-damage hit with 10 + 7 was two selections, two confirms and two animations for one decision the player had already made.
- **Decision**: `PayDamage` carries `cardIds` (the legacy single `cardId` still decodes, so an older client or a stored action keeps working). The engine applies the cards in the order given, logs one `suffer` event per card, and enforces the stop-at-threshold rule across the batch: without over-paying, no card but the last may bring the total to the required amount — a batch that would overshoot early is **rejected whole**, never silently truncated. `LegalActions` still lists single-card payments (subsets would be exponential in the snapshot); the browser composes the batch and the engine validates it.
- **Consequence**: one tap per card, one confirm, one request. The button and the step hint both count down by the whole batch ("Discard 2 cards: 5♣ 2♠ · 3 damage left"), and a tap that would end the payment early is refused with a toast instead of being dropped. The rule lives twice — Go for authority, `paySelectionValid`/`paySelectionAfter` in `web/format.js` for the button — and both are tested (engine table tests, node unit tests, smoke `ui.payMultiple` / `ui.payRefusesOvershoot`).

### D-31 Two repositories, one game: solo frozen on :8123, combo on :8321
- **Status**: Accepted
- **Context**: solo is finished and runs every day on :8123. Co-op needs the same engine and the same client, but four players need a different layout, a different transport and a much bigger surface for bugs. Letting co-op land in the solo tree would put a working game at risk for a feature nobody has played yet.
- **Decision**: the solo-only tree (this game as it shipped, v0.6.0, tag `solo-locked-v0.6.0`) stays exactly as it is and keeps port **8123**. This repository is a **clone** of it with its history, and its binary serves **both** modes on port **8321** (solo + co-op), with its own `-data` directory. Nothing co-op is ever committed to the solo tree; a solo bug fix is applied twice, by hand, and that cost is accepted.
- **Consequence**: the operator can run a mixed evening — someone solo on 8123 or 8321, a table of four on 8321 — and a co-op crash cannot take the working instance down. The clone has no upstream remote, so the two trees drift deliberately: drift is a decision, not an accident.

### D-32 Co-op plays with two card sets: `simple` and `8bit`
- **Status**: Accepted
- **Context**: with four hands, a seat rail and a played area on one screen, every pixel the art does not need is a pixel the readable information does not get. The operator: "for multiplayer I say we only allow simple and 8bit cards for max readability."
- **Decision**: a co-op room accepts `theme` ∈ {`simple`, `8bit`} and nothing else (amended by D-64: `official` is now allowed) — validated in `POST /api/rooms` (`400 bad_theme`), not merely hidden in the picker. **Solo in this same binary keeps all four sets**, because solo has the screen to itself. Seat colours are then chosen to stay legible against both decks' faces and the dark board.
- **Consequence**: an older link with `?theme=pokemon` cannot start a co-op room; the lobby shows the two tiles only, and the rule is one constant (`coopThemes`) that the tests and the client both read.

### D-33 Pointers are public, hands are not
- **Status**: Accepted
- **Context**: co-op needs a way to say "take the spades" without typing, and the rulebook forbids revealing hand contents. Voice is out of scope. The operator: "we let everyone see each others mouse cursor / finger movements on the screen (exception being over their own cards) as different coloured orbs that float — red, blue, green, black."
- **Decision**: every seat broadcasts a **floating orb** in its seat colour (seat 0 red, 1 blue, 2 green, 3 black). Coordinates are normalised to the stage box, so an orb sits over the same *thing* on a phone and on a 2560px desk. The owner hides it: over its own hand, its own peek strip, or while a sheet is open — an explicit `hidden` flag sent by the owner, never guessed by the viewer. Black gets a light ring so it survives the dark board. Own orb is never drawn.
- **Consequence**: the table can point at the castle, the tavern, the attack or another seat's play — never at a private card. This is the co-op communication rule expressed as an input model, so it needs no moderation and no free text. Presence is rate-limited and never persisted.

### D-34 SSE down, POST up
- **Status**: Accepted (reverses §2.7 of the first proposal, which chose long-poll)
- **Context**: the first proposal weighed long-poll against SSE for *state* changes, where a change every few seconds makes both viable. Shared presence changes the arithmetic: at 10 Hz, long-poll is 10 requests per second per player, each with its own connection setup.
- **Decision**: one `text/event-stream` per connected seat carries state frames, coalesced presence frames (all seats, one frame per ~100 ms) and pings; actions and cursor updates are plain `POST`s. `EventSource` reconnects on its own; the client repairs with a full snapshot when the stream's seq is newer than the last applied one. Stream handlers tighten their own write deadline with `http.ResponseController`, so the server's 30 s `WriteTimeout` does not cut a healthy stream, and a package-level `done` channel lets `Shutdown` drain them.
- **Consequence**: four players cost 10 frames/s downstream and ~48 tiny POSTs/s upstream on a LAN. WebSocket stays out: not in the stdlib, and hand-rolled framing buys nothing here. Presence frames never touch `app.state` or call `render()` — they move existing DOM nodes, because a 10 Hz full render would fight selection and animation.

### D-35 In co-op the table belongs to the players
- **Status**: Accepted (amends D-27 for multi-seat games only)
- **Context**: solo shows one played row because one player acts. With four seats the played area is the only place the table can see what everyone did this turn, and it was sized as a leftover.
- **Decision**: the played area becomes **THE TABLE**: one group per seat that played, led by that seat's colour dot and name, with art at 72 px desktop / 44 px phone, and **bigger than any previous mini**. It grows downward while a turn is in progress and clears when the enemy falls. The vertical space comes from the solo Jester tray, which does not exist in co-op (Jesters there live in the tavern or a hand, never in a reserve). Hands stay closed; the seat rail carries counts, last play and away dots.
- **Consequence**: solo layout is untouched (`numPlayers < 2` renders exactly today's board), while a four-seat table reads as a timeline of the current turn rather than a count.

### D-36 Closed hands, bounded pings, and no co-op leaderboard
- **Status**: Accepted (the ping vocabulary is superseded by D-51)
- **Context**: the rulebook forbids revealing hand contents and allows stating a desire about who goes next after a Jester; the leaderboard's tiers are derived from solo Jester flips, so a co-op result would be a number that means nothing.
- **Decision**: hands are closed server-side (`SnapshotFor` sends only the viewer's hand); a room option `openHands` exists for a table sitting in one room and is off by default. Communication is a fixed ping vocabulary (`wait`, `go`, `play_this`, `nice`, `help`) stored for 45 s, never free text; `go` is only available while a Jester seat choice is pending. Co-op games are **stated to be leaderboard-ineligible** (rejected on `len(Players) > 1`), not merely by accident of rule options, and the co-op end sheet offers no submit form.
- **Consequence**: no moderation surface, no hand leakage, and the solo board keeps meaning what it meant.

### D-37 The engine learns co-op without changing solo
- **Status**: Accepted (`engine.Version` 1 → 2)
- **Context**: the engine was seat-shaped from round 1 (`Players []Player`, `ActiveSeat`, the 2/3/4 setup table, round-robin diamond draws), but three places assumed one player and one place was provably wrong: the Jester ended the turn instead of naming the next seat, `ChooseNextSeat` was rejected, "every other player has yielded" was a counter (`ConsecutiveYields`) that mis-reads *A yields → B plays → C yields → A yields*, and `Snapshot` projected the active seat's hand for everybody.
- **Decision**: a Jester play in a multi-seat game sets `SeatChoice{By, Next}`, does not advance, and makes the seat options the only legal actions; `applyChooseNextSeat` validates and starts a fresh turn. Yield legality becomes per-seat (`Player.YieldedLastTurn`, cleared by that seat's next play) behind one predicate, `yieldLegal()`, and "cannot act" becomes `canAct() = cards || Jester || yield` — **in co-op only**. `loseWith` stamps `LostSeat` and the loser's seat on the event. Diamond draws emit one `EvDraw` per seat when `len(Players) > 1` and stay a single event in solo. `SnapshotFor(g, View{Seat, OpenHands})` scopes the hand to the viewer, publishes a seat rail (`SeatView`: count, last play, yielded, active) and gives legal actions only to the seat that may act; `Snapshot` is `SnapshotFor(View{Seat: activeSeat})`, so the solo client is untouched. Version 2 because the Jester and yield semantics changed; stored v1 sessions are discarded by the existing loader.
- **Consequence**: solo behaviour is bit-for-bit what it was (the same golden vectors, the same single draw event, the same empty-hand loss even with `allowSoloYield` on), and co-op becomes possible without a second rules implementation. Ten named acceptance tests in `engine/coop_test.go` pin the deltas, including a 200-seed termination property and a per-seat non-leak test at 2, 3 and 4 seats.

### D-38 A player does not choose a name; the table gives them one
- **Status**: Accepted
- **Context**: co-op has no accounts and no leaderboard, so a name is only ever a label on a seat rail and an orb. A typed name is a moderation surface, an impersonation surface and one more field to fill in while four people wait.
- **Decision**: the server assigns a name when a seat is created or joined, from `regicide/names` — historical European royalty weighted English-heavy (31 English, 20 French, 14 German; the German list keeps only the short, plain names: Otto, Karl, Konrad, Leopold, Ludwig, Wilhelm, Agnes, Hedwig, Sophie, Gisela, Beatrix, Albrecht, Friedrich, Heinrich). A room reserves one name per seat at creation, so a four-seat table never repeats a name. Assignment is deterministic from the room code, so a replayed room (and its tests) is reproducible. The client never sends a name and the API has no field for one.
- **Consequence**: the seat rail, the orb colour and the log line all identify a player without a profile, and there is nothing to spoof. "The green one, Blanche" is a complete identity.

### D-39 Players find each other on one URL and one room list
- **Status**: Accepted
- **Context**: the operator runs a single instance on the LAN. The first proposal assumed a typed room code as the only way in, which is the *worst* path: four people in one room all need the code read aloud, and a guest who mistypes it gets an error with no other option.
- **Decision**: three layers, in order of use. (1) **One address**: everybody opens the same `http://<lan-ip>:8321`; there is exactly one server, so no discovery protocol is needed. (2) **A live room list** on the start screen — host name, seats taken, card set — one tap to join, seat and royal name assigned in that request. (3) **A 5-character code** (Crockford alphabet, no I/L/O/U, case-insensitive) shown large in the lobby, usable as `?room=CODE`, for the edges: a chat link, a room that freed a seat, a player who arrived after the list moved on. Returning is `localStorage` per room code, so a reload rejoins the same seat. mDNS/Bonjour, accounts, matchmaking and spectators are all out.
- **Consequence**: the common case costs one tap and no conversation, and the code exists only where it helps. The room list is also the reason `GET /api/rooms` returns room metadata but never a seat token.

### D-40 A room is an open table for four; the rules are dealt for who is there
- **Status**: Accepted
- **Context**: co-op rules depend on the player count — hand limit 7/6/5 and Jesters in the tavern 0/1/2 for 2/3/4 players — and a room that asked for a count up front would either refuse a late friend or deal a four-player game with an empty chair.
- **Decision**: creating a room opens a **waiting room with capacity 4**, and the capacity is not a promise. Anyone may join while it is a lobby; the host may start as soon as two seats are taken and every seated player has not held the table. `NumPlayers` is `len(seats)` at the deal, so the setup table follows reality. The lobby publishes `nextRules` (players, hand limit, Jesters in the tavern) so the effect of a fourth join is visible before anyone commits. Joining marks a seat ready; a player may explicitly hold the table, which disables the start button until they release it.
- **Consequence**: no declared player count exists anywhere in the API, and a three-player game is a real three-player game — not a four-player game with a spectator's chair. The engine needed no change: `MaxHandSize`/`JestersInTavern` already take the seat count.

### D-41 A disconnect pauses the table; it never shrinks it
- **Status**: Accepted (the "no auto-play, no bot substitution" clause is amended by D-62: bot fill)
- **Context**: the obvious alternative — carry on with N−1 players — quietly changes the game: the hand limit was dealt at 5/6/7, the Jesters already shuffled into the tavern belong to that count, the diamond draw order skips a seat, and the loss condition ("unable to play a card or yield") is evaluated per seat. A seat that vanishes mid-game would either be played by nobody (a stall) or by a bot (a different game, decided by a machine the player never agreed to).
- **Decision**: **pause, never amputate.** Detection is absence, not tab visibility: an SSE stream that closed, no heartbeat for 90 s, no authenticated request touching the seat. At 90 s the seat is marked `away` (hollow dot, "waiting for Blanche") and the game stops advancing on that seat's turn only. At 5 minutes the host may hand the seat to a new device (`POST /seats/{n}/take`); the seat keeps its name and hand, and the old token is invalidated so two devices can never hold one seat. Otherwise the host deletes the room and the 2-hour GC catches whatever is left. No auto-play, no auto-yield, no bot substitution, ever.
- **Consequence**: the table can be stuck waiting, and that is the accepted cost: the alternative is a game that silently breaks its own rules. The recovery paths are all human (reconnect, take over, abandon) and all one tap.

### D-42 The host chooses: wait, share a reconnect link, take the seat, or cancel
- **Status**: Accepted
- **Context**: D-41 decided the table pauses rather than shrinking, which leaves the host holding a stopped game and no instructions. Waiting forever is not an option, and taking a seat from a player who is merely on a train is rude; the host needs the safe middle ground.
- **Decision**: the room view publishes `waitingFor` (the away seat whose turn it is, i.e. exactly when the table is stopped), and the host is offered four actions. **Wait** changes nothing. **Share a reconnect link** mints `/?room=CODE&seat=N&rejoin=<key>`: 10-minute life, single use, replaces the seat token, and the seat keeps its name and hand. The raw key is returned once and kept by the host's browser; the server stores `sha256(key)` and a deadline, so a copied session file cannot take a seat. **Take the seat** (after 5 minutes) moves it to the host's device, minting a fresh token and killing any live link. **Cancel the game** ends the table for everyone: the file stays until the GC so the other seats read `cancelled` and `409 game_cancelled` instead of seeing the room disappear mid-turn.
- **Consequence**: every recovery path is one tap, none of them invents play, and the link is the only thing that can revive a seat whose browser lost its storage. When the host shares a link they are handing over that seat, which is the correct trust model for a LAN table.

### D-43 A spectator link needs no key, and grey orbs are switchable
- **Status**: Accepted
- **Context**: a spectator holds no cards, takes no turn and cannot act, so there is nothing for a token to protect — the room code *is* the whole invitation. But an audience still changes how a table behaves, and the operator asked for two controls: grey translucent orbs so a spectator can point at things, and a per-player switch to turn those orbs off.
- **Decision**: **`?room=CODE&watch=1` is the spectator link.** `POST /api/rooms/{code}/watch` returns an ephemeral watch id (memory only, capped at 8) and the public snapshot with `viewerSeat: -1`: table, seat rail, log, enemy — and **no hand**, not even in an `openHands` room; legal actions empty; the id is not a seat token and cannot act (presenting it as one is a 401). Spectators share the presence channel with a visually distinct orb: translucent grey, no seat colour and no name. `prefs.spectatorOrbs` (default on, per browser) hides them for that player only. The host may switch watching off entirely, which drops open watchers and stops advertising the link. The header shows how many are watching.
- **Consequence**: watching is one URL with no per-person lifecycle and nothing to revoke individually; the only switch that matters is the room-level one, plus a local display preference so a player can ignore an audience without taking it away from the table.

### D-44 Hand size is public; the rail shows it against the limit
- **Status**: Accepted
- **Context**: co-op rules turn on hand *sizes* — the diamond draw skips a full hand, the hand limit is 7/6/5 by player count, and whether a player can pay the next attack is a count, not a secret. The engine already published `handCount`, but "6 cards" says nothing without the limit beside it.
- **Decision**: `SeatView` gains `handLimit` and `handFull`, and the rail renders `Name 6/6` with a marker when full. Contents never leave the server; counts and the table limit are always visible to every seat and to spectators.
- **Consequence**: a glance at the rail answers "who is at their limit, who is nearly out of cards" without arithmetic, and a diamond play can be reasoned about by the whole table — which is exactly the information the rulebook makes public.

### D-45 Spectator hands are a host switch, and "view as" is how they work
- **Status**: Accepted
- **Context**: watching a table with four closed hands is a broadcast of a count, which is dull and teaches nothing. Watching *as a player* — see their hand, their side of the board — is the thing that makes a spectator useful (learning the game, coaching, a fifth friend waiting for a chair). But it is also a different game for the four people playing: if everyone knows the audience can read hands, table talk changes.
- **Decision**: two host switches, `allowed` and `hands`. With `hands` off (the default) a spectator gets the public table and **no hand at all**, even in an `openHands` room. With it on, the spectator taps a player in the seat rail and the server re-projects the board **as if they were that player**: that player's hand in the hand area, `watchingAs` set in the response, `spectator: true`, and `legal` always empty — a spectator reads, never acts, and the watch id is not a seat token. An impossible seat (`?as=99`) falls back to the table view. Turning spectators off closes hands with them, because hands cannot be open in a room nobody may watch.
- **Consequence**: the audience's power is explicit, revocable by one host switch, and visible to the table (`watchHands` is in the room view, so a player can see whether the audience can read hands). A spectator never gets a button, and never gets a turn.

### D-46 The host wears the crown, above the name
- **Status**: Accepted
- **Context**: a table of four royal names needs one obvious answer to "who is in charge" — the host starts the game, mints reconnect links, takes away seats and can close the table. A text label ("host") beside a name competes with the seat state ("ready", "away", "you"), and a coloured dot is already taken by the seat colour.
- **Decision**: the host's seat carries the **brand crown**, the same `img/brand/crown.svg` as the header, positioned **over** the name rather than beside it, on both surfaces: the lobby row and the board's seat rail. The host chip also gets a gold-tinted border. Nothing else marks the host.
- **Consequence**: authority is legible at a glance and identical in both places, and the mark is the game's own mark, so it needs no legend.

### D-47 Every game records a self-proving log, and it is the training corpus
- **Status**: Accepted
- **Context**: the operator asked for a log with the seed, every hand at every moment, and every action — something that can be replayed, analysed, and used to train a better bot. The engine is already deterministic from its seed, so a log needs to record *actions*, not states; but a log that only claims "these actions happened" is unverifiable, and a log that stores every full state is ten times larger and still unverifiable.
- **Decision**: one JSONL file per game (`<data>/replays/<sessionID>.jsonl`): a header (format version, `engine.Version`, seed, rules, seats and names), one line per action carrying `seq`, `seat`, the action and a **`StateHash` of the position after it**, then a result line. `StateHash` covers every field that affects play (hands, piles in order, counters, phase, turn, RNG offset) and excludes the derived history log. Playback rebuilds the game from the seed and compares every hash, so a tampered or truncated file fails at the exact step it diverges. Steps are appended under the session lock, after the idempotency cache, so a retry never duplicates a move. `cmd/replay` verifies, summarises (per-seat actions and damage) and exports a training corpus of `(position, legal set, chosen action, outcome)` per decision.
- **Consequence**: every game — solo and co-op — leaves evidence that proves itself, a `-summary` for the after-action review, and a corpus a bot can imitate or be evaluated against without re-deriving positions. Because a replay contains **every** hand at every moment, it is a server-side artefact: the host may download it, a spectator never may.

### D-48 Phase is an enum: print it with String(), never with a conversion
- **Status**: Accepted (bug found while building D-47)
- **Context**: `Phase` is `type Phase uint8`. Writing it into the replay as `string(g.Phase)` silently produced a one-byte control character (`\u0004`) instead of `"lost"`, so every recorded result line was wrong in a way that only showed up when a reader compared it to the rebuilt phase.
- **Decision**: always `g.Phase.String()`. `StateHash` marshals the same enum through `encoding/json`, which uses `String()` for a `Stringer`, so the hash form and the wire form agree.
- **Consequence**: no control bytes in files, and a rule for any future enum: a conversion is not a serialisation.

### D-49 The stream is read with `fetch`, not `EventSource`
- **Status**: Accepted (deviation from MP-PLAN §4, which named `EventSource`)
- **Context**: the plan said "client `web/live.js`: `EventSource` with `lastEventId = seq`". `EventSource` cannot set request headers, and a seat authenticates with `X-Regicide-Seat: seat:token`. The only way to use `EventSource` as planned is to move the token into the query string, where it lands in server logs, proxy logs, and any `Referer` the page ever sends.
- **Decision**: one `fetch` with `Accept: text/event-stream` and the seat header, read through `res.body.getReader()`, parsed by a 40-line incremental SSE parser. The wire format is unchanged: `id:`, `event:`, `data:`, `: ping`, and a blank line between frames. `lastSeq` still drives gap repair, and `AbortController` replaces `eventSource.close()`.
- **Consequence**: the credential stays in a header, the parser is testable in node without a DOM (8 tests), and the client gets per-request control that `EventSource` does not offer (no retry for a 401, which would loop forever). The cost is that the reconnect/backoff policy is ours to get right, which is why it has its own tests.

### D-50 The solo routes refuse a co-op session
- **Status**: Accepted (finding F-30, from a review of the P0 diff)
- **Context**: `/api/sessions/{id}` predates rooms and keyed on the session id alone. Every co-op guarantee — seat tokens, the turn gate, the withheld seed, the host-only replay, "no leaderboard for a table" — is enforced on `/api/rooms/*`. Measured before the fix: an unauthenticated `GET /api/sessions/{coopID}` returned `seed: C00PSEED, seedVisible: true` and seat 0's whole hand.
- **Decision**: a session that has a room is **not found** on the solo routes (GET, actions, replay, restart, DELETE), and the leaderboard submit route answers 403 for it. The check is one call to `roomFacts` at the top of the session router, so a future solo route cannot forget it.
- **Consequence**: the session id stays a capability for solo practice, as it always was, and stops being a skeleton key for a live table. A shared URL to a co-op game is now worthless to anyone who is not holding a seat token, which is what a seat token was for.

### D-51 The felt marks are landmarks; presence is the only thing that moves
- **Status**: Accepted (supersedes the ping vocabulary in D-36)
- **Context**: the plan carried a ping channel — `wait / go / play_this / nice / help` — with a TTL, a per-seat cap, a spectator-visibility rule and a test surface. The operator's design removed the idea entirely: the table is a shared whiteboard. Players see each other's pointer dot move, and a dot *resting* on a shape pressed into the felt is the whole message.
- **Decision**: four debossed shapes (smile, frown, question, exclamation) sit in the felt as **places**, not controls: `pointer-events: none`, `aria-hidden`, no click, no event, nothing stored, nothing rate-limited. A dot is drawn per seat from the presence frame at 10 Hz, positioned over the felt, and **your own dot is always drawn** while other players' dots are a per-browser preference. A dot over its owner's own hand or action bar is hidden at the source (D-33).
- **Consequence**: the social surface costs one cursor relay and one frame type, both already built for P1. It removed a server task, a TTL, a limiter, a spectator-visibility question and three tests from P4.

### D-52 The co-op table shares the solo DOM, and solo's layout is a test
- **Status**: Accepted (after a shipped regression)
- **Context**: the room board reuses the solo board's elements — one enemy card, one hand, one action bar — and arranges them as a table with `body.is-coop` rules on `.board`. That needs two wrappers (`#cloth`, `#pile-side`) that exist only for the room grid. When the co-op block was rewritten, the rule that makes those wrappers dissolve outside a room was dropped, and solo collapsed into a shrink-to-fit stack. Both smokes passed: neither asserted anything about the solo board's *layout*.
- **Decision**: the wrappers are `display: contents` by default and become boxes only under `body.is-coop`; every co-op rule is scoped to a room. The solo smoke now asserts `solo.wrappersDissolve` (`#cloth` computes to `contents`), `solo.pilesKeepTheirArea` (`.zone-piles` is still in `grid-area: piles`), `solo.boardKeepsItsAreas` and `solo.handSized`.
- **Consequence**: sharing the DOM is allowed, but a shared-DOM change is a change to the frozen mode: the gate must be able to fail on it. A visual regression check (screenshot the solo board, fail on a large bounding-box change) is the next step worth taking.

### D-53 The replay viewer ticket is time-limited, not single-use
- **Status**: Accepted (deviation from the P6 task text)
- **Context**: the task said a "single-use, 2-minute ticket". A viewer has to read many steps, so single-use would mean minting a ticket per step — which is worse: it turns every arrow key into an authorisation event and multiplies the mint surface.
- **Decision**: the host mints a ticket that lives **two minutes** in memory, and that ticket authorises any number of `GET /api/replay/{ticket}?step=N&as=SEAT` reads until it expires. The **file download stays host-only and seat-authenticated**; the route is never relaxed.
- **Consequence**: the URL is worthless two minutes after the host opens the viewer, and the host-only rule for the artefact that contains every hand is unchanged.

### D-54 An open stream is the heartbeat
- **Status**: Accepted (found by a simulated game)
- **Context**: D-41 says a silent seat pauses the table rather than shrinking it, and the server marks a seat away after 90 s of silence. Only a poll or a `/ping` refreshed that timestamp, and the default client does neither: it opens one stream and listens. So after 90 s every seat read as away — the banner said "Waiting for Elizabeth" while Elizabeth was playing — and the host was offered "Take the seat" for a player sitting right there. The same gap killed spectators: F-4's 60 s watcher TTL expired a watch id whose only liveness signal was its stream.
- **Decision**: the session's hub ticker refreshes presence for every subscriber, seats and watchers, once per 100 ms while anyone is listening. A live connection *is* presence; nothing else has to say so.
- **Consequence**: presence has one source and it cannot disagree with what the table can see. A seat whose stream is open is never "away"; one whose stream dropped is, after the usual threshold, which is what the banner is for.

### D-55 A yield is illegal only when every other seat yielded
- **Status**: Accepted (corrects R-044's implementation)
- **Context**: the rulebook says "A player may not yield if every other player has yielded on their last turn … if two players have yielded in a row the third player may not yield". `yieldLegal` implemented the negation: it required every other seat to have yielded. Since only a yield sets that flag, no seat could ever be the first, so `Yield` never appeared in the legal list — and because the empty hand is the case yielding exists for, every co-op game that ran a hand dry ended with `LostCannotAct`. Three tests pinned the inversion, and the format doc's worked example ends that way.
- **Decision**: a yield is legal unless every other seat yielded on its own last turn. The tests state R-044, including the two-yields-in-a-row case and the escape hatch from an empty hand.
- **Consequence**: a dry hand is a decision, not a loss. The table now usually ends with `cannot_defend` (paying an attack it cannot cover) rather than seizing up, which is also what the rulebook describes. The lesson goes in the process: **a test that quotes a rule can still encode its opposite** — cite the sentence, then assert the two cases around it.

### D-56 The game is played in real browsers before a phase is called done
- **Status**: Accepted
- **Context**: the unit tests, the solo smoke and the co-op smoke all passed while the table was unplayable in three ways (presence, yield, Jester choice). None of them could see it: two were about a *state* nobody thought to assert, and one was a UI affordance nobody clicked. Three scripted players and one spectator, each in its own browser, found all three in twenty minutes.
- **Decision**: `scripts/coop-referee.mjs` is part of the project's test tooling. It owns a real room, opens one browser per seat plus a spectator, and exposes each as HTTP (`facts`, `play`, `yield`, `jester`, `shot`, `spec/as`) so a script needs only curl. Run it at the end of a phase, with someone watching the spectator view.
- **Consequence**: "can four people actually play this" becomes an observable, not an opinion. It is not in the merge gate — a live browser game is slow and non-deterministic — but a phase is not finished until a game has been played on it.

### D-57 A child of the board either names an area or is out of flow
- **Status**: Accepted (after a player reported it three ways)
- **Context**: in a room the board is a CSS grid. `#peek` (opened by tapping any hand card on a phone) and `#log-strip` (in flow below 559 px) carried `grid-area` values from the solo templates — `peek` and `log` — that the co-op templates never declared. Both were auto-placed, which creates implicit rows and columns and crushes the real ones: at 360 px the grid became `78px 60px 0px 190px`, the hand 314 → 144 px, the board 774 → 1151 px, the page 861 → 1238 px. `overflow-x: hidden` hid the damage and `document.scrollWidth` stayed clean, which is why every smoke passed.
- **Decision**: every child of `#board` either names an area that exists in the current template or is out of flow. The co-op phone template declares `peek` and `log`, and the rule is asserted rather than remembered.
- **Consequence**: opening the peek on a phone no longer moves anything, and a future overlay cannot reintroduce the class by accident — the check counts implicit tracks.

### D-58 A pointer is a place, not a fraction of the screen
- **Status**: Accepted (supersedes the board-fraction model of D-33; amended by D-69,
  which restores the board fraction as the fallback while keeping the mark vocabulary)
- **Context**: a cursor was a fraction of `#board`. The marks are laid out with `clamp()` and fixed sizes, so the felt's furniture is not proportional to the board: the smiley sat at 27.25%/61.52% on a desktop and 18.60%/56.46% on a phone. A dot placed on the smiley was 51 px away from it on a 47 px mark — measured, not estimated.
- **Decision**: a pointer frame carries a fraction of the **felt** plus the **mark index** it is over and the fraction inside that mark. A receiver resolves a mark against its own `.felt-mark` element, so "my dot is on the frown" is true on any screen by construction. A frame that names no mark falls back to the felt fraction.
- **Consequence**: measured after the change, a desktop dot aimed at the smiley lands 0 px from the phone's smiley. The cost is that the receiver may not have that many marks, which is why the index is checked and the felt fraction is always carried as a fallback.

### D-59 The gate runs Gecko
- **Status**: Accepted
- **Context**: the player uses Firefox Focus on a phone and Zen on the desktop, both Gecko, while every gate stage, smoke and script in this repo drove Chromium over CDP. Twenty stages went green without a single Gecko layout.
- **Decision**: Firefox ESR and geckodriver are test dependencies. `scripts/gecko.mjs` speaks WebDriver; `gecko-smoke.mjs` runs the solo and co-op smokes (both transports) in Firefox and is a gate stage. Because a Firefox window cannot be narrower than ~500 px, a phone viewport in Gecko is reproduced with a same-origin iframe, where media queries, `100vw` and `position: fixed` resolve against the frame.
- **Consequence**: Gecko is no longer invisible to the gate. Two findings came from it immediately: a headless screenshot taken at load is not evidence (it caught the setup screen before the module painted), and the smoke's readiness signal (`document.title`) is what the harness must wait for.

### D-60 The host can be succeeded
- **Status**: Accepted (supersedes the "host is seat 0, forever" assumption of D-42)
- **Context**: `HostSeat` was set at creation and never moved, every administrative action is host-only, and `TakeSeat` refuses the host's own seat. A host who closed the wrong tab left a lobby nobody could deal and a table nobody could cancel, with no path to a new host.
- **Decision**: host authority is a role on one seat. It moves to the **lowest-numbered present human seat** when the current host has been silent for `takeAfter` (5 min) — bots, away seats and seats that chose to leave are never candidates — and it can be handed over explicitly by the host to a present human seat (a left seat is refused with 409; 0.8.7). The demoted host returns as an ordinary seat: their token still works, their hand is theirs, and host-only routes refuse them. A change of host bumps `Seq`, writes a `host_change` event into the log, and is published to every stream.
- **Consequence**: a table is never stranded by one absent seat. The trade is that a present player can gain host powers after five minutes of silence from the host, which is why the change is announced rather than silent. A stranded table keeps its host rather than inventing one: the binding is the invariant.

### D-61 The seat token is the credential; the number is a label
- **Status**: Accepted (supersedes the "seat number authenticates" rule of D-41)
- **Context**: a lobby chair is removed by leaving or kicking, and the chairs above it are renumbered so the engine keeps a contiguous seat slice. A browser that held seat 2 now holds seat 1, and the old strict check (`authed != seat` → 401) logged it out of its own chair the first time somebody below it left. Seat numbers are also what the client prints, so a stale label was visible as well as fatal.
- **Decision**: `X-Regicide-Seat: <seat>:<token>` is parsed for shape and then the number is ignored. The token hash is matched against the room's live seats, and the seat it belongs to is the only seat the request acts on. `Publish` and the stream's welcome frame carry the subscriber's current seat, `adopt()` persists a changed number, and `hub.renumber` moves live subscribers and pointer cursors in the same call that removes the chair.
- **Consequence**: a renumbering is invisible to a player — the board, the rail and the seat actions follow the token. A stale header can no longer authenticate as the wrong chair, because the number never had authority.

### D-62 A silent seat gets a bot, and its owner takes it back by touching it
- **Status**: Accepted (extends the away rules of D-41)
- **Context**: a table with one dead connection stops when that seat owes an action, and the host's only options were to wait, hand over the crown, or take the seat and lose their own. The playtest asked for the table to keep moving.
- **Decision**: a room has a `BotFill` option, on by default and frozen at the deal. While it is on, a seat that has been silent for **60 s** is played by a bot; the host's seat is never filled (a bot cannot hold the menu). Any authenticated touch from the owner clears the bot and stops the loop when the last bot leaves. A player who **leaves** a dealt game marks the seat `left` and gets a bot at once. A join during a dealt game replaces the lowest `Bot` or `Left` seat, never appends one; the seat count is fixed at the deal.
- **Consequence**: a table survives a dead laptop without shrinking or stranding the host. The cost is that a silent player can watch a bot play their hand for a minute before their connection returns, so the seat rail says `bot` for as long as it lasts.

### D-63 A turn that looks repeated must explain itself
- **Status**: Accepted (records the rulebook behaviour the playtest asked about)
- **Context**: three seats reported "the killer keeps the turn" as a bug. It is the rulebook: the killing blow skips step 4 and the same seat faces the next royal. The client printed the defeat without saying who acts next, so a legitimate rule read as a stuck game.
- **Decision**: the defeat line names the next actor — "the same player faces the next royal" or, with the room option `PassOnDefeat` on, "the turn passes to the next seat". The rules sheet states the rule in the same words, and the option is labelled "Turn passes when a royal falls (easier)". The engine default stays the rulebook.
- **Consequence**: the rule is not changed, only the explanation. A table that disagrees with the rulebook switches on the option, which is per room and frozen at the deal.

### D-64 The published deck is a co-op set, and the corner names the build
- **Status**: Accepted (extends D-32's allow-list and D-31's one-binary split)
- **Context**: D-32 limited co-op to `simple` and `8bit` for readability at four hands. The operator supplied the published deck as an uncut sheet and wanted it played at a table. Separately, the header's mode slot read `solo` on this binary, which serves both solo and co-op, so the corner that names the build named the wrong one.
- **Decision**: the official deck joins the co-op allow-list, and the deck is a host-only lobby choice (`POST /theme`, frozen at the deal) shown in the table settings as **Card set**. The header's baseline label is `multi`, and the build version stays in that slot in every mode, including a room.
- **Alternatives considered**: a fixed two-set list (rejects the deck the operator asked for); per-seat themes (the table shares one deck by design); dropping the version in a room to save space (it was already lost for the rest of the session).
- **Consequence**: a table can play the printed deck, and the corner always says which build and which version is loaded. The readability concern of D-32 still applies to any future set: the allow-list is a rule, not a tile filter.

### D-65 The menu is one entry row
- **Status**: Accepted
- **Context**: The third playtest found the opening page hard to read: solo and co-op were two unrelated blocks, the seed was beside the solo button, and the room code sat inside a "Join" label. A player had to work out which field belonged to which button.
- **Decision**: Under the card-set tiles there is one row of ways in — **Start Solo**, **Host MP**, **Join MP** — and directly beneath it the two fields those ways use: **Set seed** in column 1 (aligned under Start Solo) and the **Room code** field across columns 2–3. Join MP reads the code field; Enter in it joins and never submits the solo form. The setup column is centred.
- **Consequence**: One button per mode, one field per option, and the columns line up on a phone and a desk. The room list and preview stay below.

### D-66 Solo owns its grid; the seat rail exists only in a room
- **Status**: Accepted (fixes a shipped regression)
- **Context**: A `min-width: 1200px` block written for the co-op seat rail was unscoped. The later co-op grid overrode it in a room, so its only live effect was on solo: the board became `220px 1150px` with a hidden-but-laid-out `#seats` (the class's `display: flex` beat `[hidden]`), pushing the royal right and the piles left.
- **Decision**: The rail column rule is deleted. `#seats[hidden]` is `display: none`, and every co-op board rule is scoped to `body.is-coop`. Solo keeps its `1024px` two-column template. The desktop hand and piles are centred and the action bar hugs its buttons under the hand.
- **Consequence**: `layout-check` gains a 2560×1600 viewport and property checks: no rail, enemy and piles side by side, hand and bar centred. A rewritten baseline cannot bless the old bug.

### D-67 The co-op table scales with the desk
- **Status**: Accepted
- **Context**: On 2560×1600 the table stayed 1200px wide with a fixed 200px side column and an 818px play column. Health and attack were stacked because the co-op rule set `flex-direction: row` on a grid. The hand was left-aligned by the solo desktop rule.
- **Decision**: Health and attack are a two-column grid under the royal at every width, in a 200px royal column. The hand is centred. At ≥1800px the board grows to 1400px, the royal column to 260px, the play column is capped at 640px, and the royal and pile art scale up. `justify-content: center` keeps the capped tracks centred in their board.
- **Consequence**: The royal-to-piles distance at 2560 drops from ~818px to ~739px with larger furniture, and the two-up stats also shorten the 1440×900 table by ~90px.

### D-68 A bot move is watched, not raced
- **Status**: Accepted
- **Context**: Playtest 3 could not see what a bot did: actions were 900ms apart, and the seat bubble that explains a move lasts 6s but was replaced before it could be read.
- **Decision**: A bot waits **2.4s** between actions (amended 0.8.9; originally 1.5s, which was shorter than the 1.82s table reveal, so the next move arrived mid-reveal). The interval is `Store.botPace`, defaulted in `NewStore`, so a test can shorten it without sleeping through a real table.
- **Consequence**: A four-seat table still moves in seconds, and one move is readable. The interval is pinned by a test (1.2–3s).

### D-69 A pointer is a fraction of the board, private from the cards down
- **Status**: Accepted (amends D-58)
- **Context**: D-58 normalised pointers against `#felt` to make marks exact. That also confined free movement to the 72–108px landmark strip: a dot over the royal, tavern or discard clamped to the strip's edge. The operator clarified the intended rule: everything above the player's cards is shared; the cards and everything below are private.
- **Decision**: `x`/`y` are fractions of `#board`. `mark`/`mx`/`my` are still sent and still resolved against the receiver's own `.felt-mark`, so a dot resting on a mark is exact on every screen. A frame with no mark is placed by its board fraction. A pointer is hidden when it is at or below the top of `#hand`, or over the action bar, peek strip, log or an open sheet.
- **Consequence**: Pointer movement covers the whole table while hands stay closed. The wire format is unchanged; only the meaning of `x`/`y` moves back to D-33's board model, with D-58's mark vocabulary kept on top.

### D-70 A dead seat token never becomes a join
- **Status**: Accepted (fixes the playtest-3 seat-identity incident)
- **Context**: Phase B recorded a tab that joined seat 1, closed for 28 s and came back as seat 3 holding that chair's hand, with no reclaim screen. The entry path treated every 401 from the stored-token refresh as "this device has no seat", so it called join: the first free chair (a bot's or a departed player's) became the tab's chair, cards and all. The client could not tell "my seat is gone" from "I am new here", and said nothing either way.
- **Decision**: A stored seat token is the only way back into a chair. A rejected token shows the lost-seat screen — what happened, and one explicit **Take a free chair** action that names the chair it joins. Only a tab with no stored seat, or a tap on that button, calls join. Bot fill keeps the seat's `TokenHash`: a bot borrows the chair, so the owner's token still reclaims the same seat and hand after the 60 s fill. Only a leave, a kick, or another join can make a returning token 401.
- **Consequence**: A returning player is never silently seated in somebody else's chair, and the one-tap join is an informed choice ("You are now in Dagobert's chair"). `TestBotFilledSeatKeepsItsOwnersToken` pins the server half; the seat-lifecycle smoke pins the client half (no join before the tap, chair count and bot state unchanged). Three follow-ons keep the decision true everywhere: the SSE stream reports a 401/403/404 as `ended` (so the screen appears instead of an unrecoverable chip), `renderRoomScreen` checks `seat_lost` before the lobby (a kicked guest used to redraw the stale waiting room), and the lost-seat screen drops `?room=` from the URL (a reload would otherwise re-enter with no stored seat and join silently — the incident one refresh later). A tab that was covering another chair keeps that chair's return record and is offered **Return to your own seat**.

### D-71 A move is shown large to the other seats only
- **Status**: Accepted
- **Context**: bots act every 1.5 s, and the only way to see what a bot did was to open History. The seat bubble says "attacked with" but the cards were tiny and gone; a play with several cards read as one thumbnail.
- **Decision**: a play or a payment appears for 1.4 s as a large panel in the middle of the table, with every card of the action at once, crossed swords for an attack, a shield for a defence, and then the cards fly to the played pile or the discard. It is pointer-transparent and needs no dismissal. The **actor's own tab does not show it**: that player already saw the cards leave their hand, and a copy over their board would cover the state they are reading. The reveal is fed from the events of a forward sequence, so a poll or a reconnect cannot replay old moves. With reduced motion or animations off it is a short static card instead of a flight.
- **Consequence**: the table narrates itself without a click, and the history drawer stays the reference rather than the way to follow a turn. A fast bot turn can queue at most three reveals; older ones are dropped rather than lagging the table.

### D-72 Four sets, two Jester faces, and a hand-cropped catalogue
- **Status**: Accepted (supersedes the single-face rule of D-15; retires the Modern set)
- **Context**: the menu offered five sets, one of them (Modern) a rule-text template nobody played; the Settings grid carried 22 Jester faces, 11 of them from a scan whose cells cut the corner symbols and the JOKER lettering; and both Jesters always showed one face. The operator asked for four sets (Official, Simple, Standard, Pokémon), a second Pokémon Jester (Porygon) back, two independently chosen faces, and a smaller, clean catalogue.
- **Decision**: the sets are **Official, Simple, Standard** (the 8-bit art, renamed in the UI; the id stays `8bit`) **and Pokémon**, in that order. A set brings a **pair**: Official shows the Original twice, Pokémon shows Ditto and Porygon, and Simple/Standard are dealt a random pair from the eight historical faces (never Original, Jester, Ditto or Porygon). Settings has two slots; either can take any face, including the same face twice. The catalogue is 12 faces: the four set faces plus eight hand-cropped sheet designs (Bower, Crown, Card Table, Banner, Shadow, Staff, Ribbon, Puppet), each deskewed where needed (Banner was scanned upside down), given a paper margin so no corner or word is cut, and whitened from the scan's cream/grey paper. The other 11 faces are retired.
- **Consequence**: the Jester tray, the played pile, the hand and the rail thumbnail all use the chosen pair, so a Jester looks the same everywhere. Removing Modern removes its gate stage and 54 committed faces; the source PDF stays in `artifacts/` and `build_modern.py` is gone. A stored single-face preference migrates to the same face twice.

### D-73 A payment names its payer, and a bot move outlasts its reveal
- **Status**: Accepted (amends D-68; fixes the operator's bot-table report)
- **Context**: with bots at the table the name bar kept the last attack ("Attacked with 8D") after the bot had defended, the defence reveal was missing or showed crossed swords, and a three-card defence appeared as three separate moves. `EvSuffer` carried no seat: a payment is its own action, so the client had no actor for its events, `attributeSeats` returned null, and `revealFor`/`bubbleFor` dropped the batch. The bot itself already paid in one batch (`greedyPayment`); the 1.5s tick also undercut the 1.4s + 0.42s reveal, so the next action arrived while the last one was still on screen.
- **Decision**: every `EvSuffer` carries `Seat` = the payer (the active seat). The bot tick is **2.4s**: the reveal's 1.82s plus a 0.5s beat. The reveal uses the `defend` branch (shield) for a payment, and a payment is one reveal with every card.
- **Consequence**: the name bar and the reveal agree with the last action, a defence shows a shield and all its cards at once, and no reveal is dropped for pace. Pinned by `TestPayDamageEventsNameThePayer`, `TestDefaultPolicyPaysInOneBatch`, the UI tests for `bubbleFor`/`revealFor` on a standalone payment, and smoke `coop.defendRevealShield`.

### D-74 A seat record belongs to one tab
- **Status**: Accepted (fixes the third playtest's tab-identity finding)
- **Context**: the seat record lived at `regicide.room.<CODE>` in localStorage, which every tab of a profile shares. A second tab on the same room adopted the first tab's chair, and a covering record could be overwritten by the other tab.
- **Decision**: the seat record is keyed by a **sessionStorage** tab id: `<CODE>.<tab>`. The covering ("return to your own seat") record is keyed the same way. A pre-0.8.9 room-scoped record is migrated once: the first tab to read it takes it and removes it, so a second tab cannot inherit the same chair.
- **Consequence**: two tabs are two chairs, each survives its own reload, and closing one leaves the other's return record intact. Pinned by the node tests "two tabs of one profile keep their own seat record", "a covering record belongs to the tab that covered", and "the pre-T10 room record is adopted once". **Tradeoff**: a full browser restart clears sessionStorage, so the tab cannot identify its old record; a dealt table then refuses the join and the menu says "This table has already dealt. Ask the host for a reconnect link." The host's one-use link is the supported way back (D-70).

### D-75 The seat rail shows the count, not a capacity bar
- **Status**: Accepted (fixes the seat-rail finding F-54)
- **Context**: each co-op seat carried a 3 px capacity bar under its state word. In a real screenshot the bar was a strong rule across the label row, and the `5/7` count sat stranded at the tile edge on a 1440 px table. The synthetic `is-away` class did not reproduce the finding because the bar exists on every seat; the away transition only made it noticeable.
- **Decision**: remove the bar from `seatRail` and its CSS. The `have/limit` count is the whole signal; there is no second, wordless encoding of the same number.
- **Consequence**: the label row is name, state and the count. F-54 is closed on a real 60 s bot-fill transition at 1440 and 390, and the phone keeps only the seat-colour top edge as its accent.

### D-76 The bot's model of a play matches the engine
- **Status**: Accepted (0.9.0; fixes the policy ceiling)
- **Context**: the 0.8.9 bot scored 4% wins. The weights were not the main problem — the evaluator was. Five rules were modelled wrongly: a Jester play was charged the whole step-4 bill although the engine skips step 4; suit powers were summed per suited card instead of resolved once at the play's total attack (`5♠+5♣` shields 10, `3♥+3♣` heals 6); `Observe` used raw `Shield` instead of `effectiveShield`, and `SnapshotFor` never filled `ClientState.EffectiveShield` (or `ImmunityCancelled`), so the live seat saw zero shield and no unlocked suit; hearts were credited without the discard cap; and shield was credited beyond the bill it can discount. `Weights.ShieldShadow` was declared and set by every persona but read by no code path.
- **Decision**: the evaluator follows the engine for all five; a shield is capped at `min(shield, attack)`; the dead knob is removed. `TestFromViewMatchesObserve` pins the bench path (`Observe`) to the server path (`FromView`) for every seat at every step of a real game.
- **Consequence**: the old weights alone rose from 4.0% to 8.4% on the fixed suite before any tuning. A live bot seat now sees the same shield, tavern and payment facts the bench measures.

### D-77 Diamonds pay the bill, and `HumanEngine` is the shipped policy
- **Status**: Accepted (extends D-76; board decisions D-71/D-73 unchanged)
- **Context**: after D-76 the best line was still a single-card economy. The engine resolves a diamond's draw **before** step 4, so the cards it draws can settle the same turn's bill; the payment search cannot see them and therefore undervalued every diamond play. Separately, the weights had been tuned against the broken evaluator.
- **Decision**: the evaluator credits each card drawn into the acting seat's hand with `DrawCover` (6) points of same-turn payment capacity — the deck's average discard value — and caps the draw at the tavern plus the cards a hearts play just returned. The shipped policy is **`HumanEngine`**, tuned on 2,000 fixed CAPCHECK seeds and validated on four unseen prefixes and at 2/3/4 seats. The gate runs it for 200 games and fails below **18% wins / 9.0 royals**.
- **Consequence**: `HumanEngine` scores **26.5% / 9.48** on CAPCHECK, **25.9–29.6% / 9.39–9.62** on FINAL7/RAND7/T7VAL/T7FINAL, **16.7% / 8.82** at 2 seats and **10.9% / 8.22** at 4. `HumanBestOf` stays in `Policies` as the pre-fix line (8.4% / 8.27 on the same evaluator). The tuning tool is `cmd/botlab`; the rounds and their measurements are in `docs/BOT.md`. The beam/rollout teacher remains unbuilt research and must start from `HumanEngine`.

### D-78 A retired strategy file may not outlive the code it describes
- **Status**: Accepted
- **Context**: an early strategy note, written from played sessions rather than from the engine, contradicted it on hearts ("heal damage"), clubs ("no power"), suit immunity ("immune to damage"), and automatic refills. The bot never read it.
- **Decision**: delete the file. Its verified, useful lessons — shield banks for the royal's life, exact kills recruit, spend then draw, hearts recycle the discard, keep chalk cards for payment, do not spend the last card on damage — now live in `docs/BOT.md` and the policy itself.
- **Consequence**: no live doc states a rule the engine does not implement.

### D-79 The felt and orbs are removed; the phone keeps the desktop order
- **Status**: Accepted (0.9.1)
- **Context**: the operator A:B tested a 0.9.0 build with the felt/orb system removed against one with felt on desktop and hidden on phone. They promoted the **no-felt** build: on a phone there is no strip of marks to point at, so an orb is a second control surface with no vocabulary; on desktop the felt strip cost vertical space for a feature they do not use.
- **Decision**: delete the felt and dots markup, the cursor wiring, the pointer-dot setting and the co-op felt row. Both platforms read players, royal, played, hand. The phone keeps the compact title bar and peek measured in the 0.9.1 draft.
- **Consequence**: the play state fits 390x844 with no scroll and desktop is unchanged otherwise. The client cursor code and server route are inert: no markup renders an orb and no client posts a cursor.

### D-80 A room lives one idle hour; a finished room leaves the list at once
- **Status**: Accepted (0.9.1)
- **Context**: the front page listed every loaded, non-cancelled room and `GC` waited 24 hours. Two abandoned in-progress games stayed watchable for a day, and finished games stayed in the join list.
- **Decision**: one `roomIdleTTL` of **one hour** governs the listing, the GC, the startup prune and `ByCode`. The GC sweeps every minute. `Rooms()` hides a room older than the hour and hides a finished table (`won`/`lost`) immediately; the session is kept until the hour for the end sheet and replay.
- **Consequence**: old games disappear from the front page and their codes die with them; a finished game is not offered as joinable/watchable but its replay remains readable for the hour.

### D-81 A co-op win is the table's, and the sheet waits for the last reveal
- **Status**: Accepted (0.9.1)
- **Context**: a 3-player win showed “GOLD VICTORY” and “No Jesters spent” although two Jester cards had been played. The solo tier is a score for a one-player run, and `jestersUsed` counts solo flips. The end sheet also opened on the same frame as the final state and cut off the last bot reveal.
- **Decision**: on a co-op win, omit the tier; count Jester **cards played** from the log; show **Most Damage, Most Drawn, Most Slain Royals and Most Jesters** (ties listed together) from the same log; and defer the end sheet until the reveal queue drains, with a 250 ms beat and an 8 s guard. The co-op banner names only legal actions, so it no longer says “flip a Jester” where a Jester is a hand card.
- **Consequence**: the result describes the table, not a solo medal, and the last play is seen before the sheet. `matchAwards`, `wonLine({coop:true})` and the room filters are unit-tested.

### D-82 The merge gate runs in parallel lanes, and its clock is injectable for tests
- **Status**: Accepted (0.9.3)
- **Context**: the 27-stage gate took 4 m 23 s, measured. Stages ran one after another on a 12-core host; the disconnect browser check sat through the real 60 s bot-fill grace, and the stream-drop check waits a real 12 s watchdog. The operator asked why, and for it to be faster.
- **Decision**: run the stages in five lanes (fast, ui, table, gecko, net) after a sequential `gofmt`/`vet`/`build` preflight. Every lane owns an ephemeral port, a temp data directory and a Chromium profile. The bot-fill grace becomes a `Store` field with a test-only `-botfill` flag; the shipped default stays 60 s. A run that lists fewer than 27 rows fails, and `GATE_FAST=1` runs only the fast lane for local iteration.
- **Consequence**: the same 27 checks finish in about a minute (47–99 s measured). The gate no longer masks a failing JS unit test (`pipefail`), and it fails on a missing screenshot. The real bot-fill path is still exercised — with a 3 s clock, not a real minute.

### D-83 Royal defeat and arrival are table news, in both modes
- **Status**: Accepted (0.9.3)
- **Context**: a royal's fall was visible only as a slot swap and a history line. In co-op the reveal system existed for plays and payments and deliberately skipped the player who moved, because their own cards already left their hand.
- **Decision**: show a panel for every `defeat` (the fallen royal) and every `enemy_reveal` (the incoming royal), with the royal's card art, in solo and co-op. Royal news is seatless and reaches every player, including the striker; attack, defence and Jester panels keep the actor skip. The panel holds 1.3 s and fades in place; one persistent live region announces it; the reveal queue drops older non-royal panels first.
- **Consequence**: no engine, wire or replay change — the events already existed. The solo path reads the action's events, the co-op path the fresh suffix of a sequence frame, so neither can double-show. A new coop test pins that a join does not replay the log.

### D-84 A cleanup release may not change behaviour, and its evidence is the bench line
- **Status**: Accepted (0.9.5)
- **Context**: a whole-tree review found dead code, failure-path bugs and structural drift after four rapid feature releases. The operator asked for a cleanup that is provably the same game.
- **Decision**: 0.9.5 accepts only behaviour-preserving edits — no rule, RNG stream, action codec, wire shape or replay-format change. The acceptance evidence is (a) the full 27-stage gate, (b) the golden/hash/replay tests unchanged, and (c) `coopbench -n 2000 -seats 3 -seed CAPCHECK -policy human-engine` printing the same result line before and after. Anything behaviour-visible is deferred, with its reason recorded in the same ledger.
- **Consequence**: the release removes ~1,200 lines of dead or duplicated code and fixes failure-path bugs without touching how the game plays. The one structural split (`room.go` into three files) is pure function movement.

### D-85 The client cursor layer is removed; the server route stays
- **Status**: Accepted (0.9.5)
- **Context**: 0.9.1 removed the felt, orbs and dots markup, but left ~255 lines of client code (`wireCursor`, `postCursor`, `renderDots`, `pointerFrame`, `sendCursor`) and a `playerDots` preference behind. `wireCursor` was never called, the dot layer found no `#dots`, and the CSS described elements that no longer exist.
- **Decision**: delete the client half and its CSS; keep the server cursor route and presence frames. The route is the recorded protocol surface and costs one map entry; the client never posts.
- **Consequence**: no pointer traffic, no dead preference, ~255 fewer lines. A future pointer feature starts from the server contract, not from dead UI code.

### D-86 Pokemon is a table set, with a readability warning
- **Status**: Accepted (0.9.6)
- **Context**: co-op allowed only Official, Simple and Standard because four hands and a phone needed readable art. The operator asked for the Pokemon set at multiplayer tables.
- **Decision**: `pokemon` joins the room allow-list, in every seat count. The lobby tile carries a warning ("Detailed art: hard to read at four hands, and on a phone"), the rules say the same, and the server accepts the set. Nothing is blocked.
- **Consequence**: a host can deal Pokemon; players who cannot read it can switch their own view (D-87) or ask the host for a different set. The old "detailed sets are refused" tests became "an unregistered set is refused".

### D-87 A player may look at their own card set at someone else's table
- **Status**: Accepted (0.9.6)
- **Context**: the room's theme is authoritative and shared, which is right for the table's identity but wrong for a player who cannot read the dealt art on their screen.
- **Decision**: a Settings choice, "Card set at other tables", with **use the table's set** (default) and **use my own set (this tab only)**. The client's `boardTheme()` returns the local set when chosen; faces, backs and Jesters follow it. The server, the wire, the log and every other seat are untouched.
- **Consequence**: readability is a per-player concern without a host negotiation. The rule is cosmetic and local; no test or server route needed beyond the client render.

### D-88 The Jester pair is dealt with the game, not saved in the browser
- **Status**: Accepted (0.9.7)
- **Context**: the two Jester faces were a browser preference (`prefs.jokers`), re-derived only when the player tapped a card-set tile on the menu or in Settings. The lobby's *Card set* switch never touched it, so a Simple table could show the Official pair left over from an earlier game, and two seats could show different faces for the same J1/J2. The table reveal and the replay viewer also drew the retired per-deck `img/<set>/j1.webp` files, which Settings never lists.
- **Decision**: the deal resolves the pair server-side (`server/dealJokers`) from the registry. Official and Pokemon bring their own pair; Simple and Standard draw two different faces from the shared sheet — every catalogue id no set claims — seeded by the game seed when the browser sends no fitting pair. A pair the player picked by hand is sent with `jokersCustom` and is kept on any set. The pair travels in `Game.Jokers`, `ClientState.Jokers` and the replay header, and the client renders `tableJokers(state)` in the tray, hand, piles, zoom, reveal panel and seat rail. `/data/*.json` is served no-cache so a retired catalogue cannot persist.
- **Alternatives considered**: a hard clamp that ignored hand picks (rejected: the operator allows an explicit pick); keeping the pair client-side (rejected: seats disagree and a replay cannot record it).
- **Consequence**: every seat and the replay show the same two faces, and a generated Simple/Standard pair can never be Official or Pokemon. The pair is deliberately excluded from `StateHash`, so stored replay hashes stay valid.
- **Amends D-87**: with "use my own set", the card *faces* follow the local set but the Jester *pair* stays the table's.

### D-89 The server owns the reveal clock: no bot acts while a panel is up
- **Status**: Accepted (0.9.8)
- **Context**: the bot loop ticked every `defaultBotPace` (2.4 s), but one action can publish three panels (play, defeat, entry ≈ 4.98 s). The next bot action landed while the player was still reading, and the client's 5-panel queue started dropping reveals. A client acknowledgement was considered and rejected: a background tab throttles `setTimeout` and a spectator table may have no browser at all, so only the server can hold a table.
- **Decision**: `ApplyFor` stamps `Session.revealUntil` under the existing lock from `revealBudget(events)` — the client's own constants (1.86 s normal panel, 1.56 s royal, 8 s cap). Every actor stamps it, including humans: their plays are the other seats' news. `botStep` checks the gate before the projection and again before `ApplyFor`; the loop runs a `Timer`, not a `Ticker`, so the next action starts when the reveal clears. `Start` and `PlayAgain` reset it.
- **Alternatives considered**: a client "reveal-done" post (rejected: a throttled or absent tab stalls the table); a slower fixed pace (rejected: one number cannot fit both a single play and a royal cascade).
- **Consequence**: the gate stage `reveal pacing` (gate 28) samples the panel element against the room sequence; `TestRevealBudget`, `TestBotWaitsForReveal`, `TestHumanActionStampsReveal` and `TestPlayAgainClearsRevealGate` pin it, each with a negative control.

### D-90 A finished table's sheet offers four controls, and one is the way home
- **Status**: Accepted (0.9.8)
- **Context**: the co-op end sheet had seven flat buttons, one of which duplicated the backdrop, and none returned to the main menu — a guest's only route out was closing the tab. Solo had grown the same pile.
- **Decision**: the sheet is a pure model (`web/endsheet.js`: `endSheetModel` + an action-id contract) and the renderer only wires handlers. The controls are Play again (host) or the waiting line, one `<details>` disclosure "Log & replay" (History, Copy log, and for the host Download the log and Watch the replay, per D-47), Main menu (the existing `leaveTableAndMenu`/`abandonGame` path, sheet dismissed first), and Close. The modal's Tab trap lists `summary`, so the disclosure is a real control.
- **Alternatives considered**: keeping flat buttons with smaller labels (rejected: the count was the problem); a second "are you sure" for Main menu (rejected: the leave path already treats a gone chair as gone).
- **Consequence**: host-only controls can no longer appear for a guest (model tests), the solo sheet reads the same, and the browser checks pin the shape: `coop.endSheetCompact`, `coop.endSheetLogGroup`, `endsheet.mainMenuGoesHome`, `endsheet.logGroupFocusable`.

### D-91 A refactor is accepted only with a number attached
- **Status**: Accepted (0.9.8)
- **Context**: the 0.9.5 sweep proved the method (D-84) but left no benchmark set, so "cheaper" was unverifiable and several dedupes were still open. The 0.9.8 audit also caught this session's own failure mode: a ledger that claimed edits a backup-restore had wiped — two reviewers found it independently.
- **Decision**: the acceptance contract for the sweep is (a) the full gate, (b) golden/hash/replay tests unchanged, (c) the identical 2000-game `coopbench` line, (d) a benchmark set run before and after on the same host with medians recorded, and (e) a ledger where every row is either in the tree or in the deferral list with a reason — the repo's "code wins, the doc is the bug" rule applied to the work's own notes. An automated audit row is a lead, never a finding; every accepted row is re-read in the source.
- **Consequence**: six benchmarks exist (`perf_test.go` in engine, server and bot); `ProjectFor` and `ProjectFrame` each allocate one object less; every deferral in the sweep ledger can be revisited with a number, not a feeling.

### D-92 The sheets share one contract: real hiding, sticky actions, a 44px phone floor
- **Status**: Accepted (0.9.9)
- **Context**: the operator asked for the lobby-style review on every sheet behind the board. The measured panel found that a closed `<details>` was still hit-testable in this Chromium (an invisible button overlapped a visible one), that the two long sheets kept their primary actions below a 900-2600px internal scroll, and that the nav's 36px rows were the last sub-44px touch targets on the phone.
- **Decision**: a closed disclosure hides with `display: none` (one engine-independent line, checked by `coop.endSheetClosedIsHidden`); Settings and Rules get sticky `.sheet-actions` footers scoped by sheet id; the phone touch floor is 44px everywhere, including nav rows, nav pills and the drawer's icon close; a destructive exit is the last item in a menu, never the first; end-card boilerplate renders quieter than the result it follows.
- **Alternatives considered**: a global sticky footer (rejected: the short sheets never scroll, and the end sheet's own row must not gain a hairline); hiding the closed group with the `content-visibility` the engine should have applied (rejected: it demonstrably does not here).
- **Consequence**: `elementFromPoint` returns nothing inside a closed group; Save/Cancel and Got it are visible at scroll-top on both widths; the menu reads doors-then-exits, matching D-90.
