package engine

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// ---- co-op helpers --------------------------------------------------------

// coopGame builds a multi-seat game with empty zones so a test can script every
// pile. Seat 0 acts first.
func coopGame(t *testing.T, seats int, mod func(*Game)) Game {
	t.Helper()
	g := NewGame(SetupOptions{
		Seed:       "TEST-TEST-TEST",
		SeedRandom: false,
		Theme:      "coop-test",
		Rules:      DefaultRules(),
		NumPlayers: seats,
	}, NewScriptedRand())
	g.Phase = Step1Await
	g.LostReason = LostNone
	g.LostSeat = 0
	g.ActiveSeat = 0
	g.SeatChoice = nil
	for i := range g.Players {
		g.Players[i].Hand = nil
		g.Players[i].YieldedLastTurn = false
	}
	g.Tavern = nil
	g.Discard = nil
	g.AttackArea = nil
	g.Castle = nil
	g.Damage = 0
	g.Shield = 0
	g.ImmunityCancelled = false
	g.Step4Required = 0
	g.Step4Paid = 0
	if mod != nil {
		mod(&g)
	}
	return g
}

func hasAction(actions []Action, kind string) bool {
	for _, a := range actions {
		if ActionName(a) == kind {
			return true
		}
	}
	return false
}

// ---- the Jester asks who goes next ----------------------------------------

func TestCoopJesterPlayAsksWhoGoesNext(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) {
		g.Players[0].Hand = cards(t, "J1", "2C")
		setEnemy(g, t, "JS")
	})
	next, evs := apply(t, g, Play{CardIDs: ids("J1")})

	if next.SeatChoice == nil {
		t.Fatal("a co-op Jester must ask who goes next")
	}
	if next.SeatChoice.By != 0 {
		t.Errorf("chooser = %d, want 0", next.SeatChoice.By)
	}
	if len(next.SeatChoice.Next) != 3 {
		t.Errorf("next seats = %v, want every seat", next.SeatChoice.Next)
	}
	if next.ActiveSeat != 0 {
		t.Errorf("active seat moved to %d; the turn must wait for the choice", next.ActiveSeat)
	}
	if !next.ImmunityCancelled {
		t.Error("a Jester cancels enemy immunity")
	}
	if next.Phase != Step1Await {
		t.Errorf("phase = %s, want %s while the choice is pending", next.Phase, Step1Await)
	}
	// The only legal actions are the seat options.
	legal := LegalActions(&next)
	if len(legal) != 3 {
		t.Fatalf("legal actions = %d, want 3 seat options", len(legal))
	}
	for _, a := range legal {
		if _, ok := a.(ChooseNextSeat); !ok {
			t.Fatalf("legal action %s is not a seat choice", ActionName(a))
		}
	}
	noted := false
	for _, ev := range evs {
		if strings.Contains(ev.Note, "jester") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("no event mentions the Jester: %+v", evs)
	}
}

// TestCoopJesterPlayNamesItsSeat: one play event, carrying the seat that played it.
// The second, seat-less event credited the Jester to seat 0 on the rail, so the bot's
// row kept its old attack and the host's row showed the bot's card (playtest 4).
func TestCoopJesterPlayNamesItsSeat(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) {
		g.ActiveSeat = 1
		g.Players[1].Hand = cards(t, "J1", "2C")
		setEnemy(g, t, "JS")
	})
	_, evs := apply(t, g, Play{CardIDs: ids("J1")})
	plays := 0
	for _, ev := range evs {
		if ev.Type != EvPlay {
			continue
		}
		plays++
		if ev.Seat != 1 {
			t.Errorf("Jester play seat = %d, want 1", ev.Seat)
		}
		if !strings.Contains(ev.Note, "jester") {
			t.Errorf("play event note = %q, want the jester note", ev.Note)
		}
	}
	if plays != 1 {
		t.Errorf("Jester play logged %d times, want once", plays)
	}
}

