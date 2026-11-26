# Audio plan — events, gaps, and where new sounds could come from

Status: research only. Nothing here is committed as an asset yet; the choices below need the
operator's call (synth vs CC0 packs) before implementation.

## What the game has now

`web/data/audio.json` maps 17 events to files; the client plays one at random when an event has
several takes (`audio.js`). The library is 12 samples from the **CrispCards** sampler plus 7
synthesised effects. Volume and a global mute live in Settings.

Current mapping (`SOUND_FOR_EVENT` in `app.js`):

| Event | Cue | Notes |
|---|---|---|
| deal / setup | shuffle, card deal | 2 takes |
| play | card flip, card place | 2 takes |
| combo (2+ cards) | synth combo | layered over `play` |
| draw / hearts | card draw, heal | |
| damage | hit light | |
| shield | synth shield | |
| defeat | synth defeat | same cue for a lost run |
| recruit | card reveal | royal joins the deck |
| suffer (payment card) | card flip | one per card |
| enemy attack | synth enemy attack | |
| jester flip | synth jester | solo flip only |
| turn end | turn start | |
| yield | UI click | shared with generic UI |
| win / tier | victory, tier | |
| enemy reveal | card reveal | shares `recruit`/`reveal` |
| lose | synth defeat | shares `defeat` |

## Events that should have a sound (and what is missing)

Grouped by how often a player hears them. **Bold** = no distinct cue today.

**Every turn**
- Select / deselect a card (a soft tap; today silent).
- **Refused action** (the Play button is disabled or the server says no) — needs a short “no” tick; today a toast only.
- Play one card / play a combo (exists; combo layer exists).
- Suit power resolution: spades shield, hearts shuffle, diamonds draw, clubs ×2 (exists, but clubs has no distinct sting).
- Damage dealt; **exact kill** (today identical to a normal defeat; a brighter stinger would reward the rule).
- Royal defeated; **next royal enters** (today silent).
- Payment: card forfeited (exists per card); **payment ended short** (a low thud would explain the loss).
- Enemy attack / step 4 begins (exists).

**Jesters**
- Jester played to cancel immunity (today shares the generic play cue).
- **Immunity broken** (needs a distinct cue; it changes the whole turn).
- Solo Jester flip (exists).

**Table events (co-op)**
- **Your turn starts** (a short “you” cue; the banner alone is easy to miss).
- **Another seat starts** (optional, quieter).
- **Seat joins / leaves**; **bot takes a chair / human reclaims it**.
- **Host deals / play again**.
- **Waiting for an away seat** (optional ambient tick, not a loop).
- **Connection lost / restored** (the chip already changes; a cue helps off-screen).

**Result**
- Win, tier, lose (exist; the loss and a royal defeat share one file, which reads wrong).
- **New personal best / leaderboard submit** (exists only as `leaderboard` mapped to a slot sound, unused by the client).

**UI**
- Sheet/menu open and close (cards, history, rules, credits, settings) — today mostly silent.
- Tab/badge clicks, copy, invite (UI click exists).
- **First-run onboarding** (optional).

## Design rules I would keep

1. **Short**: < 400 ms for taps, < 1.2 s for stingers; a co-op table can fire several per second.
2. **Variants**: 2–3 takes per frequent event (play, pay, select) with random pick and ±5 % pitch to avoid machine-gun repetition.
3. **Baseline**: normalise to about −12 LUFS, true peak ≤ −1 dB; the CrispCards samples are already close.
4. **Layers, not louder**: combo = play + a quiet shimmer; exact kill = defeat + a bright tail.
5. **Classes**: Settings should group `cards`, `powers`, `table`, `result`, `ui`, so a player can mute result stingers but keep card sounds.
6. **Never block**: audio initialises on the first gesture; a missing file degrades to silence (already true).
7. **Co-op fairness**: all seats hear the same event cues; “your turn” is the only per-seat cue.

## Where new sounds could come from

| Option | Licence | Fit | Cost |
|---|---|---|---|
| **Extend the synth set** (sox / Python, as the 7 existing cues) | Own work, no licence | Full control; can make exact-kill, immunity-break and turn cues distinct | Design time; a synth card tap can sound cheap |
| **Kenney Casino Audio + Digital Audio** | **CC0** (public domain) | Card handling, chips, UI; proven game pack | Free; some sounds are casino-flavoured, need curation |
| **OpenGameArt “54 Casino sound effects”** | **CC0** | Cards, dice, chips; a broad source for variants | Free; inconsistent recording levels, needs normalising |
| **Freesound (CC0 filter only)** | CC0 per sound | Fill specific gaps (paper slide, low thud) | Free; must keep the CC0 filter and record each URL |
| **Zapsplat playing-cards pack** | Free with attribution and restrictions | Good card foley | Licence adds conditions; avoid for a private fan build |
| **itch.io card SFX packs** | Varies per pack | Cheap bundles | Must read each licence; mixed quality |

Recommendation: **Kenney (CC0) for foley + a small synth set for the game-specific stingers**
(exact kill, immunity broken, royal enters, your turn). That keeps attribution simple and the
CREDITS gate honest.

## Proposed implementation (after the operator picks a direction)

1. Curate the files into `web/sfx/` with lowercase kebab names and a `source`/licence entry.
2. Extend `audio.json` with the new event keys and 2–3 takes for frequent cues.
3. Extend `SOUND_FOR_EVENT` for `enemy_reveal` (distinct royal-arrival cue), `immunity_broken`,
   `exact_kill`, `payment_short`, `turn_mine`, `seat_join`, `seat_leave`, `refuse`, `sheet_open`,
   `sheet_close`; keep `defeat` for a lost run only.
4. Add event classes + per-class volume in Settings; keep the master mute.
5. `gen_credits.py`/`check_credits.sh` cover every new file (the gate already enforces this).
6. Browser check: `?test=smoke` asserts every `audio.json` key resolves to a file and that mute
   stops playback; a manual listening pass at 390 and 1440.

Open questions for the operator: synth-only, CC0-only, or the mix above? Is a casino-flavoured
foley acceptable, or should everything sound like a paper-and-wood table?
