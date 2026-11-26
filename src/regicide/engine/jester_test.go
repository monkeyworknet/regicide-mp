package engine

import "testing"

// ---- R-047..R-055: solo Jester ------------------------------------------

func TestR047_FlipDiscardsHandAndRefills(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "2C", "3C", "4C")
		g.Tavern = cards(t, "5C", "6C", "7C", "8C", "9C", "10C", "2D", "3D", "4D")
	})
	oldHand := cardIDsToStrings(idsOf(g.Players[0].Hand))
	g, evs := apply(t, g, FlipJester{})
	if len(g.Players[0].Hand) != 8 {
		t.Fatalf("hand = %d, want 8", len(g.Players[0].Hand))
	}
	if g.JestersLeft != 1 || g.JestersUsed != 1 {
		t.Fatalf("jesters = %d left, %d used; want 1/1", g.JestersLeft, g.JestersUsed)
	}
	discarded := map[CardID]bool{}
	for _, c := range g.Discard {
		discarded[c.ID] = true
	}
	for _, id := range oldHand {
		if !discarded[CardID(id)] {
			t.Fatalf("old hand card %s not discarded", id)
		}
	}
	if !containsEvent(evs, EvJesterFlip) {
		t.Fatalf("flip event missing: %v", evs)
	}
}

func TestR048_FlipAtStepFourLetsNewHandPay(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20
		g.Players[0].Hand = cards(t, "2C")
		g.Tavern = cards(t, "10C", "10D", "10H", "10S", "5C", "5D", "5H", "5S")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Step4Pre {
		t.Fatalf("phase = %s, want step4_pre (a Jester is available)", g.Phase)
	}
	g, _ = apply(t, g, FlipJester{})
	if len(g.Players[0].Hand) != 8 {
		t.Fatalf("hand = %d after flip, want 8", len(g.Players[0].Hand))
	}
	// The refilled hand must be usable to pay: 20 owed.
	before := g.Step4Paid
	for g.inStep4() && len(g.Players[0].Hand) > 0 {
		g, _ = apply(t, g, PayDamage{CardID: g.Players[0].Hand[0].ID})
	}
	if g.Step4Paid < 20 {
		t.Fatalf("paid = %d (was %d), want at least 20", g.Step4Paid, before)
	}
	if g.Phase == Lost {
		t.Fatalf("lost despite refilling enough cards")
	}
}

func TestR049_FlipInWrongPhaseRejected(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "10C", "10D", "10H")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("10C")})
	g, _ = apply(t, g, PayDamage{CardID: "10D"}) // 10 of 20 owed: step 4 stays open
	if g.Phase != Step4Discard {
		t.Fatalf("phase = %s, want step4_discard after the first payment", g.Phase)
	}
	if err := applyErr(t, g, FlipJester{}); err == nil {
		t.Fatal("flipping after payment started must be illegal")
	}
}

func TestR050_ThirdFlipRejected(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "2C")
		g.Tavern = cards(t, "3C", "4C", "5C", "6C", "7C", "8C", "9C", "10C")
	})
	g, _ = apply(t, g, FlipJester{})
	if g.JestersLeft != 1 {
		t.Fatalf("jesters left = %d, want 1", g.JestersLeft)
	}
	g, _ = apply(t, g, FlipJester{})
	if g.JestersLeft != 0 {
		t.Fatalf("jesters left = %d, want 0", g.JestersLeft)
	}
	if err := applyErr(t, g, FlipJester{}); err == nil {
		t.Fatal("a third flip must be rejected")
	}
}

func TestR051_FlipAgainstDiamondEnemyStillDraws(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JD")
		g.Players[0].Hand = cards(t, "2C")
		g.Tavern = cards(t, "3C", "4C", "5C", "6C", "7C", "8C")
	})
	g, _ = apply(t, g, FlipJester{})
	if len(g.Players[0].Hand) != 6 {
		t.Fatalf("hand = %d, want 6 (refill is not a diamond draw and is not limited by immunity)", len(g.Players[0].Hand))
	}
}

