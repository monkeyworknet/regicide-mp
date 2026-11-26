package engine

import (
	"math/rand/v2"
	"testing"
)

// RandomSeedFrom derives deterministic test seeds.
func RandomSeedFrom(i int) string {
	r := rand.New(rand.NewPCG(uint64(i), 0x9e3779b9))
	alphabet := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, 12)
	for j := range out {
		out[j] = alphabet[r.IntN(len(alphabet))]
	}
	return string(out)
}

// TestSelfPlayTerminatesAndAdvances is a liveness + progression guard. It does
// not assert a win rate: the greedy policy used here is deliberately simple (it
// does not plan shield-before-damage sequences), and Regicide punishes that, so
// its win rate is near zero by design. What matters for the engine is that games
// always finish and that ordinary play actually progresses through the castle.
func TestSelfPlayTerminatesAndAdvances(t *testing.T) {
	const games = 120
	finished, progressed := 0, 0
	bestDefeated := 0
	for i := 0; i < games; i++ {
		seed := RandomSeedFrom(i)
		g := NewGame(SetupOptions{Seed: seed, SeedRandom: true, Rules: DefaultRules(), NumPlayers: 1}, NewSeededRand(seed))
		steps := 0
		for !g.gameOver() {
			if steps >= 4000 {
				t.Fatalf("seed %s did not terminate in 4000 transitions (phase %s)", seed, g.Phase)
			}
			legal := LegalActions(&g)
			if len(legal) == 0 {
				t.Fatalf("dead state at seed %s: phase %s", seed, g.Phase)
			}
			next, _, err := Transition(g, legal[steps%len(legal)], NewRandAt(seed, g.RGNDraws))
			if err != nil {
				t.Fatalf("seed %s step %d: %v", seed, steps, err)
			}
			g = next
			steps++
		}
		finished++
		defeated := 12 - 1 - len(g.Castle)
		if defeated > bestDefeated {
			bestDefeated = defeated
		}
		if defeated >= 3 {
			progressed++
		}
	}
	t.Logf("random legal play: %d/%d finished, %d reached 3+ enemies, best %d/12", finished, games, progressed, bestDefeated)
	if finished != games {
		t.Fatalf("only %d/%d games finished", finished, games)
	}
	if progressed == 0 {
		t.Fatal("no game defeated even three enemies: the rules economy is broken")
	}
}

// TestDamageBudgetIsSufficient documents the arithmetic that makes a win
// possible: every card in the deck can eventually be played, and their values
// (400) must exceed the castle's total health (360).
func TestDamageBudgetIsSufficient(t *testing.T) {
	total := 0
	for _, c := range buildTavern(1, NewSeededRand("BUDGET")) {
		total += c.PlayValue()
	}
	for _, c := range buildCastle(false, NewSeededRand("BUDGET")) {
		total += c.PlayValue()
	}
	required := 4*20 + 4*30 + 4*40 // four Jacks, four Queens, four Kings
	if total < required {
		t.Fatalf("deck damage budget %d is below the castle health %d: the game would be unwinnable", total, required)
	}
	t.Logf("damage budget %d vs castle health %d (%+d slack before club doubling)", total, required, total-required)
}

// TestExactKillRecruitsIntoDeck proves the recruit loop returns value: an
// exactly-killed royal goes on top of the tavern deck and can be played later.
func TestExactKillRecruitsIntoDeck(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.Damage = 18
		g.Players[0].Hand = cards(t, "2C", "3C")
		g.Castle = cards(t, "QS")
		g.Tavern = cards(t, "4C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if len(g.Tavern) == 0 || g.Tavern[0].ID != "JC" {
		t.Fatalf("recruited card is not on top of the tavern deck")
	}
	// Draw it back with a diamond and check it plays as a 10.
	g.Phase = Step1Await
	g.Players[0].Hand = cards(t, "5D")
	g.Tavern = append([]Card{parseCard(t, "JC")}, g.Tavern...)
	g, _ = apply(t, g, Play{CardIDs: ids("5D")})
	found := false
	for _, c := range g.Players[0].Hand {
		if c.ID == "JC" {
			found = true
		}
	}
	if !found {
		t.Fatal("the recruited Jack was not drawn back into the hand")
	}
}
