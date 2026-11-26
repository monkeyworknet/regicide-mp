# MP-PLAN — co-op Regicide, 2–4 players, on :8321

> **Status: reference.** Binding for the co-op design; `docs/DECISIONS.md` wins on conflict.
> Line references are against this repository at v0.6.0.

---

## 0. Two projects, one game

| | Solo tree | This tree |
|---|---|---|
| Port | **8123** | **8321** |
| Modes | solo only | **solo + co-op** (combo) |
| Version | **0.6.0, frozen** | starts at 0.7.0 |
| Card sets | all four | solo: all four · co-op: **simple, 8bit** |
| Data dir | its own | its own (never shared) |

- **Solo is locked.** The solo-only tree stays at `d9656c2` (tag `solo-locked-v0.6.0`) and keeps
  serving `:8123` exactly as it is. No co-op code lands there; only a crash fix would, and only by the
  operator's word.
- **This repository carries both modes.** Solo play must keep working byte-for-byte the same (same
  client, same API, same leaderboard), because the combo instance is the one the operator runs for a
  mixed group: someone may play solo while a table of four plays co-op.
- **Separate data directories.** Two instances, two `-data` paths, so a co-op room can never appear in a
  solo session list and the solo leaderboard stays a solo leaderboard.
- **The clone keeps history.** Every solo decision (D-1…D-30) is still binding here; this plan only adds.

---

## 1. Operator rulings for this round

These four are inputs, not proposals. Each gets an ADR.

| # | Ruling | Consequence |
|---|---|---|
| R-1 | **Co-op allows only `simple` and `8bit`.** | The room setup offers two tiles; the server rejects any other set for a co-op room (`400 bad_theme`). Solo keeps all four. |
| R-2 | **Every seat broadcasts a coloured orb** for its pointer (red, blue, green, black), **hidden while that pointer is over the owner's own cards.** | A presence channel: server-relayed, rate-limited, normalised coordinates. Hands stay closed. |
| R-3 | **The played area must grow** for 2–4 players. | Played cards group per seat, bigger art, own row budget. Hand gets the remaining vertical space. |
| R-4 | **Combo instance on :8321**, solo frozen on :8123. | See §0. |
| R-5 | **Nobody types a name.** The server assigns one when a seat is created or joined, from a pool of European royalty — mostly English and French, with only the plain German names. No multi-player leaderboard. | `royalnames` package, one name per seat, reserving four up front so a table never repeats. See §6. |
| R-6 | **The orb shows only over the shared board** — never while its owner touches their own cards. | An explicit `hidden` flag the client sets on any own-hand interaction (hover, tap, drag, sheet), not a server inference. |
| R-7 | **A room is a waiting room for up to four**, not a declared player count. Start with whoever is sitting down (2+); more may keep joining until the start. | Capacity is fixed at 4; `NumPlayers` is `len(seats)` at the deal, so hand limit and tavern Jesters follow the real count. The lobby shows the setup table it would use right now. |
| R-8 | **A disconnect pauses the table; it never shrinks it.** | No auto-play, no auto-yield, no N−1. Seat is `away` after 90 s of silence and the host may hand it to a new device after 5 min. |

The orb rule is the game's communication language made visual: you may point at the castle, the tavern,
the attack, another player's play — never at your own hand. That matches the rulebook's "you may not
reveal the contents of your hand" and gives the table a way to say *"someone take the spades"* without
typing a word.

---

## 2. Rules: what co-op needs

### 2.1 Already modelled (no engine work)

The engine has been seat-shaped since round 1: `Game.Players []Player` and `ActiveSeat`
(`engine/state.go:156-157`), the 2/3/4-player setup table (`JestersInTavern`, `MaxHandSize`,
`state.go:208-236`), deal-to-every-seat (`game.go`), shared castle/tavern/discard/attack area, per-enemy
damage and shield, round-robin diamond draws (`game.go:118`), step-4 discarding limited to the active
seat (`transition.go`), kill-starts-next-turn, and the multiplayer yield path (`transition.go:233`).
Solo is simply `NumPlayers == 1`.