func TestCoopChooseNextSeatSkipsPlayers(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) {
		g.Players[0].Hand = cards(t, "J1")
		g.Players[2].Hand = cards(t, "2C")
		setEnemy(g, t, "JS")
	})
	played, _ := apply(t, g, Play{CardIDs: ids("J1")})
	turn := played.TurnNo

	next, _ := apply(t, played, ChooseNextSeat{Seat: 2})
	if next.SeatChoice != nil {
		t.Error("the choice must clear once made")
	}
	if next.ActiveSeat != 2 {
		t.Errorf("active seat = %d, want 2", next.ActiveSeat)
	}
	if next.TurnNo != turn+1 {
		t.Errorf("turn = %d, want %d", next.TurnNo, turn+1)
	}
	if next.Phase != Step1Await {
		t.Errorf("phase = %s, want a fresh turn", next.Phase)
	}
	if !hasAction(LegalActions(&next), "yield") && len(LegalActions(&next)) == 0 {
		t.Error("the chosen seat must be able to act, empty hands are illegal here")
	}
}

func TestCoopChooseNextSeatRejectsUnknownSeat(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) {
		g.Players[0].Hand = cards(t, "J1")
		setEnemy(g, t, "JS")
	})
	played, _ := apply(t, g, Play{CardIDs: ids("J1")})
	for _, seat := range []int{3, -1, 99} {
		if err := applyErr(t, played, ChooseNextSeat{Seat: seat}); err == nil {
			t.Errorf("seat %d was accepted, want a rejection", seat)
		}
	}
	// And no choice at all is rejected, not assumed.
	plain := coopGame(t, 3, nil)
	if err := applyErr(t, plain, ChooseNextSeat{Seat: 1}); err == nil {
		t.Error("a seat choice without a pending Jester was accepted")
	}
}

// ---- yielding -------------------------------------------------------------

func TestCoopYieldRingIsExact(t *testing.T) {
	// R-044: "A player may not yield if every other player has yielded on their last
	// turn ... if two players have yielded in a row the third player may not yield."
	// So a yield is legal by default and becomes illegal only when EVERY other seat
	// yielded. The predicate used to say the reverse, which made yielding unreachable.
	g := coopGame(t, 3, func(g *Game) {
		for i := range g.Players {
			g.Players[i].Hand = cards(t, "2C", "3C")
		}
		setEnemy(g, t, "JS") // attack 10, so yielding is not pointless
	})

	if !g.yieldLegal() {
		t.Fatal("seat 0 must be able to yield when no other seat yielded (R-044)")
	}
	if !hasAction(LegalActions(&g), "yield") {
		t.Fatal("Yield must be in the legal list when nobody else yielded")
	}

	// One seat played: still legal, and that is the case the old code got wrong.
	g.Players[1].YieldedLastTurn = true
	g.Players[2].YieldedLastTurn = false
	if !g.yieldLegal() {
		t.Fatal("a seat whose last turn was a play leaves yielding legal")
	}

	// Every other seat yielded on its last turn: now seat 0 must play.
	g.Players[1].YieldedLastTurn = true
	g.Players[2].YieldedLastTurn = true
	if g.yieldLegal() {
		t.Fatal("yielding is illegal when every other seat yielded (R-044)")
	}
	if hasAction(LegalActions(&g), "yield") {
		t.Fatal("Yield must be absent once the ring has closed")
	}

	// And the flag clears on that seat's next play.
	g.Players[1].YieldedLastTurn = true
	g.ActiveSeat = 1
	g.Step4Required = 100 // stay out of step 4 by playing into a huge enemy
	g.Players[1].Hand = cards(t, "2C")
	g.Enemy = parseCard(t, "KS") // 40 attack, so the turn goes to step 4
	next, _ := apply(t, g, Play{CardIDs: ids("2C")})
	if next.Players[1].YieldedLastTurn {
		t.Error("playing must clear the seat's yield flag")
	}
}

