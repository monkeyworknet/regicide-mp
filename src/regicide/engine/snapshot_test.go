package engine

import "testing"

// The board reads the enemy panel as "landing / base", so the snapshot must carry
// the enemy's own attack value (and must not invent one for ordinary cards).
func TestEnemyViewCarriesAttack(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "AAAA-BBBB-CCCC", Theme: "official", Rules: DefaultRules(), NumPlayers: 1}, NewSeededRand("AAAA-BBBB-CCCC"))
	s := Snapshot(g)
	if s.Enemy == nil {
		t.Fatal("no enemy in the opening snapshot")
	}
	if s.Enemy.Attack != 10 {
		t.Fatalf("first enemy attack = %d, want 10 (a Jack)", s.Enemy.Attack)
	}
	for _, c := range s.Hand {
		if c.Attack != 0 {
			t.Fatalf("hand card %s carries attack %d, want 0", c.ID, c.Attack)
		}
	}
}

// TestSnapshotFillsEffectiveShield: the field was declared and never assigned, so every
// consumer read 0 for a shielded royal. The live bot seat and the client shield note both
// depend on it.
func TestSnapshotFillsEffectiveShield(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "KH") // 20 attack
		g.Players[0].Hand = cards(t, "5S", "2C", "3C", "4C", "6C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5S")})
	st := SnapshotFor(g, View{Seat: 0})
	if st.EffectiveShield != 5 {
		t.Fatalf("effectiveShield = %d, want 5", st.EffectiveShield)
	}
	if st.Incoming != 15 {
		t.Fatalf("incoming = %d, want 15", st.Incoming)
	}
}