### 2.2 Must be added

**(a) Jester → "choose who goes next".** Today a Jester play ends the turn (`transition.go:99-107`) and
`ChooseNextSeat` is rejected (`transition.go:30`). Co-op needs a blocking choice:

```go
type SeatChoice struct {
    By   int   // only this seat may act
    Next []int // every seat, including By
}
// Game gains: SeatChoice *SeatChoice
```

`applyPlay`'s Jester branch sets it and does not advance; `applyChooseNextSeat` validates, sets
`ActiveSeat`, clears it, and enters a fresh turn. While pending, `LegalActions` returns only the seat
options, so the liveness invariant still holds. Authorisation stays outside the engine (R-056).

**(b) Yield bookkeeping is per seat.** `ConsecutiveYields` (`state.go:160`, tested at
`transition.go:240`) is wrong for *A yields → B plays → C yields → A yields*: the counter leaves B
blocked although B's last turn was not a yield. Replace with `Player.YieldedLastTurn`, set on yield and
cleared when that seat plays. R-044 is per seat.

**(c) "Cannot act" must consider yielding.** `enterTurnStart` loses on an empty hand
(`game.go:158-161`). In co-op an empty hand is normal; the loss condition is *no card and no yield*.
Extract one predicate — `canAct() = len(hand) > 0 || yieldLegal()` — and use it for legality, yield and
turn start.

**(d) Loss attribution.** `loseWith` records no seat: add `Game.LostSeat` and stamp it in `EvLose`. Win
and loss stay table-wide.

**(e) Yield legality in co-op.** `legal.go` gates Yield on `AllowSoloYield`, `applyYield` allows it for
multi-seat. Fix legality to `(len(Players) > 1 || AllowSoloYield) && incomingDamage() > 0`.

**(f) Per-seat draw events.** `drawRoundRobin` appends silently and the caller emits one
`EvDraw{Seat: ActiveSeat}`, so other seats' diamond draws cannot be animated. Emit one event per seat
**only when `len(Players) > 1`**, keeping the solo event stream (and the golden vectors) untouched.

**(g) Viewer-scoped snapshot.**

```go
type View struct{ Seat int; OpenHands bool }
func SnapshotFor(g Game, v View) ClientState  // Hand = viewer's hand, Legal only for the actor
func Snapshot(g Game) ClientState             // = SnapshotFor(g, View{Seat: g.ActiveSeat})
```

`ClientState` gains `viewerSeat`, `players []SeatView` (`seat, name, handCount, lastPlay, yielded,
active, away`), `lostSeat`, `pendingSeatChoice`, `openHands`. A non-leak test marshals a 3- and 4-seat
snapshot for every seat and asserts no foreign hand id appears.

**(h) Version bump.** (a) and (b) change replay semantics → `engine.Version` 1 → 2. Stored v1 sessions
are discarded by the existing loader, which is correct.

---

## 3. Transport: SSE down, POST up

The first proposal chose long-poll. **Shared presence changes the arithmetic**: at 10 Hz a long-poll
means 10 polls per second per player, each its own request. One stream per seat is the honest shape.

```
POST /api/rooms/{code}/cursor   {x, y, hidden}     ≤ 12 Hz, only on movement
POST /api/rooms/{code}/actions  {actionId, expectedSeq, action}
GET  /api/rooms/{code}/stream   text/event-stream  state + presence + pings
```

Server push rules:

- One goroutine per connected seat, writing to a `chan []byte` (cap 32, oldest-dropped).
- `store.Apply` publishes to the room's subscriber list after unlocking; **one** `state` frame per seq.
- Presence is **coalesced**: one `presence` frame per ~100 ms carrying every seat's latest point, so a
  4-seat table costs 10 frames/s total, not 40.
- Heartbeat `: ping` every 15 s; `Cache-Control: no-store`; `X-Accel-Buffering: no`.
- `WriteTimeout` is 30 s (`cmd/regicide/main.go:37`) — a stream must therefore take the deadline out of
  the connection (`http.ResponseController.SetWriteDeadline`) or the server needs a `-addr`-specific
  write timeout of 0 for the stream path. Plan: `ResponseController` + a soft 10-minute reconnect in the
  client, so no request lives forever.
