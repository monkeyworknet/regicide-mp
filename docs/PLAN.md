# PLAN — Regicide Web v0 (solo plan; historical)

> **Status: historical.** This is the v0 solo plan (rev4). The co-op build is designed in
> `docs/MP-PLAN.md`. Where this plan and `docs/DECISIONS.md` disagree, DECISIONS wins.

Status: **rev4** — rev3 resolved the P0/P1 findings from the pre-build reviews; rev4 records what
shipped in rounds 2–6 so the plan stops lagging the code.
Binding scope: `docs/DECISIONS.md` (scope answers and ADRs — wins on conflict).
Normative rules source: `docs/research/01-rules-dossier.md` (matrix `R-001…R-060` = its Appendix B)

---

## 1. Scope

Server-authoritative, **solo-only** web Regicide. Every operator requirement traces to a phase and a test.

| # | Requirement (DECISIONS Part A) | v0 approach | Phase | Verified by |
|---|---|---|---|---|
| R1 | Solo only, structured for multiplayer later | Seat-agnostic engine (`Players []Player`, `ActiveSeat`), server owns state | 1–6 | `R-056` seat round-trip, API has no solo-only route |
| R2 | Card sets, Pokémon / modern (rev4: **four** sets — modern, simple, pokémon, 8bit) | One card component, art per set from the generated registry (ADR-005, D-13) | 8, 12 | `ui.themeRegistry` + per-set `--check` gate stages |
| R3 | Sessions, concurrent solo players | `POST /api/sessions`, per-session JSON snapshot, resume, GC | 6 | API tests incl. `-race` concurrency |
| R4 | Damage tracking + counters (rev4: dice/Dicier dropped; tabular numerals instead) | Chips with tabular figures, no webfont (D-12) | 9 | `ui.healthCountsDown`, `ui.attackCountsDown`, credits provenance |
| R5 | Full rules + optional rules in Settings | Engine `RuleOptions` + dossier matrix | 1–4 | one Go test per `R-0xx` |
| R6 | Mobile friendly, desktop uses space | Token-based responsive spec (Appendix A); rev4 adds tap-to-select, the peek strip, a pinned action bar and long-press to read (D-14) | 8 | screenshots 390×844 + 1440×900, `ui.peekStrip`, 8-card portrait test |
| R7 | Seed on launch, reveal at end if random | Seed input, `?seed=`, hidden during random play, copy at end | 6, 11 | sanitisation leak test, end-screen test |
| R8 | Gold/Silver/Bronze leaderboard, names, random seeds only | Server-computed eligibility, top-N per tier, 200 rows per tier cap | 7, 11 | eligibility matrix test, `TestLeaderboardCapsPerTier` |
| R9 | Sound effects + disable | 12 supplied + 7 synthesised, AudioManager (Appendix D) | 10 | audio manifest test, unlock test |

**Non-goals for this solo tree:** accounts, online multiplayer, hot-seat, achievements, native app,
i18n, PWA/offline, public deployment, Pokémon asset redistribution, TLS. (The `regicide-mp` binary
serves co-op separately; D-31.)

### Rev4 deltas (rounds 2–6, already shipped — do not re-plan)

| Area | Rev3 said | Reality now | Authority |
|---|---|---|---|
| Card sets | two (Pokémon, modern) | four: `modern`, `simple`, `pokemon`, `8bit`; backs are picked automatically per set | D-13, D-15 |
| Counters | Dicier webfont | tabular numerals on poker-chip art; no webfont is shipped | D-12 |
| Card backs | one back | ten backs, registry-generated, validated by the gate | D-15 |
| Turn history | end-of-game log | live drawer, turn-stamped, copy-as-text, `role=log` live region | D-09 |
| Optional rules | four switches | five: solo Jester, exact damage, yield, overpay, **chaos** (leaderboard-ineligible) | ADR-006, D-11 |
| Mobile input | tap opens a read-only sheet | tap selects, peek strip reads, long-press zooms, action bar pinned | D-14 |
| Enemy panel | four counters (health, damage, shield, incoming) | two counters read from the enemy's side: health `left/total`, attack `landing/base` | D-18 |
| Testing | Go tests + manual smoke | Go tests, `node --test` UI unit tests, a scripted smoke harness, a pre-commit hook | D-20 |
| Rounding out | — | brand crown, credits ledger, leaderboard cap, bounded caches, derived theme whitelist | D-19, D-21 |

---

## 2. Verified asset facts (do not re-derive)

