package engine

import "testing"

// TestLegalPlaysEnumeratesEverySubset is the regression guard for the review
// finding: with three cards of one rank, all three pairs must be offered, because
// the suits in the play decide whether clubs double, hearts heal, diamonds draw or
// spades shield. Offering only cards[:size] hid legal, materially different moves.
func TestLegalPlaysEnumeratesEverySubset(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "SUBSET-1", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("SUBSET-1"))
	// Force a known hand: three 5s (legal pair/triple values ≤ 10) plus filler.
	g.setHand([]Card{
		NewCard(5, Clubs), NewCard(5, Hearts), NewCard(5, Spades),
		NewCard(9, Diamonds), NewCard(2, Clubs),
	})

	plays := LegalPlays(&g)
	pairs := map[string]bool{}
	for _, p := range plays {
		if len(p.CardIDs) == 2 {
			pairs[pairKey(p.CardIDs[0], p.CardIDs[1])] = true
		}
	}
	want := [][2]CardID{
		{"5C", "5H"}, {"5C", "5S"}, {"5H", "5S"},
	}
	for _, w := range want {
		if !pairs[pairKey(w[0], w[1])] {
			t.Errorf("missing legal pair %s+%s (offered pairs: %v)", w[0], w[1], keysOf(pairs))
		}
	}

	// Every offered play must pass validation, and every play must carry the
	// attack the server previews.
	g2 := NewGame(SetupOptions{Seed: "SUBSET-2", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("SUBSET-2"))
	for _, p := range LegalPlays(&g2) {
		if _, err := ValidatePlay(&g2, p.CardIDs); err != nil {
			t.Fatalf("offered play %v does not validate: %v", p.CardIDs, err)
		}
		view := actionView(&g2, p)
		if view.Attack <= 0 {
			t.Fatalf("play %v has attack %d", p.CardIDs, view.Attack)
		}
	}
}

// TestPreviewAttackMatchesDamage proves the number on the Play button equals the
// damage the transition actually applies (clubs doubling included).
func TestPreviewAttackMatchesDamage(t *testing.T) {
	checked := 0
	for _, seed := range []string{"PREVIEW-1", "PREVIEW-2", "PREVIEW-3", "PREVIEW-4"} {
		g := NewGame(SetupOptions{Seed: seed, NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand(seed))
		if g.Phase != Step1Await {
			continue
		}
		for _, p := range LegalPlays(&g) {
			want := actionView(&g, p).Attack
			_, evs, err := Transition(g, p, NewSeededRand(seed+"-play"))
			if err != nil {
				continue // the enemy may be immune to the power, but a play is legal
			}
			// The play event records the raw attack; the damage event records the
			// amount after suit powers, which is what the button must preview.
			var got int
			found := false
			for _, ev := range evs {
				if ev.Type == EvDamage {
					got = ev.Amount
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("seed %s: play %v produced no damage event", seed, p.CardIDs)
			}
			if got != want {
				t.Fatalf("seed %s: play %v preview %d, dealt %d", seed, p.CardIDs, want, got)
			}
			checked++
		}
	}
	if checked < 20 {
		t.Fatalf("only %d plays checked", checked)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
