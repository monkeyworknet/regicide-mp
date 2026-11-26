package engine

import (
	"testing"
)

// benchGame is a three-seat deal at the start of a turn: the shape the server applies
// most actions to. It is rebuilt once per benchmark, never inside the loop.
func benchGame(b *testing.B) (Game, Action) {
	b.Helper()
	g := NewGame(SetupOptions{
		Seed: "BENCH-SEED-01", Theme: "official", Rules: DefaultRules(), NumPlayers: 3,
	}, NewSeededRand("BENCH-SEED-01"))
	for _, act := range LegalActions(&g) {
		if p, ok := act.(Play); ok {
			return g, p
		}
	}
	b.Fatalf("the bench deal offered no play")
	return g, nil
}

// BenchmarkTransition measures one action against a fresh copy of the same deal. The
// copy is what the server would do anyway (Transition returns a new Game), so the
// benchmark includes the state copy, not just the rule work.
func BenchmarkTransition(b *testing.B) {
	base, act := benchGame(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g := base
		if _, _, err := Transition(g, act, NewRandAt(g.Seed, g.RGNDraws)); err != nil {
			b.Fatalf("transition: %v", err)
		}
	}
}

// BenchmarkLegalActions is the projection's cost per seat per frame.
func BenchmarkLegalActions(b *testing.B) {
	g, _ := benchGame(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(LegalActions(&g)) == 0 {
			b.Fatal("no legal actions")
		}
	}
}

// BenchmarkView measures one seat's snapshot, which every poll and every stream frame
// builds.
func BenchmarkView(b *testing.B) {
	g, _ := benchGame(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Snapshot(g); got.ViewerSeat != 0 {
			b.Fatalf("snapshot viewer = %d", got.ViewerSeat)
		}
	}
}