func TestCoopCannotActNeedsYieldToo(t *testing.T) {
	g := coopGame(t, 2, func(g *Game) {
		g.Players[0].Hand = nil // the active seat holds nothing
		g.Players[1].Hand = cards(t, "2C")
		setEnemy(g, t, "JS")
	})
	g.Players[1].YieldedLastTurn = false
	g.enterTurnStart()
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s: an empty hand with a legal yield must not lose (R-043)", g.Phase)
	}
	if !g.canAct() {
		t.Error("canAct must be true when yielding is legal")
	}

	// No card, no Jester, and every other seat already yielded: the table is stuck,
	// which is the loss.
	g.Players[1].YieldedLastTurn = true
	g.enterTurnStart()
	if g.Phase != Lost {
		t.Fatalf("phase = %s, want a loss when no action exists", g.Phase)
	}
	if g.LostReason != LostCannotAct {
		t.Errorf("lost reason = %s, want %s", g.LostReason, LostCannotAct)
	}

	// Solo is untouched: an empty hand loses even with yielding enabled.
	solo := baseGame(t)
	solo.Rules.AllowSoloYield = true
	solo.JestersLeft = 0
	solo.Players[0].Hand = nil
	solo.Phase = Step1Await
	solo.enemyAttackHigh(t)
	solo.enterTurnStart()
	if solo.Phase != Lost {
		t.Errorf("solo phase = %s; an empty hand is still a solo loss (ADR-006)", solo.Phase)
	}
}

// enemyAttackHigh points the solo game at an enemy that deals damage, so yield
// legality is not the thing under test.
func (g *Game) enemyAttackHigh(t *testing.T) {
	t.Helper()
	g.Enemy = Card{ID: "KS", Rank: King, Suit: Spades}
}

func TestCoopAllHandsEmptyTerminates(t *testing.T) {
	// No hands, no Jesters, an enemy that deals damage: every seat can only yield,
	// and the fourth yield is illegal. The table must end, not spin.
	g := coopGame(t, 3, func(g *Game) {
		setEnemy(g, t, "JS")
	})
	// R-044 lets each seat yield once, so the table does not die on seat 0's turn:
	// A and B yield (C has not, so each is legal), then C may not yield and has no
	// card. The end arrives, and it arrives before the ring can spin.
	g.enterTurnStart()
	if g.Phase == Lost {
		t.Fatalf("an empty table lost on the first seat's turn: R-044 allows A and B to yield first")
	}
	cur := g
	for i := 0; i < 8 && cur.Phase != Lost && cur.Phase != Won; i++ {
		legal := LegalActions(&cur)
		if len(legal) == 0 {
			t.Fatalf("step %d: no legal action while the phase is %s", i, cur.Phase)
		}
		pick := legal[0]
		for _, a := range legal {
			if hasAction([]Action{a}, "yield") {
				pick = a
				break
			}
		}
		next, _, err := Transition(cur, pick, NewSeededRand("COOP-EMPTY-TERM"))
		if err != nil {
			t.Fatalf("step %d: %s rejected: %v", i, ActionName(pick), err)
		}
		cur = next
	}
	if cur.Phase != Lost {
		t.Fatalf("phase = %s: an empty table must end within one rotation", cur.Phase)
	}
	// Either end is correct: the ring can close at turn start (cannot_act) or the
	// last yielder can face a payment it cannot make (cannot_defend).
	if cur.LostReason != LostCannotAct && cur.LostReason != LostCannotDefend {
		t.Errorf("lost reason = %s, want a loss with no legal action (%s or %s)",
			cur.LostReason, LostCannotAct, LostCannotDefend)
	}

	// A random-ish policy over real seeds must always end, and always with a legal
	// action available right up to the end (the liveness invariant).
	for i := 0; i < 200; i++ {
		seed := "COOP-" + strconv.Itoa(i) + "-TERM"
		start := NewGame(SetupOptions{
			Seed: seed, Theme: "simple", Rules: DefaultRules(), NumPlayers: 3,
		}, NewSeededRand(seed))
		g := start
		steps := 0
		for g.Phase != Won && g.Phase != Lost {
			legal := LegalActions(&g)
			if len(legal) == 0 {
				t.Fatalf("seed %s: no legal actions at phase %s turn %d", seed, g.Phase, g.TurnNo)
			}
			if steps > 2000 {
				t.Fatalf("seed %s: 2000 actions without an ending", seed)
			}
			// Prefer completing a pending choice or payment, then yield, then play
			// the first offered card: a legal but impatient player.
			var pick Action
			for _, a := range legal {
				switch a.(type) {
				case ChooseNextSeat, EndPayment:
					pick = a
				}
				if pick != nil {
					break
				}
			}
			if pick == nil {
				for _, a := range legal {
					if _, ok := a.(PayDamage); ok {
						pick = a
						break
					}
				}
			}
			if pick == nil {
				for _, a := range legal {
					if _, ok := a.(Yield); ok {
						pick = a
						break
					}
				}
			}
			if pick == nil {
				pick = legal[0]
			}
			next, _, err := Transition(g, pick, NewSeededRand(seed))
			if err != nil {
				t.Fatalf("seed %s: %s rejected: %v", seed, ActionName(pick), err)
			}
			g = next
			steps++
		}
	}
}