- **Drain:** `Server.Shutdown` closes a `done` channel; every stream handler returns, the goroutine
  exits, and `Shutdown` completes. Test: start a stream, shut the server down, assert it returns and
  the handler goroutine count falls to baseline.

Client (`web/live.js`): `EventSource` with `lastEventId` = seq; on reconnect, `GET /api/rooms/{code}`
for a full snapshot if the stream's first frame is newer than the last applied seq. Presence frames are
applied to `app.presence`, never to `app.state`, and never trigger a full `render()` — they move DOM
nodes that already exist (a render at 10 Hz would fight the selection and the animations).

Why not WebSocket: not in the stdlib, and hand-rolling RFC-6455 framing (masking, fragmentation, close
codes) buys nothing that SSE+POST does not already deliver for four players on a LAN. Why not
long-poll: 40 requests/s at 4 players versus 10 frames/s on one stream.

---

## 4. Presence

### 4.1 Data

```go
type Cursor struct {
    Seat   int     `json:"seat"`
    X, Y   float64 `json:"x,y"`   // 0..1 of the #stage box
    Hidden bool    `json:"hidden"` // over the owner's own cards, or a modal is open
    At     int64   `json:"at"`     // server clock, ms
}
```

**Normalised to the stage box, not pixels.** A phone at 390×844 and a desktop at 2560×1440 share the
same board semantics but not the same layout; fractions keep an orb over the same *thing* (the enemy
card, the tavern pile) on every screen. The client sends
`(clientX - rect.left) / rect.width` of `#stage`; the server clamps to `[-0.05, 1.05]` so a pointer on
the edge is not lost.

### 4.2 Sending (up)

- `pointermove` (mouse) and `pointermove` on touch: throttled to 83 ms, and only when the point moved
  more than 0.4 % of the stage. **A still pointer sends nothing** — the phone stops posting.
- `hidden: true` is sent once when the pointer enters the owner's own hand, peek strip, or a modal
  opens, and once when it leaves. Being over your own hand is defined by DOM containment
  (`#hand .card`, `#peek`, `.sheet`), not by coordinates.
- Idle heartbeat every 20 s so `away` detection has a clock; the server also stamps `LastSeenAt` on
  every request.

### 4.3 Receiving and drawing (down)

- One orb per other seat: 12 px filled circle, seat colour, 24 px soft halo at 22 % alpha, 1 px light
  ring so **black** stays visible on the dark board.
- Eased, not teleported: `requestAnimationFrame` lerp 0.22 per frame toward the target, so a 10 Hz
  stream reads as a smooth float.
- Fades to 0 after 3 s without movement, returns on the next frame. `prefers-reduced-motion` → no lerp,
  no fade (position updates only).
- `pointer-events: none`, `aria-hidden="true"`; the orbs are never focusable and never announced.
- The seat colour is the same one used on the seat rail, the played-card group and the turn banner, so
  "the blue player is pointing at the spades" needs no legend.
- Own orb is never drawn (a cursor is already visible as the system cursor).

### 4.4 Cost and abuse

Clamped coordinates, one message per 83 ms per seat, subscribe-time rate limit per seat (drops, no
error), and a hard cap of 4 seats per room. Total presence traffic at four players: ~48 small POSTs/s
and 10 SSE frames/s on a LAN. No storage: cursors live in memory only, never in the session file.

---

## 5. Board for 2–4 players

