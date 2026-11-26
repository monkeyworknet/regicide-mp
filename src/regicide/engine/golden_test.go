package engine

import (
	"encoding/json"
	"os"
	"testing"
)

// goldenDeal is produced by the independent reference implementation in
// src/regicide/tools/rngref (see docs/PLAN.md §4.4). If this test fails after an
// intentional shuffle change, bump engine.Version and regenerate the file.
type goldenDeal struct {
	Seed     string   `json:"seed"`
	Hand     []string `json:"hand"`
	Tavern   []string `json:"tavern"`
	Enemy    string   `json:"enemy"`
	Castle   []string `json:"castle"`
	RGNDraws int      `json:"rngDraws"`
}

func loadGolden(t *testing.T) goldenDeal {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden-deal.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g goldenDeal
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	// Schema checks: a truncated or hand-edited fixture must not silently pass.
	if g.Seed == "" || g.Enemy == "" {
		t.Fatal("golden fixture is missing seed or enemy")
	}
	if len(g.Hand) != 8 || len(g.Tavern) != 32 || len(g.Castle) != 11 {
		t.Fatalf("golden fixture sizes: hand %d tavern %d castle %d", len(g.Hand), len(g.Tavern), len(g.Castle))
	}
	if g.RGNDraws <= 0 {
		t.Fatalf("golden fixture rngDraws = %d", g.RGNDraws)
	}
	return g
}

func TestGoldenDealMatchesReference(t *testing.T) {
	golden := loadGolden(t)
	g := NewGame(SetupOptions{
		Seed:       golden.Seed,
		Rules:      DefaultRules(),
		NumPlayers: 1,
	}, NewSeededRand(golden.Seed))

	if got := idsOf2(g.Players[0].Hand); !equalStrings(got, golden.Hand) {
		t.Fatalf("hand = %v, want %v", got, golden.Hand)
	}
	if got := idsOf2(g.Tavern); !equalStrings(got, golden.Tavern) {
		t.Fatalf("tavern = %v, want %v", got, golden.Tavern)
	}
	if g.Enemy.ID != CardID(golden.Enemy) {
		t.Fatalf("enemy = %s, want %s", g.Enemy.ID, golden.Enemy)
	}
	if got := idsOf2(g.Castle); !equalStrings(got, golden.Castle) {
		t.Fatalf("castle = %v, want %v", got, golden.Castle)
	}
	if g.RGNDraws != golden.RGNDraws {
		t.Fatalf("rng draws = %d, want %d", g.RGNDraws, golden.RGNDraws)
	}
}

func TestRndResumeAtOffset(t *testing.T) {
	// NewRandAt(seed, n) must produce the same stream as n draws then continue.
	full := NewSeededRand("RESUME-SEED")
	want := make([]uint64, 0, 8)
	for i := 0; i < 3; i++ {
		full.Uint64()
	}
	for i := 0; i < 5; i++ {
		want = append(want, full.Uint64())
	}

	resumed := NewRandAt("RESUME-SEED", 3)
	for i, w := range want {
		if got := resumed.Uint64(); got != w {
			t.Fatalf("draw %d = %d, want %d", i, got, w)
		}
	}
	if NewRandAt("RESUME-SEED", 0).Uint64() != NewSeededRand("RESUME-SEED").Uint64() {
		t.Fatal("offset 0 must equal a fresh stream")
	}
}

func idsOf2(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = string(c.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSpadesBeforeJester documents dossier §6.1's highest-risk interaction: spades
// played against a spades enemy accumulate but do not reduce until immunity is
// cancelled by a Jester.
func TestSpadesBeforeJester(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS") // attack 10
		g.Players[0].Hand = cards(t, "8S", "J1", "2C")
		g.JestersLeft = 1
		g.Players[0].Hand = append(g.Players[0].Hand, cards(t, "3C")...)
	})
	g, _ = apply(t, g, Play{CardIDs: ids("8S")})
	if g.Step4Required != 10 {
		t.Fatalf("required = %d, want 10 while the spades enemy is still immune", g.Step4Required)
	}
	// A Jester cannot be played in solo (it is set aside), so cancel immunity the
	// only way the rules allow and re-evaluate the shield.
	g.ImmunityCancelled = true
	if got := g.incomingDamage(); got != 2 {
		t.Fatalf("incoming = %d, want 2 once immunity is cancelled (10 - 8 shield)", got)
	}
}