- `playing cards modern A4.pdf` → vector, 6 pages × 9 = **exactly the 54-card Regicide deck** (A,2–10,J,Q,K ×4 + 2 Jokers). Thin rounded double frame, **rank embossed inside the corner suit pip**, mirrored bottom-right pip, **blank white middle**. Clubs/spades black, hearts/diamonds red. We author only the middle text.
- `Pokemon_RageIced_*_of_4.pdf` → 14 pages × 9 rasters (750×1050 @300 dpi). `pdfimages` extracts all 126 exactly. 60 unique hashes = 54 faces + 1 back (×54 copies) + duplicated info pages + HP/ATK tracker + "Champion Red". Licence constraint → private/self-host only (ADR-007).
- `Dicier v1_5_4.zip` → 10 families incl. woff/woff2, CC BY 4.0. Glyphs: dice/dominoes/cards/coins only — **no HP or damage icons** ⇒ digit glyphs drive counters; card codes used in help text.
- `CrispCards_Free_Sampler.zip` → 20 ogg/wav, embed-ok, no pack redistribution. Gaps (enemy attack, defeat, shield, jester, combo, UI click) synthesised with `numpy` → `ffmpeg`.
- Toolchain: Go 1.26.9, Python 3 (+numpy, PIL), ffmpeg, Chromium.

---

## 3. Rules locked for v0 (ADR-006 is the binding copy)

