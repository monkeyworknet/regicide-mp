package bot

import (
	"testing"

	"regicide/engine"
)

func TestDebugRefusal(t *testing.T) {
	seed := "LEGAL-greedy-XC"
	g := engine.NewGame(engine.SetupOptions{Seed: seed, Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: 3}, engine.NewSeededRand(seed))
	for step := 0; step < 400; step++ {
		if g.Phase == engine.Won || g.Phase == engine.Lost {
			return
		}
		legal := engine.LegalActions(&g)
		if len(legal) == 0 {
			return
		}
		actor := g.ActiveSeat
		if g.SeatChoice != nil {
			actor = g.SeatChoice.By
		}
		obs, _ := Observe(g, actor)
		want := Greedy(obs)
		next, _, err := engine.Transition(g, want, engine.NewRandAt(g.Seed, g.RGNDraws))
		if err != nil {
			t.Fatalf("step %d phase=%s want=%s err=%q hand=%v required=%d choices=%v counts=%v",
				step, g.Phase, engine.ActionName(want), err.Error(), ids2(obs.Hand), obs.Required, obs.Choices, obs.HandCounts)
		}
		g = next
	}
}

func ids2(cs []engine.Card) []string {
	var out []string
	for _, c := range cs {
		out = append(out, string(c.ID))
	}
	return out
}
