package bot

import (
	"reflect"
	"testing"

	"regicide/engine"
)

// TestFromViewMatchesObserve pins the bench/server parity: PlayStrict sees Observe(),
// a real bot seat sees FromView(), and a difference between them would mean the bench
// is measuring a policy the server does not run. Compare every decision-relevant field
// at several points in a real game.
func TestFromViewMatchesObserve(t *testing.T) {
	for _, seats := range []int{1, 3} {
		testFromViewMatchesObserve(t, seats)
	}
}

func testFromViewMatchesObserve(t *testing.T, seats int) {
	g := engine.NewGame(engine.SetupOptions{
		Seed: "FROMVIEW-PARITY", Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: seats,
	}, engine.NewSeededRand("FROMVIEW-PARITY"))
	policy := DefaultPolicy()
	for step := 0; step < 30; step++ {
		if g.Phase == engine.Won || g.Phase == engine.Lost {
			break
		}
		actor := g.ActiveSeat
		if g.SeatChoice != nil {
			actor = g.SeatChoice.By
		}
		for seat := 0; seat < len(g.Players); seat++ {
			want, ok := Observe(g, seat)
			if !ok {
				t.Fatalf("step %d: Observe refused seat %d", step, seat)
			}
			st := engine.SnapshotFor(g, engine.View{Seat: seat})
			got, ok := FromView(st)
			if !ok {
				t.Fatalf("step %d: FromView refused seat %d", step, seat)
			}
			// The legal flags belong to the seat to act; a projection for any other
			// seat has no Legal list, so compare the full observation only for the actor.
			if seat == actor && !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d seat %d: FromView and Observe disagree\ngot  %+v\nwant %+v", step, seat, got, want)
			}
			// At later states the two constructors differ only where a projection
			// legitimately hides information; compare the fields the policy reads.
			checkField(t, step, seat, "Shield", got.Shield, want.Shield)
			checkField(t, step, seat, "Tavern", got.Tavern, want.Tavern)
			checkField(t, step, seat, "Discard", got.Discard, want.Discard)
			checkField(t, step, seat, "DiscardDiamonds", got.DiscardDiamonds, want.DiscardDiamonds)
			checkField(t, step, seat, "Required", got.Required, want.Required)
			checkField(t, step, seat, "Health", got.Health, want.Health)
			checkField(t, step, seat, "MaxHand", got.MaxHand, want.MaxHand)
			if !reflect.DeepEqual(got.HandCounts, want.HandCounts) {
				t.Fatalf("step %d seat %d: HandCounts = %v, want %v", step, seat, got.HandCounts, want.HandCounts)
			}
			if len(got.Hand) != len(want.Hand) {
				t.Fatalf("step %d seat %d: hand %d vs %d", step, seat, len(got.Hand), len(want.Hand))
			}
		}
		legal := engine.LegalActions(&g)
		if len(legal) == 0 {
			break
		}
		obs, ok := Observe(g, actor)
		if !ok {
			t.Fatalf("step %d: actor %d has no observation", step, actor)
		}
		rng := engine.NewRandAt(g.Seed, g.RGNDraws)
		next, _, err := engine.Transition(g, policy(obs), rng)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		g = next
	}
}

func checkField[T comparable](t *testing.T, step, seat int, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("step %d seat %d: %s = %v, want %v", step, seat, name, got, want)
	}
}