func TestCoopLostSeatIsRecorded(t *testing.T) {
	g := coopGame(t, 2, func(g *Game) {
		g.Players[1].Hand = nil
		setEnemy(g, t, "JS")
	})
	// Seat 1 holds nothing. Seat 0 yielded on its last turn, so the ring is closed
	// and seat 1 has no action left: that is the loss, and it names seat 1.
	g.Players[0].YieldedLastTurn = true
	g.ActiveSeat = 1
	g.enterTurnStart()
	if g.Phase != Lost {
		t.Fatalf("phase = %s, want a loss once the ring closed on an empty hand", g.Phase)
	}
	if g.LostSeat != 1 {
		t.Errorf("LostSeat = %d, want 1", g.LostSeat)
	}
	last := g.Log[len(g.Log)-1]
	if last.Type != EvLose || last.Seat != 1 {
		t.Errorf("lose event = %+v, want seat 1", last)
	}

	// And with seat 0 having played instead, seat 1 may yield, so nobody loses.
	open := coopGame(t, 2, func(g *Game) {
		g.Players[1].Hand = nil
		setEnemy(g, t, "JS")
	})
	open.Players[0].YieldedLastTurn = false
	open.ActiveSeat = 1
	open.enterTurnStart()
	if open.Phase == Lost {
		t.Fatal("an empty hand must not lose while a yield is legal (R-043)")
	}
}

// ---- snapshots ------------------------------------------------------------

