package engine

import (
	"strings"
	"testing"
)

// ---- R-006..R-013: Animal Companions and combos ---------------------------

func TestR006_CompanionAlone(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "AD")
		g.Tavern = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("AD")})
	if g.Damage != 1 {
		t.Errorf("damage = %d, want 1", g.Damage)
	}
	if len(g.Players[0].Hand) != 1 {
		t.Errorf("hand = %d, want 1 (diamond drew one)", len(g.Players[0].Hand))
	}
}

func TestR007_CompanionPlusDiamond(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.MaxHand = 12 // lift the limit so the power count is observable
		g.Players[0].Hand = cards(t, "AD", "8D")
		g.Tavern = cards(t, "2C", "3C", "4C", "5C", "6C", "7C", "8C", "9C", "10C", "2H")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("AD", "8D")})
	if g.Damage != 9 {
		t.Errorf("damage = %d, want 9 (no club in the play)", g.Damage)
	}
	if len(g.Players[0].Hand) != 9 {
		t.Errorf("hand = %d, want 9 drawn", len(g.Players[0].Hand))
	}
}

func TestR008_CompanionPlusCompanion(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "AS", "AD")
		g.Tavern = cards(t, "2C", "3C", "4C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("AS", "AD")})
	if g.Damage != 2 {
		t.Errorf("damage = %d, want 2", g.Damage)
	}
	if g.Shield != 2 {
		t.Errorf("shield = %d, want 2 (spades at the play total)", g.Shield)
	}
	if len(g.Players[0].Hand) != 2 {
		t.Errorf("hand = %d, want 2 drawn", len(g.Players[0].Hand))
	}
}

func TestR009_SameSuitCompanionPairAppliesOnce(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.MaxHand = 12
		g.Players[0].Hand = cards(t, "AD", "5D")
		g.Tavern = cards(t, "2C", "3C", "4C", "5C", "6C", "7C", "8C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("AD", "5D")})
	if len(g.Players[0].Hand) != 6 {
		t.Errorf("hand = %d, want 6 (power applied once at 6)", len(g.Players[0].Hand))
	}
}

func TestR010_CompanionWithJesterIllegal(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "AS", "J1")
	})
	if err := applyErr(t, g, Play{CardIDs: ids("AS", "J1")}); err == nil {
		t.Fatal("companion + Jester should be illegal")
	}
}

func TestR011_CompanionInComboIllegal(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "AS", "2C", "2D")
	})
	if err := applyErr(t, g, Play{CardIDs: ids("AS", "2C", "2D")}); err == nil {
		t.Fatal("companion inside a number combo should be illegal")
	}
}

func TestR012_ComboLegalityTable(t *testing.T) {
	cases := []struct {
		name  string
		hand  []string
		play  []string
		legal bool
	}{
		{"pair of 2s", []string{"2C", "2D"}, []string{"2C", "2D"}, true},
		{"triple 2s", []string{"2C", "2D", "2H"}, []string{"2C", "2D", "2H"}, true},
		{"quad 2s", []string{"2C", "2D", "2H", "2S"}, []string{"2C", "2D", "2H", "2S"}, true},
		{"pair of 3s", []string{"3C", "3D"}, []string{"3C", "3D"}, true},
		{"triple 3s", []string{"3C", "3D", "3H"}, []string{"3C", "3D", "3H"}, true},
		{"pair of 4s", []string{"4C", "4D"}, []string{"4C", "4D"}, true},
		{"pair of 5s sums to 10", []string{"5C", "5D"}, []string{"5C", "5D"}, true},
		{"pair of 6s exceeds 10", []string{"6C", "6D"}, []string{"6C", "6D"}, false},
		{"mixed ranks", []string{"2C", "3D"}, []string{"2C", "3D"}, false},
		{"five 2s impossible in deck but rejected", []string{"2C", "2D", "2H", "2S", "AS"}, []string{"2C", "2D", "2H", "2S", "AS"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := scenario(t, func(g *Game) {
				setEnemy(g, t, "JS")
				g.Players[0].Hand = cards(t, tc.hand...)
			})
			_, err := ValidatePlay(&g, ids(tc.play...))
			if tc.legal && err != nil {
				t.Fatalf("expected legal, got %v", err)
			}
			if !tc.legal && err == nil {
				t.Fatal("expected illegal, got nil error")
			}
		})
	}
}

