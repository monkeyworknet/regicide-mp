package bot

import (
	"testing"

	"regicide/engine"
)

// benchView is one seat's projection of a dealt three-seat table, which is exactly what
// the bot seat receives. FromView must stay cheap: it runs once per bot tick.
func benchView(b *testing.B) engine.ClientState {
	b.Helper()
	g := engine.NewGame(engine.SetupOptions{
		Seed: "BENCH-BOT-01", Theme: "official", Rules: engine.DefaultRules(), NumPlayers: 3,
	}, engine.NewSeededRand("BENCH-BOT-01"))
	return engine.SnapshotFor(g, engine.View{Seat: 0})
}

func BenchmarkFromView(b *testing.B) {
	st := benchView(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := FromView(st); !ok {
			b.Fatal("FromView refused a player projection")
		}
	}
}