func TestR052_FlipDoesNotCancelImmunity(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JD")
		g.Players[0].Hand = cards(t, "2C")
		g.Tavern = cards(t, "9D", "2C", "3C", "4C", "5C", "6C", "7C", "8C")
	})
	g, _ = apply(t, g, FlipJester{})
	if g.ImmunityCancelled {
		t.Fatal("a solo Jester flip must not cancel enemy immunity")
	}
	// Play the refilled 9D: immune, so no draw.
	before := len(g.Players[0].Hand)
	g, _ = apply(t, g, Play{CardIDs: ids("9D")})
	if len(g.Players[0].Hand) != before-1 {
		t.Fatalf("hand = %d from %d: an immune diamond must not draw", len(g.Players[0].Hand), before)
	}
	if g.Damage != 9 {
		t.Fatalf("damage = %d, want 9", g.Damage)
	}
}

func TestR053_FlipWithEmptyTavernConsumesJester(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Tavern = nil
	})
	g, _ = apply(t, g, FlipJester{})
	if g.JestersLeft != 1 || g.JestersUsed != 1 {
		t.Fatalf("jesters = %d/%d, want 1 left and 1 used", g.JestersLeft, g.JestersUsed)
	}
	if len(g.Players[0].Hand) != 0 {
		t.Fatalf("hand = %d, want 0", len(g.Players[0].Hand))
	}
	// A second flip is still allowed while a Jester remains; the third is not.
	g, _ = apply(t, g, FlipJester{})
	if g.Phase != Lost || g.LostReason != LostCannotAct {
		t.Fatalf("phase = %s reason = %s, want lost/cannot_act once both Jesters are gone", g.Phase, g.LostReason)
	}
}

func TestR054_FlipWindowReopensAfterAKill(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 18
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Castle = cards(t, "QS")
		g.JestersLeft = 2
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want step1 after a kill", g.Phase)
	}
	if !g.canFlipJester() {
		t.Fatal("the Jester window must reopen at the new step 1")
	}
}

func TestR055_Tiers(t *testing.T) {
	for _, tc := range []struct {
		used int
		want Tier
	}{{0, TierGold}, {1, TierSilver}, {2, TierBronze}} {
		if got := TierFor(tc.used); got != tc.want {
			t.Errorf("TierFor(%d) = %s, want %s", tc.used, got, tc.want)
		}
	}
	// A win with one flip is a silver victory.
	g := scenario(t, func(g *Game) {
		g.Enemy = parseCard(t, "KD")
		g.Castle = nil
		g.Damage = 39
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.JestersUsed = 1
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Won || g.Tier() != TierSilver {
		t.Fatalf("phase = %s tier = %s, want won/silver", g.Phase, g.Tier())
	}
}

// ---- R-060: liveness and termination ------------------------------------

func TestR060_RandomPlayAlwaysTerminates(t *testing.T) {
	const games = 500
	const cap = 10000

	randoms := []*SeededRand{}
	for i := 0; i < games; i++ {
		randoms = append(randoms, NewSeededRand(string(rune('A'+i%26))+"-TERMINATION"))
	}
	wins, losses := 0, 0
	for i, r := range randoms {
		g := NewGame(SetupOptions{Seed: "TERM", Rules: DefaultRules(), NumPlayers: 1}, r)
		steps := 0
		for !g.gameOver() {
			if steps >= cap {
				t.Fatalf("game %d did not terminate within %d transitions (phase %s)", i, cap, g.Phase)
			}
			acts := LegalActions(&g)
			if len(acts) == 0 {
				t.Fatalf("dead state in game %d: phase %s, hand %d, loseReason %s", i, g.Phase, len(g.Players[0].Hand), g.LostReason)
			}
			next, _, err := Transition(g, acts[steps%len(acts)], r)
			if err != nil {
				t.Fatalf("game %d step %d: %v", i, steps, err)
			}
			g = next
			steps++
		}
		if g.Phase == Won {
			wins++
		} else {
			losses++
			if g.LostReason == LostNone {
				t.Fatalf("game %d lost without a reason", i)
			}
		}
	}
	t.Logf("random play: %d games, %d wins, %d losses", games, wins, losses)
	if wins+losses != games {
		t.Fatalf("accounting = %d wins + %d losses, want %d", wins, losses, games)
	}
}