func TestR013_ComboPowersAtTotal(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KH") // hearts enemy: no diamonds/clubs in the play are immune
		g.MaxHand = 14
		g.Players[0].Hand = cards(t, "3D", "3S", "3C")
		g.Tavern = cards(t, "2D", "4D", "5D", "6D", "7D", "8D", "9D", "10D", "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("3D", "3S", "3C")})
	if g.Damage != 18 {
		t.Errorf("damage = %d, want 18 (9 doubled by clubs)", g.Damage)
	}
	if g.Shield != 9 {
		t.Errorf("shield = %d, want 9", g.Shield)
	}
	// 9 diamonds worth of draws, minus the 3 cards played: hand limit lifted to 14.
	if len(g.Players[0].Hand) < 9 {
		t.Errorf("hand = %d, want at least 9 drawn", len(g.Players[0].Hand))
	}
}

// ---- R-014..R-022: suit powers -------------------------------------------

func TestR014_HeartsBeforeDiamonds(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.MaxHand = 12
		g.Players[0].Hand = cards(t, "2D", "2H")
		g.Tavern = cards(t, "3C", "4C")              // only 2 cards on top
		g.Discard = cards(t, "5C", "6C", "7C", "8C") // hearts moves 4 under the tavern
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2D", "2H")})
	// Hearts moved 4 cards under the deck, so the tavern is 2 + 4 = 6 cards, and
	// the 4 draws came from the original top (3C, 4C, then the moved cards).
	if len(g.Tavern) != 2 {
		t.Errorf("tavern = %d, want 2 after moving 4 and drawing 4", len(g.Tavern))
	}
	if g.Discard == nil || len(g.Discard) != 0 {
		t.Errorf("discard = %d, want 0 (all four healed away)", len(g.Discard))
	}
	if len(g.Players[0].Hand) != 4 {
		t.Errorf("hand = %d, want 4", len(g.Players[0].Hand))
	}
}

func TestR015_HeartsMovesAtMostDiscardSize(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "5H")
		g.Discard = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5H")})
	if len(g.Discard) != 0 {
		t.Errorf("discard = %d, want 0", len(g.Discard))
	}
	if len(g.Tavern) != 2 {
		t.Errorf("tavern = %d, want 2 (both discards moved under)", len(g.Tavern))
	}
}

func TestR016_HeartsEmptyDiscardConsumesNoRNG(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "5H")
		g.Discard = nil
	})
	before := g.RGNDraws
	g, _ = apply(t, g, Play{CardIDs: ids("5H")})
	if g.RGNDraws != before {
		t.Errorf("RNG draws = %d, want %d (no shuffle on an empty discard)", g.RGNDraws, before)
	}
}

func TestR017_HeartsCardsGoUnderTavern(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "2H")
		g.Discard = cards(t, "9C", "10C")
		g.Tavern = cards(t, "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2H")})
	// The single tavern card stays on top; the two healed cards are appended under
	// it, so they are drawn later.
	if len(g.Tavern) != 3 {
		t.Fatalf("tavern = %d, want 3", len(g.Tavern))
	}
	if g.Tavern[0].ID != "3C" {
		t.Fatalf("tavern top = %s, want 3C (healed cards go underneath)", g.Tavern[0].ID)
	}
	moved := map[CardID]bool{g.Tavern[1].ID: true, g.Tavern[2].ID: true}
	if !moved["9C"] || !moved["10C"] {
		t.Fatalf("healed cards not under the deck: %v", cardIDsToStrings(idsOf(g.Tavern)))
	}
}

func TestR018_HeartsCannotTakeAttackArea(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Damage = 10
		g.AttackArea = cards(t, "7C", "8C")
		g.Players[0].Hand = cards(t, "9H")
		g.Discard = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("9H")})
	if len(g.AttackArea) != 3 {
		t.Fatalf("attack area = %d, want 3 (staged cards are not discardable by hearts)", len(g.AttackArea))
	}
	for _, c := range g.AttackArea[:2] {
		if c.ID != "7C" && c.ID != "8C" {
			t.Fatalf("attack area lost staged cards: %v", cardIDsToStrings(idsOf(g.AttackArea)))
		}
	}
}