1. **Attack area ≠ discard pile.** Played cards stage in `AttackArea` until the enemy dies. Hearts cannot touch them (`R-018`).
2. **Hearts before diamonds**, always, in one play (`R-014`, `R-015`).
3. **One parameter per play**: total attack value `X`. Clubs double `X` (boolean, not stacking); spades add `X`; hearts/diamonds use `X`.
4. **Shield accumulates unconditionally** — `Shield += X` even while the enemy's suit immunity suppresses *other* spade effects; immunity only gates the step-4 application (`R-029`, dossier §6.1). Solo never cancels immunity, multi-seat will.
5. **Immunity suppresses only the matching suit's power**, never the damage total (`R-027`, `R-028`).
6. **Shield is per enemy**: cumulative across turns, reset on reveal, incoming attack floors at 0 (`R-023`–`R-026`).
7. **Exact kill ⇒ enemy face-down on top of the tavern deck**; overkill ⇒ discard. Damage counters reset per enemy, overkill never carries (`R-030`–`R-035`).
8. **Defeat cascade:** (i) enemy away, (ii) attack area → discard, (iii) reveal next castle card, (iv) killer starts a new turn at step 1 and skips step 4. **Win check precedes any reveal** (`R-046`).
9. **Step-4 payment stops at the threshold**; `allowOverpayDiscard` off (`R-039`). When on, `EndPayment` is required.
10. **Empty hand ordering:** window A (Jester flip) is offered first, then the cannot-act check runs — so a flip may rescue an otherwise empty hand; if the hand is still empty at the end of window A the result is `Lost{CannotAct}` regardless of settings (`R-043`). `CannotAct` is re-evaluated on **every** entry into `Step1Await` (setup, post-kill, post-flip). Pending operator delta D-1; if the operator says "immediate loss", the check moves ahead of window A.
11. **Tavern is never reshuffled from the discard**; running out mid-draw is penalty-free (`R-021`).
12. **Solo yield disabled by default** (`R-044`). When enabled: a yield with required damage 0 is illegal (keeps termination), and empty hand is still a loss.
13. **Solo Jester**: window A (start of step 1) and window B (start of step 4), reopens after every kill, one Jester per activation including no-ops, refill target **`min(HandLimit, len(Tavern))`** drawn from the top after discarding the hand, never counts as a diamond draw, never cancels immunity (`R-047`–`R-054`, dossier §5.2/§7 #14).
14. **Hearts mechanics pinned:** `n = min(X, len(Discard))`; if `len(Discard) == 0` it is a no-op that **consumes no RNG**; the `n` moved cards go **under** the tavern deck in shuffled order; `AttackArea` is excluded (`R-015`–`R-018`).
15. **Tier from Jesters used** (0 Gold / 1 Silver / 2 Bronze), only on a win (`R-055`).

---

## 4. Architecture

### 4.1 Repository / GOPATH layout

Work in worktrees off `main`; `artifacts/` is gitignored and symlinked into each worktree.

```
main/
  README.md
  docs/ PLAN.md DECISIONS.md CREDITS.md rules-coverage.md research/
  scripts/gate.sh run.sh
  src/regicide/engine/        # pure rules engine (+ testdata/<R-ID>.json fixtures)
  src/regicide/server/        # HTTP, sessions, store, sanitisation
  src/regicide/cmd/regicide/  # main
  src/regicide/server/web/    # embedded UI (go:embed): index.html, app.js, theme-*.js, style.css,
                              #   data/cardtext.json, fonts/, sfx/, img/pokemon/
  src/test/                   # black-box HTTP/integration tests
  src/regicide/tools/rngref/  # independent RNG reference used to generate golden vectors
  tools/assets/               # one-shot asset pipeline (output committed)
  bin/ (gitignored)
```

```
GO111MODULE=off GOPATH=$PWD go build -o bin/regicide regicide/cmd/regicide
GO111MODULE=off GOPATH=$PWD go vet  ./src/...
GO111MODULE=off GOPATH=$PWD go test -race ./src/...
```

### 4.2 Engine — seams fixed

```go
type Suit uint8   // Clubs, Diamonds, Hearts, Spades, NoSuit (Jokers)
type Rank uint8   // Joker=0, Ace=1, Two..Ten=2..10, Jack=11, Queen=12, King=13
type CardID string // "AS".."KH", "J1", "J2"  — stable, printable, assertable
type Card struct{ ID CardID; Rank Rank; Suit Suit }

type Phase uint8 // Step1Await, Step4Pre, Step4Discard, Won, Lost
                 // (turn start resolves inside the same transition; never externally observable)
type LostReason uint8 // None, CannotAct, CannotDefend

type Player struct{ Hand []Card }

type Game struct {
    Version     int          // bump when rules/shuffle change ⇒ old sessions invalid (404)
    Rules       RuleOptions  // frozen for the session
    Seed        string
    SeedRandom  bool
    Theme       string
    Players     []Player     // seat-agnostic; len==1 in v0
    ActiveSeat  int
    ConsecutiveYields int
    Castle      []Card       // face-down stack, last = next to reveal
    Enemy       Card
    Damage      int          // vs current enemy
    Shield      int          // cumulative spade reduction vs current enemy
    ImmunityCancelled bool   // multiplayer Jester effect; always false in solo
    Tavern      []Card       // index 0 = top
    Discard     []Card       // unordered multiset as far as the client knows
    AttackArea  []Card
    MaxHand     int
    JestersLeft int
    JestersUsed int
    Step4Required int        // computed once on entry to Step4Pre (after window B), then immutable
    Step4Paid     int
    Phase       Phase
    LostReason  LostReason
    TurnNo      int
    RGNDraws    int          // audit counter for determinism tests
    Log         []Event
}
```

Enemy attack/health are **derived** (`enemyStats(rank) → {attack, health}`), never stored as mutable fields.

```go
type Rand interface{ Uint64() uint64 }           // seam: seeded ChaCha8 in prod, scripted in tests
func Transition(g Game, a Action, r Rand) (Game, []Event, error)
```

Actions: `Play{CardIDs []CardID}`, `FlipJester{}`, `Yield{}`, `PayDamage{CardID}`, `EndPayment{}`
(only when `Rules.AllowOverpayDiscard`), `ChooseNextSeat{Seat int}` (multiplayer-ready, unused in v0).

**Zero-damage step 4 auto-resolves:** when `Step4Required == 0` the engine advances out of `Step4Pre`
inside the same transition (`R-041`, `R-042`). There is no `ConfirmZeroDamage` action — the client never
has to acknowledge a zero-damage step.

Engine contract (each item is a test):
1. **Determinism** — same seed + same actions ⇒ identical state and `RGNDraws` (`R-057`, `R-058`).
2. **All powers mandatory and auto-resolved** inside `Play`; no decline action exists (`R-019`).
3. **Liveness invariant** — `Phase ∉ {Won, Lost} ⇒ len(LegalActions(g)) > 0`. Checked after every transition, and as a property over 500 random games.
4. **Non-leak** — `Snapshot(g) ClientState` hides: all tavern contents, undrawn castle cards, and the seed while `SeedRandom && !gameOver`. Test: fixed seed, marshal snapshot, assert the JSON contains none of the hidden `CardID` strings nor the seed string; discard is sent as a multiset.
5. **Card conservation** — 54 distinct `CardID`s across all zones after every action (`R-004`).

### 4.3 Server

```
POST   /api/sessions              {seed?, theme?, rules?} -> 201 {sessionId, seq, state}
GET    /api/sessions/{id}                                 -> 200 {sessionId, seq, state} | 404
POST   /api/sessions/{id}/actions {actionId, expectedSeq, action:{type,...}} -> 200 {seq, state, events} | 409 {error:"stale", state}
POST   /api/sessions/{id}/restart {seed?, theme?, rules?} -> 200 {sessionId, seq, state}
DELETE /api/sessions/{id}                                 -> 204
GET    /api/leaderboard?tier=&limit=                      -> {gold:[],silver:[],bronze:[]}
POST   /api/leaderboard           {sessionId, name}       -> {ok, rank, tier} | 4xx
GET    /api/health ; GET /  (embedded UI)
```

- `ServeMux` + manual `r.Method` dispatch; request bodies size-limited; no reflection decoders.
- Idempotency: `actionId` + `expectedSeq`; replay of a known `actionId` returns the cached response; stale seq ⇒ 409 with current state. Session mutation under a per-session mutex.
- Store: `map[string]*Session` + `sync.RWMutex`; snapshot to `data/sessions/<id>.json` (temp + `os.Rename`); lazy load on miss; GC after 24 h idle or on `Version` mismatch.
- Leaderboard: `data/leaderboard.json` `{schemaVersion, entries[]}` under its own mutex + atomic rename. Top-N computed at read. Same name+tier keeps the best run.
- Eligibility computed server-side: `SeedRandom && Phase == Won && !claimed`. One submission per session.
- Per-IP token bucket on session-create and leaderboard-post. Nickname: NFC, trimmed, 2–16 chars `\p{L}\p{N} _-`, small profanity blocklist.
- Timeouts: `ReadHeaderTimeout`, `WriteTimeout`, graceful shutdown on SIGINT/SIGTERM (lessons: 2026-09-16).

### 4.4 Seeds, RNG, pinned consumption

- Seed: 12 Crockford Base32 chars (60 bits), displayed `XXXX-XXXX-XXXX`; input normalises case, strips dashes/spaces, maps `I/L→1`, `O→0`. Any non-conforming string is SHA-256'd and treated as custom (`SeedRandom=false`).
- `rand.NewChaCha8(sha256.Sum256([]byte(seed)))`, wrapped in `Rand`.
- **Fisher–Yates direction pinned:** `for i := n-1; i > 0; i-- { j := int(r.Uint64() % uint64(i+1)); swap(i, j) }`.
- **Consumption order pinned:** setup = Kings, Queens, Jacks, Tavern (each a full Fisher–Yates); per play = Hearts (only if `len(Discard) > 0`), then nothing else. Clubs/spades/diamonds consume no RNG.
- **Golden vector from an independent implementation** (`src/regicide/tools/rngref`, written from the ChaCha8 spec, not from the engine): seed `AAAA-AAAA-AAAA` yields a committed fixture asserting the full deal — 8 hand ids, 32 tavern ids, and the first 4 enemies in order. The test also asserts `RGNDraws`, so a wrong loop direction or shuffle order fails.
- Random seeds come from `crypto/rand`; never sent to the client before `Phase ∈ {Won, Lost}`.

---

## 5. Themes

One component, two renderers (ADR-005); engine emits ids only.

```
<div class="card" data-theme="modern|pokemon" data-card="KS">
  modern:  .pip-tl (rank inside suit) · .rule (from data/cardtext.json) · .pip-br
  pokemon: <img src="img/pokemon/ks.webp" alt="King of Spades — Elite Agatha"> + overlay counters
</div>
```

- **Modern** — CSS/SVG replica of the template: double rounded frame, index pip with the rank inside the suit shape (top-left + mirrored), authored middle text (Appendix B). No invented art.
- **Pokémon** — pipeline in Appendix C: `pdfimages` → hash dedupe → hand-verified `mapping.json` → WebP (300 px hand / 750 px detail) → `img/pokemon/<id>.webp` + back. HP/damage are overlays, never baked in.
- Palette and layout tokens: Appendix A.

---

## 6. Settings

| Setting | Default | Enforced in |
|---|---|---|
| Theme (modern / Pokémon) | modern | client + session |
| Sound on/off, volume | on, 60 % | client (localStorage) |
| Solo Jester power enabled | on | engine `RuleOptions` |
| Exact-damage rule | on | engine |
| Allow solo yield | **off** | engine |
| Allow over-pay in step 4 | **off** (with `EndPayment` when on) | engine |
| Turn confirmation (select → confirm) | on | client |
| Damage/hint previews | on | client |
| Animation speed + `prefers-reduced-motion` | normal / auto | client |

`RuleOptions` are frozen per session; changing them requires a restart and the UI says so.

---

## 7. Verification

### 7.1 Engine tests
- One Go test per dossier matrix row `R-001…R-060`; **test-name convention is `TestR013_ComboPowers`** (no dash) and the ledger checker normalises `R-013` ⇄ `R013` before comparing.
- **Positions:** scenario builders in the test files (`src/regicide/engine/*_test.go`) construct `{enemy, hand, tavern, discard, castle, rules, scriptedRng[], actions[]}`; the `<R-ID>.json` per-row fixture plan was not used (only `testdata/golden-deal.json` is a file). A scripted RNG replaces `Rand` so hearts/shuffle rows are deterministic without depending on the real stream.
- **Properties** (500 random games, hard transition cap 10 000): termination; `Phase ∉ {Won,Lost} ⇒ legal actions exist`; 54-card conservation; hand ≤ `MaxHand`; counters ≥ 0; shield/damage reset per enemy; replay determinism; snapshot non-leak.
- **Win-rate sanity:** self-play over 200 fixed seeds must land in a calibrated band. The band lives in the self-play test (`selfplay_test.go`), not in a separate calibration file; it guards against rule regressions, not against difficulty.

### 7.2 Server tests
`httptest`: create/resume/action/restart; 400 on illegal action; 409 on stale seq; `actionId` idempotency; seed reproducibility across two sessions; leaderboard eligibility matrix (seeded, custom, lost, double-submit, unknown session); concurrency with `-race`; 404 after GC.

### 7.3 UI tests (mechanism fixed — ADR-008)
- Self-driving harness: `GET /?test=smoke` runs a scripted scenario against the real API inside the page, then writes `#result` JSON (`{pass, checks:[...]}`).
- Driver: `chromium --headless --virtual-time-budget=8000 --dump-dom 'http://127.0.0.1:<port>/?test=smoke'`; `gate.sh` parses the JSON and fails on any check.
- Visual: `chromium --headless --screenshot=... --window-size=390,844` and `1440,900`; `gate.sh` asserts non-blank and records the files in `artifacts/screens/` (gitignored).
- Scenario coverage: new game → play a card → damage/HP update → pay damage → flip Jester → defeat an enemy → reload-resume → theme switch → sound toggle → win path (seeded short game) → seed reveal.
- Console-error assertion: the harness fails if `console.error` fired or any request left the origin.

### 7.4 Gate — `scripts/gate.sh`, per-stage `timeout`
`gofmt -l` (empty) → `go vet` → `go test -race -cover ./src/...` (per-package 120 s) → coverage ≥ 90 % engine → `go build` → engine fixture schema check → credits test (`tools/assets/check_credits.sh`) → rules-coverage ledger check → **UI stage** → PASS/FAIL table with timings.

UI stage lifecycle (no external deps, no `pkill -f`):
1. pick a free port (`127.0.0.1:0` probe, fall back to `8124`),
2. start `./bin/regicide -addr 127.0.0.1:<port> -data $(mktemp -d)` in the background, capture `$!`,
3. poll `GET /api/health` up to 50 × 100 ms, fail the stage if it never answers,
4. run the harness twice — `--window-size=390,844` and `--window-size=1440,900` — each with
   `chromium --headless --virtual-time-budget=8000 --dump-dom "http://127.0.0.1:<port>/?test=smoke"`,
   parse `#result` JSON, require ≥ 10 passing checks in both runs,
5. take `--screenshot` per viewport into `artifacts/screens/`,
6. `kill "$PID"` in a `trap` on EXIT (never `pkill`), and remove the temp data dir.

### 7.5 Rules coverage ledger
`docs/rules-coverage.md` maps every rulebook clause (`rules-raw.txt`, quoted) → `R-0xx` ids. `gate.sh` asserts: every `R-0xx` in the dossier matrix appears in the ledger **and** in a Go test name; no ledger row cites a missing test.

---

## 8. Chunk plan


| Phase | Deliverable | Red tests first |
|---|---|---|
| 0 | Scaffolding: GOPATH build, `scripts/gate.sh` (all stages stubbed but running), hello-server + `/api/health`, `CREDITS.md` check, **30-min Pokémon mapping spike** (verify 54 faces are identifiable) | gate green on skeleton |
| 1 | Engine base: types, `Card.ID`, decks with pinned shuffle order, `Rand` seam, phases, `RuleOptions`, `Snapshot`, `LegalActions` skeleton; `src/regicide/tools/rngref` + golden fixture | R-001–R-003, R-056, R-057, R-004, R-005, R-059 |
| 2 | Steps 1–3: legal-play generator, singles/combos/companion pairs, hearts→diamonds→clubs→spades pipeline, damage, defeat cascade, reveal | R-006–R-023, R-027, R-028, R-030–R-035, R-058 |
| 3 | Step 4 + endgame: required/paid, threshold stop, shield floor, loss paths, yield option, exact-kill placement, win | R-024–R-026, R-029, R-036–R-046, R-060 |
| 4 | Solo Jester + tier + option interactions | R-047–R-055 |
| 5 | Property/self-play harness finalisation + calibration record | R-004, R-005, R-059, R-060 hardening |
| 6 | Server: sessions, API, seq/idempotency, persistence, GC, sanitisation, graceful shutdown, `-race` | API matrix |
| 7 | Leaderboard: store, eligibility, rate limit, top-N | eligibility matrix |
| 8 | Client shell + modern theme + responsive spec (Appendix A) + `cardtext.json` (Appendix B) | UI smoke ≥ 6 checks, screenshots, 8-card portrait case, **visual parity check**: render one card (e.g. `10♥`) from the supplied PDF at the same pixel width next to the CSS card, compare pip position and frame inset by eye, commit the comparison screenshot to `artifacts/screens/` |
| 9 | Dicier subset, counters, animations, a11y announcements (Appendix E) | counter/font/aria checks |
| 10 | Audio manifest + AudioManager + settings (Appendix D) | unlock, preload, mute persistence, manifest↔files check |
| 11 | End screen: result, tier, seed reveal/copy, leaderboard submit, share link; resume contract (ADR-009) | hidden-seed, eligibility UX, 404-resume |
| 12 | Pokémon theme: pipeline, `mapping.json`, WebP, renderer, toggle | 54/54 mapping, weight budget, toggle persistence |
| 13 | Polish: credits screen, empty/error states, README, run script, a11y pass | credits, 404s, aria |
| 14 | Final gate, review fanout, merge `v0` → `main`, prune worktrees | full gate green |

Per chunk: red test → green → gate → review with no context on the change → fix → granular commit.

---

## 9. Risks

| Risk | Mitigation |
|---|---|
| Rules ambiguity | Locked in ADR-006 with dossier citations; remaining player-facing choices are Settings toggles (§11). |
| Pokémon licence/fan assets | Private/self-host only, `CREDITS.md` provenance, no public deployment without operator sign-off. |
| `pdfimages` mapping errors | Phase 0 spike + hand-verified `mapping.json` + 54/54 test. |
| GOPATH/no-deps limits | Zero deps by design; `go:embed`; gate script encodes every command. |
| Review quality | Review every proposal against the code; one retry, then do the work directly; drop a reviewer that misses a pattern of real findings. |
| Client/server desync | Full sanitised state per response, `expectedSeq` + `actionId`, client animates from events only. |
| Engine dead states | Liveness invariant + transition cap property. |
| Leaderboard cheating | Server-authoritative state, server-computed eligibility, one submission, rate limits. |

---

## 10. Definition of done (machine-checkable)

1. `scripts/gate.sh` exits 0 on a clean checkout with `artifacts/` present; its table shows every stage PASS with timings.
2. A scripted API session reconciles to `Phase=Won` using a committed action list, and a second committed script reaches `Phase=Lost/CannotDefend` (see `src/regicide/test/testdata/`).
3. Every `R-001…R-060` appears in a Go test name and in `docs/rules-coverage.md`; ledger check green.
4. UI self-driving smoke reports ≥ 10 passing checks at 390×844 **and** 1440×900; both screenshots non-blank.
5. Leaderboard API test matrix green: random-seed win accepted once, seeded/custom/lost/double-submit/unknown rejected.
6. Sanitisation test proves neither hidden card ids nor the seed appear in any client payload before game end; the end screen returns the seed and a copy control (asserted in the UI harness).
7. `tools/assets/check_credits.sh` green: every committed asset path is in `CREDITS.md`.
8. `README.md` documents run (`./scripts/run.sh`, port 8123), build, test, asset regeneration, licensing.

---

## 11. Open questions — deltas only (Part A answered; `DECISIONS.md` is binding)

| ID | Question | Default if unanswered |
|---|---|---|
| D-1 | Empty hand at start of step 1 with Jesters left: may window-A flip run first, or is it an immediate loss? | **Flip first** (window A is "start of step 1"), then loss if the hand is still empty. |
| D-2 | `allowSoloYield` on: a yield with required damage 0 is illegal (prevents a stalemate loop). Accept? | **Yes, illegal**; empty hand still loses. |
| D-3 | `allowOverpayDiscard` on: add an explicit `EndPayment` action? | **Yes**, off by default. |
| D-4 | Leaderboard: one global board with theme shown per entry, or per-theme boards? | **One global board.** |
| D-5 | Name entry: prompt at win only, or persistent player name in Settings? | **Prompt at win only.** |
| D-6 | Pokémon assets: commit to git (private repo) or keep out? | **Commit** (ADR-007 constraints apply). |

---

## 12. Review traceability

| Review finding | Resolution |
|---|---|
| ZC-1 scope answers unattributable | `docs/DECISIONS.md` Part A (verbatim Q&A) + ADRs; §11 reduced to deltas |
| ZC-2 no UI-test mechanism | ADR-008, §7.3 |
| ZC-3 single-seat engine | ADR-002, §4.2 `Players/ActiveSeat/ConsecutiveYields`, R-056 assigned to Phase 1 |
| ZC-4 no way to end Step 4 with overpay | `EndPayment` action, §3.9, R-039 |
| ZC-5 no RNG test seam | `Rand` interface, §4.2, §4.4 |
| ZC-6 no fixtures | `testdata/<R-ID>.json` + schema test, §7.1 |
| ZC-7 cardtext.json unspecified | Appendix B (schema + 54 entries + test) |
| ZC-8 no layout/design spec | Appendix A (tokens, grid areas, fan formula, palette) |
| ZC-9 licence artefacts missing | `docs/CREDITS.md`, gate check, Phase 0/13 |
| ZC-10 DoD not measurable | §10 rewritten as 8 mechanical checks |
| ZC-11 resume contract | ADR-009 |
| ZC-12 phase/test-range mismatch | §8 re-cut row by row |
| ZC-13 sequencing risks | `RuleOptions` + property harness in Phase 1, mapping spike in Phase 0, per-stage timeouts |
| ZC-14 `Card` cannot identify cards | `CardID` in §4.2 |
| RE-1 no RNG seam | §4.2 `Rand`, pinned consumption order §4.4 |
| RE-2 no step-4 state | `Step4Required/Step4Paid` + `EndPayment` |
| RE-3 non-terminal dead state | Liveness invariant §4.2.3, re-check on every `Step1Await` entry (§3.10) |
| RE-4 wrong refill formula | §3.13 `min(HandLimit, len(Tavern))` |
| RE-5 orphans R-056/R-058 | Assignments in §8 |
| RE-6 shield-immunity asymmetry | §3.4 unconditional accumulation |
| RE-7 yield termination | §3.12, D-2 |
| RE-8 `TurnStart` no action | Turn start resolves inside the transition (§4.2 `Phase`) |
| RE-9 hearts clauses missing | §3.14 |
| RE-10 golden vector self-referential | `src/regicide/tools/rngref` independent reference, §4.4 |
| RE-11 sanitisation names wrong secret | §4.2.4 hidden set + concrete test |
| RE-12…17 P2 cleanups | `Card`/`Rank`/`Suit`/`LostReason`/`ImmunityCancelled`/derived enemy stats/calibration band/consecutive yields |
| WEB-1 layout unspecified | Appendix A |
| WEB-2 cardtext undefined | Appendix B |
| WEB-3 Pokémon pipeline gaps | Appendix C |
| WEB-4 Dicier underspecified | Appendix E |
| WEB-5 audio manifest missing | Appendix D |
| WEB-6 resume path | ADR-009 |
| WEB-7 a11y gaps | Appendix A §A.6, §7.3 |

---

## Appendix A — UI spec

### A.1 Layout tokens
| Token | Value |
|---|---|
| `--card-aspect` | `2.5 / 3.5` |
| `--card-w` | `clamp(58px, 15vw, 96px)` (handled by grid area) |
| `--card-w-hand-desktop` | `clamp(88px, 8vw, 116px)` |
| `--gap` | `clamp(6px, 1.2vw, 14px)` |
| `--radius` | `10px` |
| `--tap` | `44px` minimum |
| breakpoints | `<560` portrait phone · `560–1023` tablet/landscape phone · `≥1024` desktop · landscape-short `(max-height: 470px)` |

### A.2 Palette (dark felt, cards stay white)
`--felt:#12312a`, `--panel:#1b4038`, `--panel-2:#245046`, `--ink:#f2f0e6`, `--ink-dim:#a9c2b8`,
`--gold:#d4a017`, `--danger:#c0392b`, `--heal:#3fa66b`, `--card-bg:#fbfaf6`, `--card-ink:#141414`,
`--red-suit:#c0392b`, `--black-suit:#1b1b1b`.

### A.3 Grid areas
Portrait (`<560`): `enemy` (castle count + enemy card + HP/damage/shield counters) → `piles` (tavern count, discard top, attack area) → `hand` → `actionbar` (Flip Jester · Yield · Confirm · Settings · Restart).
Landscape-short: two columns — left `enemy`+`piles`, right `hand`+`actionbar`.
Desktop (`≥1024`): `grid-template-areas: "left board right" "hand hand hand"`; `left` = castle/tavern/discard, `board` = enemy + attack area, `right` = event log + settings shortcuts.

### A.4 Hand fan (n cards, container width `W`)
`step = min(card_w * 0.72, (W - card_w) / max(1, n-1))`; card `i` offset `left = i*step`; rotation
`rot = clamp((i - (n-1)/2) * 2deg, -8deg, 8deg)`; `transform-origin: 50% 120%`; `z-index = i`.
Selected card: `translateY(-14%)` + 2 px `--gold` outline + `aria-pressed="true"`.
Portrait with 8 cards at 390 px: `card_w = 58`, `step ≈ 41.7` → total 349 px ≤ 100 % of `W - 2*gap`. No
card is ever smaller than 58 px; tapping the selected card again opens the detail sheet.

### A.5 Interaction state machine (client)
`idle → selecting(cards) → ready(legal) → submitting` plus `detail(card)` overlay. Tap toggles selection;
the primary button is disabled until `LegalActions` contains the current selection; tap-to-select is the
only mechanism (no drag) — simpler and touch-safe. Step-4 payment always sends one `PayDamage` per card.
Keyboard: `←/→` move focus, `Space` select, `Enter` confirm, `Esc` clear.

### A.6 Accessibility acceptance criteria
Every counter is `role="status" aria-live="polite"` with a text alternative ("Enemy health 20, damage 13");
suits are never colour-only (glyph + `aria-label` "spades"); all interactive elements ≥ 44 px and have
`:focus-visible`; contrast ≥ 4.5:1 for text, ≥ 3:1 for large glyphs; `prefers-reduced-motion` disables
transforms; the card detail overlay traps focus and restores it on close.

---

## Appendix B — `cardtext.json`

Location `src/regicide/server/web/data/cardtext.json`; source of truth, committed; 54 entries keyed by `CardID`.

```json
{
  "version": 1,
  "cards": {
    "AS": { "title": "Ace of Spades — Animal Companion",
            "value": "1 to attack · 1 when discarded",
            "power": "Shield: reduce this enemy's attack by the play total.",
            "note": "Play alone, or pair with one other card (never a Joker). Suit powers are mandatory." },
    "J1": { "title": "Joker", "value": "0", "power": "—", "note": "Solo: flip a Jester to discard your hand and refill." }
  }
}
```

Authoring rule: text is **derived** from `docs/research/01-rules-dossier.md` §1.2/§3 (value line, suit-power
line, one usage note) — never copied from the PDF art. Test: 54 keys, ids match the canonical deck exactly,
no empty fields, every string ≤ 160 chars. Drafted in Phase 8, then reviewed against the dossier.

---

## Appendix C — Pokémon pipeline

1. `pdfimages -png` all 4 PDFs → 126 images → md5-of-resized dedupe → 60 unique (verified this session).
2. `mapping.json` (hand-verified against a contact sheet, committed): `{ "<hash>": {"id":"KS","name":"Elite Agatha","alt":"King of Spades — Elite Agatha"} }` for 54 faces + 1 back.
3. Encode: hand size `width=300`, detail `width=750`, WebP `q=82`; budget ≤ 30 KB per face, ≤ 1.8 MB total; the back is a single 300 px image used for all face-down cards.
4. Test: 54 ids resolve to existing files, no orphan files, total bytes within budget, every image has non-empty `alt` from `mapping.json`.
5. Licence banner shown in the theme's credits entry (ADR-007).

---

## Appendix D — Audio manifest

`src/regicide/server/web/data/audio.json`, `{event: [file…]}`; the client picks a random take when a list
has >1 entry. Gate test: every listed file exists; every committed sfx file is listed.

| Event | File(s) | Source |
|---|---|---|
| `deal`, `shuffle` | `shuffle_riffle_01`, `card_deal_01` | supplied |
| `play` | `card_flip_table_01`, `card_place_wood_01` | supplied |
| `draw` | `card_draw_fast_01` | supplied |
| `discard` | `card_flip_01` | supplied |
| `damage` | `hit_light_01` | supplied |
| `heal` | `heal_01` | supplied |
| `reveal` | `card_reveal_rare` | supplied |
| `turn` | `turn_start` | supplied |
| `victory` | `victory` | supplied |
| `leaderboard` | `slot_win_small_01` | supplied |
| `enemy_attack` | `synth_enemy_attack` | synthesised |
| `defeat` | `synth_defeat` | synthesised |
| `shield` | `synth_shield` | synthesised |
| `combo` | `synth_combo` | synthesised |
| `jester` | `synth_jester` | synthesised |
| `ui` | `synth_ui_click` | synthesised |
| `stalemate`/`tier` | `synth_tier` | synthesised |

AudioManager: fetch + `decodeAudioData` at boot (≤ 17 files, ≈ 250 KB), single `AudioContext` created on
the first user gesture, gain node with volume, voice cap 6 + soft limiter, `mute` and `volume` in
`localStorage`, and **no sound before the first gesture** (mobile unlock).

---

## Appendix E — Dicier usage

- Families: **Round-Heavy** for counters and headings, **Block-Dark** for damage badges (both subset).
- Subset glyphs: digits `0`–`9`, `+`, `−`, `∞`, and the card codes used in help text (`ACE_HEARTS`…
  `KING_SPADES`, `_JOKER`, suit codes). Record the subset as the licence-noted modification.
- CSS: `@font-face { font-family: Dicier; src: url(fonts/dicier-round-heavy.woff2) format("woff2"); font-display: fallback; }`, `font-feature-settings: "liga" 1, "calt" 1; text-transform: none;` and `unicode-range` limited to ASCII digits/punctuation.
- Always pair the glyph with text: `role="status"` + `aria-label="damage 13 of 20"`.
- Fallback: if the font fails to load, counters render in the system font with identical numbers (a
  `document.fonts` check adds `.no-dicier` to `<html>`, and the gate fails if the fallback is active).