func TestCoopSnapshotHidesOtherHands(t *testing.T) {
	for _, seats := range []int{2, 3, 4} {
		g := NewGame(SetupOptions{
			Seed: "COOP-HIDE-" + strconv.Itoa(seats), Theme: "simple", Rules: DefaultRules(), NumPlayers: seats,
		}, NewSeededRand("COOP-HIDE-"+strconv.Itoa(seats)))

		secret := map[CardID]bool{}
		for i, p := range g.Players {
			for _, c := range p.Hand {
				secret[c.ID] = true
			}
			_ = i
		}
		// Tavern and castle order stay server-side too.
		for _, c := range g.Tavern {
			secret[c.ID] = true
		}
		for _, c := range g.Castle {
			secret[c.ID] = true
		}

		for viewer := 0; viewer < seats; viewer++ {
			snap := SnapshotFor(g, View{Seat: viewer})
			raw, err := json.Marshal(snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			body := string(raw)

			own := map[CardID]bool{}
			for _, c := range g.Players[viewer].Hand {
				own[c.ID] = true
			}
			for id := range secret {
				if own[id] {
					continue // the viewer's own hand is theirs to see
				}
				if strings.Contains(body, `"`+string(id)+`"`) {
					t.Fatalf("%d seats, viewer %d: snapshot leaks %s", seats, viewer, id)
				}
			}
			if len(snap.Hand) != len(g.Players[viewer].Hand) {
				t.Errorf("%d seats, viewer %d: hand size %d, want %d", seats, viewer, len(snap.Hand), len(g.Players[viewer].Hand))
			}
			if snap.ViewerSeat != viewer {
				t.Errorf("viewerSeat = %d, want %d", snap.ViewerSeat, viewer)
			}
			if len(snap.Players) != seats {
				t.Errorf("seat rail has %d rows, want %d", len(snap.Players), seats)
			}
			for _, row := range snap.Players {
				if row.Seat == viewer {
					continue
				}
				if len(row.Hand) != 0 {
					t.Errorf("viewer %d sees seat %d's hand", viewer, row.Seat)
				}
				if row.HandCount != len(g.Players[row.Seat].Hand) {
					t.Errorf("seat %d count = %d, want %d", row.Seat, row.HandCount, len(g.Players[row.Seat].Hand))
				}
			}
		}

		// Open hands serialise every hand, which is a deliberate room option.
		open := SnapshotFor(g, View{Seat: 0, OpenHands: true})
		for _, row := range open.Players {
			if len(row.Hand) != len(g.Players[row.Seat].Hand) {
				t.Errorf("open hands: seat %d serialised %d cards, want %d", row.Seat, len(row.Hand), len(g.Players[row.Seat].Hand))
			}
		}
	}
}

func TestCoopSnapshotLegalOnlyForActiveSeat(t *testing.T) {
	g := NewGame(SetupOptions{
		Seed: "COOP-LEGAL", Theme: "simple", Rules: DefaultRules(), NumPlayers: 3,
	}, NewSeededRand("COOP-LEGAL"))

	for viewer := 0; viewer < 3; viewer++ {
		snap := SnapshotFor(g, View{Seat: viewer})
		if viewer == g.ActiveSeat {
			if len(snap.Legal) == 0 {
				t.Fatalf("the active seat must see its legal actions")
			}
		} else if len(snap.Legal) != 0 {
			t.Fatalf("viewer %d saw %d legal actions while seat %d acts", viewer, len(snap.Legal), g.ActiveSeat)
		}
	}

	// The chooser keeps its actions while a Jester choice is pending, everyone else
	// sees none, and the choice is published so the UI can say who is deciding.
	g.SeatChoice = &SeatChoice{By: 2, Next: []int{0, 1, 2}}
	chooser := SnapshotFor(g, View{Seat: 2})
	if len(chooser.Legal) != 3 || chooser.PendingSeatChoice == nil {
		t.Fatalf("chooser: %d actions, pending=%v", len(chooser.Legal), chooser.PendingSeatChoice)
	}
	watcher := SnapshotFor(g, View{Seat: 0})
	if len(watcher.Legal) != 0 {
		t.Errorf("watcher saw %d actions", len(watcher.Legal))
	}
	if watcher.PendingSeatChoice == nil || watcher.PendingSeatChoice.By != 2 {
		t.Errorf("watcher pending = %+v, want the chooser named", watcher.PendingSeatChoice)
	}
}

// ---- draws ----------------------------------------------------------------

func TestCoopDiamondsEmitPerSeatDrawEvents(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) {
		g.Players[0].Hand = cards(t, "5D")
		g.Tavern = cards(t, "2C", "3C", "4C", "6C", "7C", "8C", "9C")
		setEnemy(g, t, "JS")
	})
	next, evs := apply(t, g, Play{CardIDs: ids("5D")})

	drawn := map[int]int{}
	for _, ev := range evs {
		if ev.Type == EvDraw {
			drawn[ev.Seat] += ev.Amount
		}
	}
	if len(drawn) != 3 {
		t.Fatalf("draw events per seat = %v, want all three seats", drawn)
	}
	total := 0
	for _, n := range drawn {
		total += n
	}
	if total != 5 {
		t.Errorf("total drawn = %d, want 5 (the play's value)", total)
	}
	if next.Players[0].Hand == nil {
		t.Error("the active seat draws first, so its hand must not be empty")
	}
}