func TestR019_DiamondsDrawAttackValue(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "5D")
		g.Tavern = cards(t, "2C", "3C", "4C", "6C", "7C", "8C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5D")})
	if len(g.Players[0].Hand) != 5 {
		t.Errorf("hand = %d, want 5", len(g.Players[0].Hand))
	}
}

func TestR020_DiamondsWithFullHandDrawNothing(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "4D", "2C", "3C", "4C", "5C", "6C", "7C", "8C")
		g.Tavern = cards(t, "9C", "10C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("4D")})
	if len(g.Players[0].Hand) != 8 {
		t.Errorf("hand = %d, want 8 (limit)", len(g.Players[0].Hand))
	}
}

func TestR021_DiamondsWithShortTavernNoPenalty(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "7D")
		g.Tavern = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("7D")})
	if g.Phase == Lost {
		t.Fatalf("short tavern should not lose the game (phase %s)", g.Phase)
	}
	if len(g.Players[0].Hand) != 2 {
		t.Errorf("hand = %d, want 2", len(g.Players[0].Hand))
	}
	if len(g.Tavern) != 0 {
		t.Errorf("tavern = %d, want 0", len(g.Tavern))
	}
}

func TestR022_ClubsDoubleWholePlay(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KD")
		g.Players[0].Hand = cards(t, "8C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("8C")})
	if g.Damage != 16 {
		t.Errorf("8C damage = %d, want 16", g.Damage)
	}

	g = scenario(t, func(g *Game) {
		setEnemy(g, t, "KD")
		g.Players[0].Hand = cards(t, "5C", "AD")
		g.Tavern = cards(t, "2C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5C", "AD")})
	if g.Damage != 12 {
		t.Errorf("5C+AD damage = %d, want 12", g.Damage)
	}
}

// ---- R-023..R-029: spades and immunity -----------------------------------

func TestR023_SpadesCumulativeAcrossTurns(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20
		g.Players[0].Hand = cards(t, "3S", "4S", "10C", "7C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("3S")})
	if g.Shield != 3 {
		t.Fatalf("shield = %d, want 3", g.Shield)
	}
	if !g.inStep4() {
		t.Fatalf("phase = %s, want a step-4 phase", g.Phase)
	}
	g, _ = apply(t, g, PayDamage{CardID: "10C"}) // 10 of the 17 owed
	g, _ = apply(t, g, PayDamage{CardID: "7C"})  // 17 >= 17 ends the turn
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want a new turn", g.Phase)
	}
	g, _ = apply(t, g, Play{CardIDs: ids("4S")})
	if g.Shield != 7 {
		t.Fatalf("shield = %d, want 7", g.Shield)
	}
}

func TestR024_SpadesReduceThisTurnsAttack(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC") // attack 10
		g.Players[0].Hand = cards(t, "4S")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("4S")})
	if g.Step4Required != 6 {
		t.Errorf("required = %d, want 6 (10 - 4)", g.Step4Required)
	}
}

func TestR025_ShieldResetsOnNewEnemy(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 19
		g.Shield = 9
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Castle = cards(t, "QS", "KD")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want a new turn", g.Phase)
	}
	if g.Shield != 0 {
		t.Errorf("shield = %d, want 0 against the new enemy", g.Shield)
	}
	if g.Enemy.Rank != Queen {
		t.Errorf("enemy = %s, want a Queen", g.Enemy.ID)
	}
}

func TestR026_ShieldFloorsAtZero(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Shield = 30
		g.Players[0].Hand = cards(t, "2C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase == Lost {
		t.Fatalf("over-shielded attack should require no discards")
	}
	if g.Step4Required != 0 {
		t.Errorf("required = %d, want 0", g.Step4Required)
	}
}

func TestR027_ImmunitySuppressesPowerNotDamage(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JD") // diamonds immune
		g.Players[0].Hand = cards(t, "9D")
		g.Tavern = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("9D")})
	if g.Damage != 9 {
		t.Errorf("damage = %d, want 9", g.Damage)
	}
	if len(g.Players[0].Hand) != 0 {
		t.Errorf("hand = %d, want 0 (immune diamond draws nothing)", len(g.Players[0].Hand))
	}
}