Screen real estate is the constraint, so the layout gives the table to the *played* area and the seat
rail, and keeps exactly one private region (the viewer's hand).

```
DESKTOP ≥1200×800
┌───────────────────────────────────────────────────────────────────────────────┐
│ ♠ REGICIDE   CO-OP · 7K3QP · turn 14 · castle 6     [History] [Rules] [Menu]   │
├────────────────────────┬──────────────────────────────────────────────────────┤
│ SEATS                  │ ENEMY        King of Spades (20)                     │
│ ● Ana   6 cards  ▲     │   ┌────┐  health 11/20  attack 20  ● shield 9        │
│ ● Ben   5 cards        │   └────┘  ⚠ spades immune                           │
│ ● Cy    6 cards        │                                                      │
│ ● Dee   5 cards        │ THE TABLE    (grows with every play, big cards)      │
│  ○ = away              │  ● Ana  9♥ 9♦           18 damage                   │
│                        │  ● Ben  5♣              5 damage                    │
│                        │  ● Cy   J1 (jester — Ana chooses who goes next)      │
│                        │  ─────────────────────────────────────────────────   │
│                        │  tavern 22 · discard 7 · attack 3                    │
├────────────────────────┴──────────────────────────────────────────────────────┤
│ YOUR HAND (6)   ◐ you are red                                                 │
│   [2♣] [3♦] [5♥] [7♠] [J♦] [A♣]                                              │
├───────────────────────────────────────────────────────────────────────────────┤
│ [ Play 2 cards · 18 damage ]   [ Yield ]   [ Deselect ]                       │
└───────────────────────────────────────────────────────────────────────────────┘

PHONE 390×844
┌───────────────────────┐
│ ♠ REGICIDE  7K3QP  ☰  │
│ ●A 6 ●B 5 ●C 6 ●D 5   │  seat strip, tap = seat sheet
│ ┌───────┐  hp 11      │
│ │  K♠   │  atk 20     │
│ └───────┘  ▲ YOUR TURN │
│ THE TABLE              │  per-seat rows, 2 visible + scroll
│ ● Ana 9♥ 9♦     18 dmg │
│ ● Ben 5♣        5 dmg  │
│ tavern 22 · discard 7  │
│  2♣ 3♦ 5♥ 7♠ J♦ A♣    │  viewer's hand only
│ [ Play 18 ]  [ Yield ] │
└───────────────────────┘
```

- Played cards in the table area are **bigger than today's minis** (72 px desktop / 44 px phone), each
  group led by its seat dot; the area scrolls vertically when a turn has four plays.
- The three piles stay a single row; the played area takes the space freed by removing the solo Jester
  tray (which does not exist in co-op).
- Hands: closed. **The seat rail shows `Name 6/6`** — count against the table limit
  (`handLimit`), with a `full` marker, because a full hand is skipped by the diamond
  draw. That is the whole of the public hand information: how many, never which. `openHands` room option exists for a
  table in one room and is off by default.
- The orbs draw over everything except the sheets.

---

## 6. How players find each other

There is one server on the LAN, so discovery is a URL and a list — not a protocol.

1. **The address.** Everyone opens the host's address on :8321, e.g. `http://regicide.lan:8321`
   (bookmark, or add it to the phone's home screen). There is exactly one instance, so there is nothing
   to discover. mDNS / Bonjour is deliberately not built: it needs a raw multicast
   responder outside the stdlib, and it solves a problem a bookmark and a static
   router entry already solve.
2. **The room list.** The start screen shows the open tables on that server:
   `Louis's table · 2/4 · waiting`, with the card set. One tap joins — the same
   request creates the seat and hands it a royal name. This is the normal path: a
   group that is already together just looks at the page.
3. **The code.** Every room has a 5-character Crockford code (`7K3QP`, no I/L/O/U
   because those are what a spoken code gets wrong). It is shown large in the lobby,
   accepted in any case, and works as a link: `?room=7K3QP`. It exists for the
   edges — a full room that later frees a seat, a link pasted into a chat, a player
   joining after the list has moved on.
4. **Returning.** The browser keeps its seat token in `localStorage` per room code,
   so a reload, a phone lock or a dead Wi-Fi link rejoins the same seat with the
   same hand. Lose the token and the seat is gone; the host may take over an away
   seat after five minutes.
5. **Not built:** accounts, matchmaking, spectators, cross-network play, mDNS.

Names are part of finding each other: because nobody types a name, the seat rail
and the orbs are the identity. A player is "the green one, Blanche" from the moment
they join (D-38).

## 6b. An open table, and what happens when someone drops

**The table is defined by who sat down.** A room opens as a waiting room for four.
Anybody may join while it is a lobby, and the host may start as soon as two players
are seated; the fourth chair can stay empty, or be filled while the others watch the
setup table update:

| Seats | Hand limit | Jesters in the tavern |
|---|---|---|
| 2 | 7 | 0 |
| 3 | 6 | 1 |
| 4 | 5 | 2 |

Those numbers are `engine.MaxHandSize` and `engine.JestersInTavern`, and the deal uses
`len(seats)` — so a game that starts with three plays exactly like a three-player
game, never like a four-player game with one empty chair. Joining marks a seat ready
(sitting down is consent); a player can hold the table with an explicit "not ready"
and the start button stays disabled until they let go.

**A disconnect pauses the table. It never shrinks it.** Removing a seat mid-game is
not a rule the game has — the hand limit (5/6/7), the Jester count already shuffled
into the tavern, the diamond draw order and the loss condition all depend on how many
players started. Continuing with N−1 would silently deal a different game, and the
table's last ten minutes would have been played under rules that no longer hold. So:

- **Detection**, in this order: the SSE stream closes (the browser retries on its
  own), the 15-second heartbeat stops arriving, and no authenticated request has
  touched the seat. Any request stamps `LastSeenAt`; a background tab is *not* away,
  because its stream is still open. Cursor posts are not needed for liveness.
- **`away` at 90 s:** the seat shows a hollow dot in its colour and the table is told
  "waiting for Blanche". The game does not advance while it is that seat's turn;
  everyone else's turns continue normally.
**The host gets three choices, not one** (D-42), offered the moment the stopped seat
is the away one (`waitingFor` in the room view):

| Choice | What happens |
|---|---|
| **Wait** | Nothing changes. The seat is `away`, the banner counts the silence, and the table resumes the instant that player's stream returns. |
| **Share the reconnect link** | The host mints `/?room=7K3QP&seat=2&rejoin=<key>` and hands it over however they like (chat, spoken, a QR later). The key lives 10 minutes, works once, and replaces the seat token — so the returning player resumes the same seat, name and hand on whatever device opens it. The host's browser keeps the link and can show it again; the server stores only a hash and a deadline. |
| **Take the seat** | After 5 minutes of silence the host may move the seat to their own new device. The seat keeps its name and hand; the old token and any live reconnect link die, so exactly one device can act. |
| **Cancel the game** | The host ends the table for everyone. The room file stays until the GC so the other seats read "the host closed the table" instead of watching the room vanish mid-turn; actions are refused with `409 game_cancelled`. |

Bots, auto-yield and "the table finishes for them" are all out: they would be the N−1
problem wearing a hat. What the host cannot do is pretend the absent player is still
there.
- **Reconnect** is the same path as a reload: `localStorage` token → full snapshot,
  never a delta, so a phone that slept for an hour comes back exact.

## 6c. Spectators

A spectator has **no seat**, so the room code is the whole invitation: `?room=7K3QP&watch=1`
(D-43). No key, no token, nothing to protect, and nothing to hand over.

- `POST /api/rooms/{code}/watch` → an **ephemeral watch id** (memory only, never
  persisted) and the public snapshot with `viewerSeat: -1`. `GET .../watch?as=N`
  answers with the board as seat N when the host allows it.
- A spectator gets the table, the seat rail, the log and the enemy — and **no hand at
  all**, not even in an `openHands` room. Legal actions are empty; the id is not a
  seat token and presenting it as one fails with 401.
- **Grey orbs.** Spectators share the presence channel with a distinct look: a
  translucent grey orb (no seat colour, no name), so a player can tell "someone is
  watching" from "that is Otto". Capped at 8 per room, clamped and rate-limited like
  a player cursor.
- **Players can turn spectator orbs off** in Settings (`prefs.spectatorOrbs`, default
  on, local to that browser — hiding the audience for yourself does not take it away
  from anyone else).
- **Two host switches** (`POST .../watching {allowed, hands}`): `allowed` turns
  spectators on or off (dropping open watchers and unadvertising the link), and
  `hands` lets a spectator **tap a player in the seat rail and see the board as that
  player** — that player's hand, their side of the table, `watchingAs` in the
  response, `legal` always empty. `hands` defaults to off, and cannot be on in a room
  nobody may watch. `watchHands` is published in the room view so the table can see
  whether the audience can read hands.
- The header shows `2 watching`, so the table knows it has an audience.

## 6d. The game log

Every game records a self-proving replay (`docs/REPLAY-FORMAT.md`, D-47): seed, rules,
seats, and one line per action with a state hash, so playback proves the file. The host
downloads it with `GET /api/rooms/{code}/replay`; a spectator never can, because a replay
holds every hand at every moment. `cmd/replay -summary` is the after-action review and
`-training` exports `(position, legal, chosen)` decisions — the corpus a future co-op bot
would learn from.

## 7. Rooms API

```
GET    /api/rooms                              open tables: {code,seats,maxSeats,lobby,theme,phase}
POST   /api/rooms        {theme,rules?,openHands?,seed?}  → {code,seat,token,seq,room}   capacity is 4
POST   /api/rooms/{code}/join                  → {seat,token,seq,room} | 409 full | 410 started
POST   /api/rooms/{code}/ready {ready}         → {state}
POST   /api/rooms/{code}/start                 [host] → {seq,state}
POST   /api/rooms/{code}/actions {actionId,expectedSeq,action}
POST   /api/rooms/{code}/cursor  {x,y,hidden}  → 204
GET    /api/rooms/{code}/stream                text/event-stream
GET    /api/rooms/{code}                       full snapshot (reconnect)
POST   /api/rooms/{code}/ping                  seat heartbeat (polling clients)
POST   /api/rooms/{code}/seats/{seat}/invite   [host] → {link,expires} one reconnect link
POST   /api/rooms/{code}/seats/{seat}/reclaim  {key} → a fresh seat token (no auth needed)
POST   /api/rooms/{code}/seats/{seat}/take     [host, after 5 min away] → a fresh token
POST   /api/rooms/{code}/cancel                [host] → table ends, seats read the reason
POST   /api/rooms/{code}/watch                 → {watchId, room, state} · no auth, read-only
GET    /api/rooms/{code}/watch                 [X-Regicide-Watch] → public snapshot
POST   /api/rooms/{code}/watch/cursor          [X-Regicide-Watch] {x,y,hidden} → 204
DELETE /api/rooms/{code}/watch                 [X-Regicide-Watch] → 204
GET    /api/rooms/{code}/replay                [host] → the recorded game (JSONL)
POST   /api/rooms/{code}/watching              [host] {allowed,hands} → spectators on/off,
                                               and whether they may view as a player
DELETE /api/rooms/{code}                       [host]
```

- Seat identity: 32-byte token, returned once, stored as SHA-256, sent as `X-Regicide-Seat: seat:token`;
  the client keeps it in `localStorage["regicide.room.<CODE>"]`. The code is not a secret.
- One global `Seq` per room, one writer (`store.Apply`), `expectedSeq` on every action, `403
  not_your_turn` and `409 stale` both carry a full state so a desynced client repairs in one round trip.
- `POST /api/rooms` validates the theme against the co-op allow-list (§1 R-1).
- No auto-play for idle seats, ever; the host may take an away seat after 5 minutes.

---

## 8. Milestones

| # | Scope | Done when |
|---|---|---|
| **M1** | Engine co-op (§2.2 a–h) + viewer snapshot | `go test -race` green; the ten co-op acceptance tests pass; a committed 3-seat action script plays to a win; solo golden vectors unchanged |
| **M2** | Rooms, seats, SSE, actions, cursor relay | API-only co-op game to a win in `src/regicide/server/room_test.go`; stream drain test; cursor rate-limit test |
| **M3** | Client: mode switch, lobby, seat rail, table, turn banner | a headless smoke plays a 2-seat game through the UI while a second seat is driven over `fetch`; solo smoke still green at 390 and 1440 |
| **M3a (done)** | Menu co-op entry (host / join by code), the lobby screen, `?room=` `?watch=1` and reconnect routing, a 1.5 s poll loop, the **seat rail** with hand counts and the crowned host (D-46), header mode label | verified in Chromium at 390 and 1440: hosting from the menu, two API joins appearing in the lobby, start, and the rail showing `Victoria 6/6 full` crowned above `Agnes 6/6` |
| **M4** | Presence orbs, colours, hide-over-own-hand, grey spectator orbs + the player switch | `coop.orbAppears`, `coop.orbHidesOverOwnHand`, `coop.orbEased`, `coop.blackOrbVisible`, `coop.spectatorOrbGrey`, `coop.spectatorOrbsOff` |
| **M5** | Played table growth, pings, away/takeover, end sheet, watch link, view-as-a-player | `coop.tableGroupsPerSeat`, `coop.pingVisible`, `coop.awayBadge`, `coop.endSheetNamesTheLoser`, `coop.seatRailHandCount`, `coop.watchLinkCopies`, `coop.spectatorViewAs`, `coop.spectatorNoActions` |

Acceptance tests for M1, by name:

`TestCoopJesterPlayAsksWhoGoesNext`, `TestCoopChooseNextSeatSkipsPlayers`,
`TestCoopChooseNextSeatRejectsUnknownSeat`, `TestCoopYieldRingIsExact`,
`TestCoopCannotActNeedsYieldToo`, `TestCoopAllHandsEmptyTerminates` (200 seeds, ≤ N turns),
`TestCoopLostSeatIsRecorded`, `TestCoopSnapshotHidesOtherHands`,
`TestCoopSnapshotLegalOnlyForActiveSeat`, `TestCoopDiamondsEmitPerSeatDrawEvents`.

Gate for this repository: `./scripts/gate.sh`, extended with a **co-op smoke** stage and a
**presence** stage (a synthetic 4-seat cursor stream must not exceed the relay budget).

### The pre-UI test harness

`scripts/coop-play.py` creates a room on a running server, seats 2–4 players with real seat
tokens, deals, joins a spectator, and plays legal actions until the table wins or loses —
every call going through the same HTTP API the browser will use.

```bash
./scripts/coop-play.py --seats 3              # one game, with a summary
./scripts/coop-play.py --seats 4 --repeat 8   # eight rooms, summarised
./scripts/coop-play.py --seats 2 --verbose    # action-by-action transcript
```

Its policy is deliberately plain (hit hard, pay with the smallest card, yield when the hand is
nearly gone), so it mostly loses: it exists to exercise the rules over the wire, not to play
well. The solo bot is a separate plan (`docs/BOT-PLAN.md`). A *winning* co-op script is the
M2 acceptance test (`TestCoopRoomPlaysToWin`) and will be committed as a fixed action list.

---

## 9. Risks

1. **Seat divergence** — full snapshot on every response and on every stream reconnect; `expectedSeq`
   everywhere; no client-side rules.
2. **Hidden information** — `SnapshotFor` is the only projection; a per-seat non-leak test; the grep gate
   `grep -rn "\.Players\[" src/regicide/server | grep -v _test` stays empty; `Event.Note` never carries a
   hand card id; the orb is *explicitly* hidden by the owner rather than inferred by the viewer.
3. **SSE lifecycle** — a leaked stream goroutine per reload is the failure mode to watch; drain test plus
   a goroutine-count assertion in the race suite.
4. **Presence cost on phones** — send-on-move only, 83 ms floor, and no posting while the tab is hidden
   (`document.hidden`).
5. **Store scaling** — one JSON rewrite per action; fine for a handful of LAN rooms, watched by the
   existing `GC`.
6. **Two binaries drifting** — solo is frozen and this repo is a clone, so there is no upstream. Any solo
   fix must be applied twice, by hand, and that is recorded in `docs/PROCESS.md`.

---

## 10. Deliberately not built

Free-text chat (contradicts the communication rule and needs moderation), spectators, matchmaking,
accounts, a co-op leaderboard or tier, hot-seat, turn timers, auto-play, mid-game joins, 5+ players,
cross-restart lobbies, replay export, per-seat undo, voice, WebSocket transport.