func TestSoloDrawEventUnchanged(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Players[0].Hand = cards(t, "5D")
		g.Tavern = cards(t, "2C", "3C", "4C", "6C", "7C")
		setEnemy(g, t, "JS")
	})
	_, evs := apply(t, g, Play{CardIDs: ids("5D")})

	draws := 0
	for _, ev := range evs {
		if ev.Type != EvDraw {
			continue
		}
		draws++
		if ev.Amount != 5 || ev.Seat != 0 {
			t.Errorf("solo draw event = %+v, want one event of 5 for seat 0", ev)
		}
	}
	if draws != 1 {
		t.Errorf("solo emitted %d draw events, want exactly 1", draws)
	}
}

// ---- last play tracking ---------------------------------------------------

func TestCoopSeatRailTracksLastPlay(t *testing.T) {
	g := NewGame(SetupOptions{
		Seed: "COOP-RAIL", Theme: "simple", Rules: DefaultRules(), NumPlayers: 3,
	}, NewSeededRand("COOP-RAIL"))

	// Give seat 0 a clubs card it can definitely play, then look at the rail.
	seat := g.ActiveSeat
	g.Players[seat].Hand = cards(t, "2C")
	next, _, err := Transition(g, Play{CardIDs: ids("2C")}, NewSeededRand("COOP-RAIL"))
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	snap := SnapshotFor(next, View{Seat: seat})
	row := snap.Players[seat]
	if len(row.LastPlay) != 1 || row.LastPlay[0] != "2C" {
		t.Errorf("last play = %v, want [2C]", row.LastPlay)
	}
	if !row.Active && next.Phase != Step4Pre && next.Phase != Step4Discard {
		t.Errorf("seat %d should still be marked active", seat)
	}
}

func TestSeatRailPublishesHandSizeAndLimit(t *testing.T) {
	// Hand size is public information; hand contents are not. The rail carries the
	// count against the table's limit, and marks a full hand because the diamond
	// draw skips it.
	g := NewGame(SetupOptions{
		Seed: "COOP-RAIL-SIZE", Theme: "simple", Rules: DefaultRules(), NumPlayers: 3,
	}, NewSeededRand("COOP-RAIL-SIZE"))

	snap := SnapshotFor(g, View{Seat: 0})
	if len(snap.Players) != 3 {
		t.Fatalf("rail has %d rows", len(snap.Players))
	}
	for _, row := range snap.Players {
		if row.HandCount != len(g.Players[row.Seat].Hand) {
			t.Errorf("seat %d count = %d, want %d", row.Seat, row.HandCount, len(g.Players[row.Seat].Hand))
		}
		if row.HandLimit != g.MaxHand {
			t.Errorf("seat %d limit = %d, want %d", row.Seat, row.HandLimit, g.MaxHand)
		}
		if row.HandFull != (row.HandCount >= row.HandLimit) {
			t.Errorf("seat %d full = %v at %d/%d", row.Seat, row.HandFull, row.HandCount, row.HandLimit)
		}
	}

	// Fill one seat to the limit and check the flag flips.
	g.Players[1].Hand = nil
	for len(g.Players[1].Hand) < g.MaxHand {
		g.Players[1].Hand = append(g.Players[1].Hand, g.Players[0].Hand[0])
	}
	snap = SnapshotFor(g, View{Seat: 0})
	if !snap.Players[1].HandFull {
		t.Errorf("a full seat was not marked full (%d/%d)", snap.Players[1].HandCount, snap.Players[1].HandLimit)
	}
}