func TestR028_ClubsImmuneNoDoubling(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "8C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("8C")})
	if g.Damage != 8 {
		t.Errorf("damage = %d, want 8 (clubs immune: no doubling)", g.Damage)
	}
}

func TestR029_SpadesImmuneNoShield(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "8S")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("8S")})
	if g.Shield != 8 {
		t.Errorf("accumulated shield = %d, want 8 (accumulates unconditionally)", g.Shield)
	}
	if g.Step4Required != 10 {
		t.Errorf("required = %d, want 10 (immune spades do not reduce yet)", g.Step4Required)
	}
}

// ---- R-030..R-038: defeating enemies and recruiting ----------------------

func TestR030_DamageAtOrAboveHealthDefeats(t *testing.T) {
	for _, tc := range []struct{ damage int }{{20}, {21}} {
		g := scenario(t, func(g *Game) {
			setEnemy(g, t, "JC")
			g.Damage = tc.damage - 2 // play 2C for the last 2 points
			g.Players[0].Hand = cards(t, "2C")
			g.Castle = cards(t, "QS")
			g.Tavern = cards(t, "3C")
		})
		g, _ = apply(t, g, Play{CardIDs: ids("2C")})
		if g.Enemy.Rank != Queen {
			t.Fatalf("cumulative damage %d did not defeat the 20-health Jack", tc.damage)
		}
	}
}

func TestR031_ExactKillTopsTavern(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 18
		g.Players[0].Hand = cards(t, "2C", "5D")
		g.Castle = cards(t, "QS")
		g.Tavern = cards(t, "3C", "4C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")}) // 18 + 2 = 20 exactly
	if len(g.Tavern) == 0 || g.Tavern[0].ID != "JC" {
		t.Fatalf("tavern top = %v, want the exact-killed JC", cardIDsToStrings(idsOf(g.Tavern)))
	}
}

func TestR032_OverkillGoesToDiscard(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 19
		g.Players[0].Hand = cards(t, "5C") // 10 damage with doubling
		g.Castle = cards(t, "QS")
		g.Tavern = cards(t, "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5C")})
	if len(g.Tavern) > 0 && g.Tavern[0].ID == "JC" {
		t.Fatal("over-killed enemy must not go on top of the tavern deck")
	}
	found := false
	for _, c := range g.Discard {
		if c.ID == "JC" {
			found = true
		}
	}
	if !found {
		t.Fatal("over-killed enemy should be in the discard pile")
	}
}

func TestR033_AttackAreaFlushedOnDefeat(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 18
		g.AttackArea = cards(t, "7S")
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Castle = cards(t, "QS")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if len(g.AttackArea) != 0 {
		t.Fatalf("attack area = %d, want 0", len(g.AttackArea))
	}
	seen := map[CardID]bool{}
	for _, c := range g.Discard {
		seen[c.ID] = true
	}
	if !seen["7S"] || !seen["2C"] {
		t.Fatalf("staged cards missing from discard: %v", cardIDsToStrings(idsOf(g.Discard)))
	}
}

func TestR034_KillerStartsNewTurnSkippingStep4(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 18
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Castle = cards(t, "QS")
	})
	before := g.TurnNo
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want step1 (step 4 skipped)", g.Phase)
	}
	if g.TurnNo != before+1 {
		t.Errorf("turn = %d, want %d", g.TurnNo, before+1)
	}
	if g.Step4Required != 0 || g.Step4Paid != 0 {
		t.Errorf("step4 state leaked: required=%d paid=%d", g.Step4Required, g.Step4Paid)
	}
}

func TestR035_OverkillNotCarried(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 19
		g.Players[0].Hand = cards(t, "10C") // 20 damage, 19 + 20 = 39 vs 20 hp
		g.Castle = cards(t, "QS")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("10C")})
	if g.Damage != 0 {
		t.Errorf("damage on the new enemy = %d, want 0", g.Damage)
	}
}