// TestEmptyHandRescuedByFlip covers D-1's positive case: window A runs before the
// cannot-act check, so a flip can save a player who starts a turn with no cards.
func TestEmptyHandRescuedByFlip(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = nil
		g.Tavern = cards(t, "2C", "3C", "4C", "5C")
		g.JestersLeft = 2
	})
	g.enterTurnStart()
	if g.Phase == Lost {
		t.Fatal("a player with a Jester left has not lost yet")
	}
	g, _ = apply(t, g, FlipJester{})
	if g.Phase == Lost {
		t.Fatalf("flip should have rescued the hand (reason %s)", g.LostReason)
	}
	if len(g.Players[0].Hand) != 4 {
		t.Fatalf("hand = %d, want 4", len(g.Players[0].Hand))
	}
}

// TestWindowBAllowsSecondFlip covers §8 #11: both jesters may be spent in one
// step-4 window as long as damage has not been suffered.
func TestWindowBAllowsSecondFlip(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC") // attack 20
		g.Players[0].Hand = cards(t, "2C")
		g.Tavern = cards(t, "3C", "4C") // only two cards: the first refill cannot pay
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if g.Phase != Step4Pre {
		t.Fatalf("phase = %s, want step4_pre", g.Phase)
	}
	g, _ = apply(t, g, FlipJester{})
	if g.Phase != Step4Pre {
		t.Fatalf("phase = %s, want the window to stay open for the second Jester", g.Phase)
	}
	if g.JestersLeft != 1 {
		t.Fatalf("jesters left = %d, want 1", g.JestersLeft)
	}
}

// TestEventsAreOrderedAndComplete guards the client animation contract: events
// arrive in the order they happened, terminal events are not duplicated, and a
// turn that ends normally reports the attack before the turn boundary.
func TestEventsAreOrderedAndComplete(t *testing.T) {
	// Loss path: no payment is possible, so the lose event closes the sequence.
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC") // attack 10
		g.Players[0].Hand = cards(t, "2C")
		g.JestersLeft = 0
		g.Tavern = nil
	})
	_, evs, err := Transition(g, Play{CardIDs: ids("2C")}, NewScriptedRand())
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	assertEventOrder(t, evs, []string{EvPlay, EvDamage, EvLose})

	// Survival path: pay exactly, so the enemy attack and turn end precede the
	// next turn's state (and no lose event exists).
	g = scenario(t, func(g *Game) {
		setEnemy(g, t, "JC") // attack 10
		g.JestersLeft = 0
		g.Players[0].Hand = cards(t, "2C", "10C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	_, envs, err := Transition(g, PayDamage{CardID: "10C"}, NewScriptedRand())
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	assertEventOrder(t, envs, []string{EvSuffer, EvEnemyAttack, EvTurnEnd})

	var loseCount int
	for _, e := range g.Log {
		if e.Type == EvLose {
			loseCount++
		}
	}
	if loseCount != 0 {
		t.Fatalf("the input state must not be mutated (lose events: %d)", loseCount)
	}
}

func assertEventOrder(t *testing.T, evs []Event, want []string) {
	t.Helper()
	got := make([]string, len(evs))
	for i, e := range evs {
		got[i] = e.Type
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

// TestCardsAreCopiedIntoHistory makes sure a caller cannot rewrite history by
// mutating the action slice it passed in.
func TestCardsAreCopiedIntoHistory(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KC")
		g.Players[0].Hand = cards(t, "5C")
	})
	action := Play{CardIDs: ids("5C")}
	next, _, err := Transition(g, action, NewScriptedRand())
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	action.CardIDs[0] = "9H"
	for _, e := range next.Log {
		for _, id := range e.Cards {
			if id == "9H" {
				t.Fatal("history was mutated through the caller's action slice")
			}
		}
	}
}