// F-6: a pending Jester choice is a gate on the whole action set, enforced in the
// dispatch. Before this, a Play could slip past the choice because only the Jester's
// own handler looked at SeatChoice.
func TestF6PlayRejectedWhileSeatChoicePending(t *testing.T) {
	g := coopGame(t, 3, nil)
	g.SeatChoice = &SeatChoice{By: 0, Next: []int{0, 1, 2}}
	g.Players[0].Hand = []Card{parseCard(t, "5H")}

	// Every action except the choice itself is refused, and nothing changes.
	for _, a := range []Action{
		Play{CardIDs: []CardID{"5H"}},
		Yield{},
		FlipJester{},
		EndPayment{},
		PayDamage{CardIDs: []CardID{"5H"}},
	} {
		next, events, err := Transition(g, a, NewScriptedRand())
		if err == nil {
			t.Fatalf("%T was accepted while a seat choice was pending", a)
		}
		if len(events) != 0 {
			t.Fatalf("%T emitted %d events on a refused action", a, len(events))
		}
		if next.TurnNo != g.TurnNo || len(next.Log) != len(g.Log) {
			t.Fatalf("%T advanced the state", a)
		}
	}

	// The choice itself is the only legal action, and the projection offers only it.
	legal := LegalActions(&g)
	if len(legal) != 3 {
		t.Fatalf("legal actions = %v, want one per candidate seat", legal)
	}
	for _, a := range legal {
		if _, ok := a.(ChooseNextSeat); !ok {
			t.Fatalf("a %T was offered while a choice was pending", a)
		}
	}
	if _, _, err := Transition(g, legal[0], NewScriptedRand()); err != nil {
		t.Fatalf("the pending choice was refused: %v", err)
	}
}

// TestCoopKeepsMoreHistoryThanSolo: a three-seat turn emits an event per seat, so the
// solo ceiling of 512 was reached mid-game and the replay lost its opening. The cap is
// 1024 at a table and unchanged in solo.
func TestCoopKeepsMoreHistoryThanSolo(t *testing.T) {
	g := coopGame(t, 3, func(g *Game) { setEnemy(g, t, "JS") })
	if got := g.logCap(); got != maxLogCoop {
		t.Fatalf("co-op cap = %d, want %d", got, maxLogCoop)
	}
	solo := baseGame(t)
	if got := solo.logCap(); got != maxLog {
		t.Fatalf("solo cap = %d, want %d", got, maxLog)
	}
	// A co-op log of 900 events survives; 1100 is trimmed to the cap.
	long := make([]Event, 900)
	if got := trimLogTo(long, g.logCap()); len(got) != 900 {
		t.Fatalf("900 events were trimmed to %d", len(got))
	}
	longer := make([]Event, 1100)
	if got := trimLogTo(longer, g.logCap()); len(got) != maxLogCoop {
		t.Fatalf("1100 events trimmed to %d, want %d", len(got), maxLogCoop)
	}
}

// TestNoteEventKeepsTheLogCap pins the server-side note path: host changes are the only
// events the room appends itself, and before 0.9.8 they bypassed the cap.
func TestNoteEventKeepsTheLogCap(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "NOTE-CAP", Rules: DefaultRules(), NumPlayers: 2}, NewScriptedRand())
	for i := 0; i < maxLogCoop+50; i++ {
		g.NoteEvent(Event{Type: EvHostChange, Seat: 0, Amount: 1, Note: "host moved"})
	}
	if len(g.Log) != maxLogCoop {
		t.Fatalf("log = %d entries, want the %d cap", len(g.Log), maxLogCoop)
	}
	// The empty turn is stamped, like every other event the log records.
	if got := g.Log[len(g.Log)-1]; got.Turn != g.TurnNo {
		t.Fatalf("note turn = %d, want %d", got.Turn, g.TurnNo)
	}
}