func TestR036_RecruitedRoyalValues(t *testing.T) {
	for _, tc := range []struct {
		id    string
		value int
	}{{"JS", 10}, {"QD", 15}, {"KH", 20}} {
		c := parseCard(t, tc.id)
		if c.PlayValue() != tc.value || c.DiscardValue() != tc.value {
			t.Errorf("%s = %d/%d, want %d", tc.id, c.PlayValue(), c.DiscardValue(), tc.value)
		}
	}
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "10C")
	})
	_ = g
	// Discarding a recruited Queen covers 15 of an attack.
	g = scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20
		g.Players[0].Hand = cards(t, "QD", "5C")
		g.Step4Required = 20
		g.Step4Paid = 0
		g.Phase = Step4Discard
	})
	g, _ = apply(t, g, PayDamage{CardID: "QD"})
	if g.Step4Paid != 15 {
		t.Errorf("paid = %d, want 15", g.Step4Paid)
	}
}

func TestR037_RecruitedRoyalSuitPower(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.MaxHand = 25
		g.Players[0].Hand = cards(t, "KH")
		g.Discard = cards(t, "2C", "3C", "4C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("KH")})
	if len(g.Tavern) != 3 {
		t.Errorf("tavern = %d, want 3 cards healed under the deck", len(g.Tavern))
	}

	g = scenario(t, func(g *Game) {
		setEnemy(g, t, "KD")
		g.Players[0].Hand = cards(t, "JS")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("JS")})
	if g.Shield != 10 {
		t.Errorf("shield = %d, want 10", g.Shield)
	}
}

func TestR038_RecruitedRoyalVsSameSuitEnemy(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KH") // hearts immune
		g.Players[0].Hand = cards(t, "QH")
		g.Discard = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("QH")})
	if len(g.Discard) != 2 {
		t.Errorf("discard = %d, want 2 (hearts power suppressed)", len(g.Discard))
	}
	if g.Damage != 15 {
		t.Errorf("damage = %d, want 15 (damage still applies)", g.Damage)
	}
}

// ---- R-039..R-046: step 4, yield, win -----------------------------------

func TestR039_StepFourStopsAtThreshold(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC") // attack 10, clubs immune
		g.Players[0].Hand = cards(t, "4C", "6C", "5C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("4C")}) // 4 damage, no doubling (clubs immune)
	if !g.inStep4() {
		t.Fatalf("phase = %s, want a step-4 phase", g.Phase)
	}
	g, _ = apply(t, g, PayDamage{CardID: "6C"})
	if g.Step4Paid != 6 || !g.inStep4() {
		t.Fatalf("paid %d phase %s, want 6 and still paying", g.Step4Paid, g.Phase)
	}
	// 6 + 5 >= 10: the step ends immediately, no extra card may be discarded.
	g, _ = apply(t, g, PayDamage{CardID: "5C"})
	if g.Step4Paid != 11 {
		t.Fatalf("paid = %d, want 11", g.Step4Paid)
	}
	if g.inStep4() {
		t.Fatal("step 4 should have ended at the threshold")
	}
	if err := applyErr(t, g, PayDamage{CardID: "4C"}); err == nil {
		t.Fatal("discarding past the threshold must be illegal")
	}
}

func TestR040_UnaffordableAttackLoses(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.JestersLeft = 0
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Lost || g.LostReason != LostCannotDefend {
		t.Fatalf("phase = %s reason = %s, want lost/cannot_defend", g.Phase, g.LostReason)
	}
}

func TestR041_ZeroRequiredAttackNeedsNoDiscards(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Shield = 25
		g.Players[0].Hand = cards(t, "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Step4Required != 0 {
		t.Fatalf("required = %d, want 0", g.Step4Required)
	}
	if g.inStep4() {
		t.Fatalf("phase = %s, want the turn to have ended", g.Phase)
	}
}

func TestR042_EmptyHandZeroRequiredSurvivesStep4(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Shield = 25
		g.Players[0].Hand = cards(t, "2C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.LostReason == LostCannotDefend {
		t.Fatal("an empty hand should survive a zero-damage step 4")
	}
	// The following turn start is a different rule: no cards and no Jesters loses.
	if g.Phase == Lost && g.LostReason != LostCannotAct {
		t.Fatalf("lost reason = %s, want cannot_act", g.LostReason)
	}
}

func TestR043_EmptyHandAtStepOneLoses(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = nil
		g.JestersLeft = 0
	})
	g.enterTurnStart()
	if g.Phase != Lost || g.LostReason != LostCannotAct {
		t.Fatalf("phase = %s reason = %s, want lost/cannot_act", g.Phase, g.LostReason)
	}
}

func TestR044_YieldRejectedByDefault(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Players[0].Hand = cards(t, "10C")
	})
	if err := applyErr(t, g, Yield{}); err == nil {
		t.Fatal("yielding should be disabled by default")
	}
}

func TestR045_YieldSkipsStepsTwoAndThree(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Rules.AllowSoloYield = true
		setEnemy(g, t, "JC") // attack 10
		g.Players[0].Hand = cards(t, "10C", "2C")
	})
	g, _ = apply(t, g, Yield{})
	if g.Damage != 0 {
		t.Errorf("damage = %d, want 0 (no play happened)", g.Damage)
	}
	if g.Step4Required != 10 {
		t.Errorf("required = %d, want 10", g.Step4Required)
	}
	if !g.inStep4() {
		t.Errorf("phase = %s, want a step-4 phase", g.Phase)
	}
}

func TestR046_WinOnFinalEnemy(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Enemy = parseCard(t, "KD")
		g.Castle = nil
		g.Damage = 39
		g.Players[0].Hand = cards(t, "2C")
	})
	g, evs := apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Won {
		t.Fatalf("phase = %s, want won", g.Phase)
	}
	if g.Enemy.ID != "" {
		t.Errorf("enemy = %s, want none after the final kill", g.Enemy.ID)
	}
	if len(g.Castle) != 0 {
		t.Errorf("castle = %d, want 0", len(g.Castle))
	}
	if g.Tier() != TierGold {
		t.Errorf("tier = %s, want gold", g.Tier())
	}
	if !containsEvent(evs, EvWin) {
		t.Errorf("win event missing: %v", evs)
	}
}

func containsEvent(evs []Event, typ string) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func (g *Game) Tier() Tier { return TierFor(g.JestersUsed) }

var _ = strings.Join

// inStep4 reports whether the state is waiting for step-4 input.
func (g *Game) inStep4() bool { return g.Phase == Step4Pre || g.Phase == Step4Discard }

// TestChaosCastle holds the twelve royals exactly once and shuffles their order.
func TestChaosCastle(t *testing.T) {
	rules := DefaultRules()
	rules.Chaos = true
	g := NewGame(SetupOptions{Seed: "CHAOS-1", NumPlayers: 1, Rules: rules}, NewSeededRand("CHAOS-1"))
	// The first enemy is already revealed at setup, so it is out of the castle pile.
	pile := append([]Card{g.Enemy}, g.Castle...)
	if len(pile) != 12 {
		t.Fatalf("castle = %d cards, want 12", len(pile))
	}
	seen := map[string]bool{}
	jacks, queens, kings := 0, 0, 0
	for _, c := range pile {
		if seen[string(c.ID)] {
			t.Fatalf("duplicate castle card %s", c.ID)
		}
		seen[string(c.ID)] = true
		switch c.Rank {
		case Jack:
			jacks++
		case Queen:
			queens++
		case King:
			kings++
		default:
			t.Fatalf("castle contains a non-royal: %s", c.ID)
		}
	}
	if jacks != 4 || queens != 4 || kings != 4 {
		t.Fatalf("ranks = %d/%d/%d, want 4 each", jacks, queens, kings)
	}

	// Chaos must not fall back to the ramp (Jacks, then Queens, then Kings).
	ramp := true
	for i, c := range pile {
		want := Jack
		if i >= 8 {
			want = King
		} else if i >= 4 {
			want = Queen
		}
		if c.Rank != want {
			ramp = false
			break
		}
	}
	if ramp {
		t.Fatal("chaos castle is still ordered J,Q,K")
	}
}

// TestChaosIsReproducible proves a chaos seed replays identically.
func TestChaosIsReproducible(t *testing.T) {
	rules := DefaultRules()
	rules.Chaos = true
	a := NewGame(SetupOptions{Seed: "CHAOS-2", NumPlayers: 1, Rules: rules}, NewSeededRand("CHAOS-2"))
	b := NewGame(SetupOptions{Seed: "CHAOS-2", NumPlayers: 1, Rules: rules}, NewSeededRand("CHAOS-2"))
	for i := range a.Castle {
		if a.Castle[i].ID != b.Castle[i].ID {
			t.Fatalf("castle differs at %d: %s vs %s", i, a.Castle[i].ID, b.Castle[i].ID)
		}
	}
	if a.RGNDraws != b.RGNDraws {
		t.Fatalf("rng draws %d vs %d", a.RGNDraws, b.RGNDraws)
	}
	if a.Rules.Chaos != true {
		t.Fatal("chaos flag not stored in the game")
	}
}

// TestStandardCastleUnaffected keeps the classic ramp intact when chaos is off.
func TestStandardCastleUnaffected(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "STANDARD-1", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("STANDARD-1"))
	// The first Jack is the revealed enemy; the rest keep the ramp order.
	if g.Enemy.Rank != Jack {
		t.Fatalf("first enemy = %s, want a Jack", g.Enemy.ID)
	}
	for i, c := range g.Castle {
		want := Jack
		if i >= 7 {
			want = King
		} else if i >= 3 {
			want = Queen
		}
		if c.Rank != want {
			t.Fatalf("castle[%d] = %s, want rank %d", i, c.ID, want)
		}
	}
}

// --- multi-card payment (step 4) -------------------------------------------------

// step4Game puts the game in step 4 owing `owed` damage with the given hand.
func step4Game(t *testing.T, owed int, hand ...string) Game {
	t.Helper()
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20; shields trim it below
		g.Players[0].Hand = cards(t, hand...)
		g.Step4Required = owed
		g.Step4Paid = 0
		g.Phase = Step4Pre
	})
	return g
}

// TestPayDamageWithSeveralCards covers the batch form: cards are discarded in the
// order given and the running total ends the turn at the threshold.
func TestPayDamageWithSeveralCards(t *testing.T) {
	g := step4Game(t, 17, "10C", "7C", "2D")
	g, envs := apply(t, g, PayDamage{CardIDs: ids("10C", "7C")})
	if g.Step4Paid != 17 {
		t.Fatalf("step4Paid = %d, want 17", g.Step4Paid)
	}
	if len(g.Players[0].Hand) != 1 || g.Players[0].Hand[0].ID != "2D" {
		t.Fatalf("hand = %v, want only 2D", g.Players[0].Hand)
	}
	if g.Phase == Step4Pre || g.Phase == Step4Discard {
		t.Fatalf("phase = %s, want the turn to have ended", g.Phase)
	}
	var payments int
	for _, ev := range envs {
		if ev.Type == EvSuffer {
			payments++
		}
	}
	if payments != 2 {
		t.Fatalf("got %d payment events, want one per card", payments)
	}
}

// TestPayDamageEventsNameThePayer: a payment is its own action, so the client needs
// the seat on every suffer event. Without it the seat bubble kept the previous attack
// and the defence reveal never fired.
func TestPayDamageEventsNameThePayer(t *testing.T) {
	g := step4Game(t, 10, "10C", "2D")
	_, envs := apply(t, g, PayDamage{CardIDs: ids("10C")})
	suffer := 0
	for _, ev := range envs {
		if ev.Type != EvSuffer {
			continue
		}
		suffer++
		if ev.Seat != g.ActiveSeat {
			t.Errorf("suffer seat = %d, want the payer %d", ev.Seat, g.ActiveSeat)
		}
	}
	if suffer != 1 {
		t.Fatalf("got %d suffer events, want 1", suffer)
	}
}

// TestPayDamageRejectsOvershootBeforeLastCard: the stop-at-threshold rule applies to
// every card but the last, so a batch may not keep discarding once the total is met.
func TestPayDamageRejectsOvershootBeforeLastCard(t *testing.T) {
	g := step4Game(t, 10, "10C", "2D")
	if err := applyErr(t, g, PayDamage{CardIDs: ids("10C", "2D")}); err == nil {
		t.Fatal("want an error when an earlier card already covers the attack")
	}
}

// TestPayDamageAllowsOvershootWhenEnabled: with over-paying on, the batch is legal
// and the player ends the payment themselves.
func TestPayDamageAllowsOvershootWhenEnabled(t *testing.T) {
	g := step4Game(t, 10, "10C", "2D")
	g.Rules.AllowOverpayDiscard = true
	g, _ = apply(t, g, PayDamage{CardIDs: ids("10C", "2D")})
	if g.Step4Paid != 12 {
		t.Fatalf("step4Paid = %d, want 12", g.Step4Paid)
	}
	if g.Phase != Step4Discard {
		t.Fatalf("phase = %s, want payment still open", g.Phase)
	}
	g, _ = apply(t, g, EndPayment{})
	if g.Phase == Step4Discard {
		t.Fatal("EndPayment did not close the turn")
	}
}

// TestPayDamageRejectsDuplicates keeps a hand-crafted batch from spending one card twice.
func TestPayDamageRejectsDuplicates(t *testing.T) {
	g := step4Game(t, 20, "10C", "10D")
	if err := applyErr(t, g, PayDamage{CardIDs: ids("10C", "10C")}); err == nil {
		t.Fatal("want an error for a repeated card")
	}
}

// TestPayDamageAcceptsLegacySingleCard keeps the old wire form working.
func TestPayDamageAcceptsLegacySingleCard(t *testing.T) {
	g := step4Game(t, 10, "10C", "2D")
	g, _ = apply(t, g, PayDamage{CardID: "10C"})
	if g.Step4Paid != 10 {
		t.Fatalf("step4Paid = %d, want 10", g.Step4Paid)
	}
}

// TestPayDamageThreeCards checks a batch of three, and that a batch is rejected as a
// whole (nothing is discarded) when one card is missing.
func TestPayDamageThreeCards(t *testing.T) {
	g := step4Game(t, 15, "5C", "5D", "5H", "2S")
	g, _ = apply(t, g, PayDamage{CardIDs: ids("5C", "5D", "5H")})
	if g.Step4Paid != 15 {
		t.Fatalf("step4Paid = %d, want 15", g.Step4Paid)
	}
	if len(g.Players[0].Hand) != 1 {
		t.Fatalf("hand = %v, want one card left", g.Players[0].Hand)
	}

	g2 := step4Game(t, 15, "5C", "5D")
	if err := applyErr(t, g2, PayDamage{CardIDs: ids("5C", "2H")}); err == nil {
		t.Fatal("want an error for a card that is not in hand")
	}
	if g2.Step4Paid != 0 || len(g2.Players[0].Hand) != 2 {
		t.Fatal("a rejected batch must not discard anything")
	}
}

// ---- R-034: the killing blow gives the killer the next turn ---------------

// TestPassOnDefeatMovesTheTurn: a defeated royal hands the fresh royal to the killer
// by default — that is the rulebook, and a table that found it surprising gets the
// easier variant instead of a silent rule change.
func TestPassOnDefeatMovesTheTurn(t *testing.T) {
	build := func(pass bool) Game {
		return coopGame(t, 3, func(g *Game) {
			g.ActiveSeat = 0
			// Everybody holds a card, so the table is not lost and the reveal is the
			// last word on who acts next.
			g.Players[0].Hand = cards(t, "10C", "2C")
			g.Players[1].Hand = cards(t, "2C")
			g.Players[2].Hand = cards(t, "2C")
			// A non-royal below the royal keeps the game going after the kill.
			g.Castle = cards(t, "5C")
			setEnemy(g, t, "JD")
			g.Rules.PassOnDefeat = pass
		})
	}

	// Default: clubs double, so 10♣ deals exactly the 20 a Jack needs, and seat 0
	// takes the next turn.
	kept, _ := apply(t, build(false), Play{CardIDs: ids("10C")})
	if kept.Enemy.ID != "5C" {
		t.Fatalf("enemy = %s, want the next royal", kept.Enemy.ID)
	}
	if kept.ActiveSeat != 0 {
		t.Errorf("active seat = %d, want 0: the killer keeps the turn by default", kept.ActiveSeat)
	}

	// The easier room option passes the turn to the next seat instead.
	passed, evs := apply(t, build(true), Play{CardIDs: ids("10C")})
	if passed.ActiveSeat != 1 {
		t.Fatalf("active seat = %d, want 1 with pass-on-defeat", passed.ActiveSeat)
	}
	var reveal *Event
	for i := range evs {
		if evs[i].Type == EvEnemyReveal {
			reveal = &evs[i]
		}
	}
	if reveal == nil || reveal.Seat != 1 {
		t.Errorf("reveal event = %+v, want it to name seat 1", reveal)
	}
}
