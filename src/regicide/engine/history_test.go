package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLogStampsTurn proves every event carries the turn it happened in, so the UI
// can group the history without guessing boundaries.
func TestLogStampsTurn(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "history-turn", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("history-turn"))
	if g.TurnNo != 1 {
		t.Fatalf("turn = %d, want 1", g.TurnNo)
	}
	// The setup event is emitted before the turn counter is set.
	if first := g.Log[0]; first.Type != EvSetup || first.Turn != 0 {
		t.Fatalf("setup event = %+v, want turn 0", first)
	}
	for _, e := range g.Log[1:] {
		if e.Turn != 1 {
			t.Fatalf("event %s turn = %d, want 1", e.Type, e.Turn)
		}
	}

	next, evs, err := Transition(g, Play{CardIDs: []CardID{g.hand()[0].ID}}, NewSeededRand("history-turn"))
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	for _, e := range evs {
		if e.Turn != 1 {
			t.Fatalf("play event %s turn = %d, want 1", e.Type, e.Turn)
		}
	}
	if len(next.Log) <= len(g.Log) {
		t.Fatalf("history did not grow: %d -> %d", len(g.Log), len(next.Log))
	}
}

// TestLogCapKeepsNewest proves the stored history is bounded and that the cap
// drops the oldest events only.
func TestLogCapKeepsNewest(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "history-cap", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("history-cap"))
	for i := 0; i < maxLog+40; i++ {
		g.log(Event{Type: EvTurnEnd, Amount: i + 1})
	}
	if len(g.Log) != maxLog+40+2 {
		t.Fatalf("log grew to %d before trimming", len(g.Log))
	}
	trimmed := trimLogTo(g.Log, maxLog)
	if len(trimmed) != maxLog {
		t.Fatalf("trimmed to %d, want %d", len(trimmed), maxLog)
	}
	last := trimmed[len(trimmed)-1]
	if last.Amount != maxLog+40 {
		t.Fatalf("newest event lost: %+v", last)
	}
	if trimmed[0].Amount != last.Amount-maxLog+1 {
		t.Fatalf("oldest kept = %d, want %d", trimmed[0].Amount, last.Amount-maxLog+1)
	}
}

// TestTransitionEventsSurviveTrim is the regression guard for the offset: a
// transition on a full log must still return exactly its own events.
func TestTransitionEventsSurviveTrim(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "history-trim", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("history-trim"))
	for len(g.Log) < maxLog {
		g.log(Event{Type: EvTurnEnd, Amount: len(g.Log)})
	}
	if len(g.Log) != maxLog {
		t.Fatalf("precondition: log = %d", len(g.Log))
	}
	card := g.hand()[0].ID
	next, evs, err := Transition(g, Play{CardIDs: []CardID{card}}, NewSeededRand("history-trim"))
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if len(evs) == 0 || evs[0].Type != EvPlay {
		t.Fatalf("events = %+v, want a play first", evs)
	}
	if evs[0].Cards[0] != card {
		t.Fatalf("event card = %s, want %s", evs[0].Cards[0], card)
	}
	if len(next.Log) != maxLog {
		t.Fatalf("post-transition log = %d, want %d", len(next.Log), maxLog)
	}
}

// TestSnapshotHistoryHidesSecrets proves the exposed history is safe to send to
// the browser: it names played cards, never undrawn tavern or castle cards.
func TestSnapshotHistoryHidesSecrets(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "history-secret", NumPlayers: 1, Rules: DefaultRules()}, NewSeededRand("history-secret"))
	snap := Snapshot(g)
	if len(snap.Log) == 0 {
		t.Fatal("snapshot history is empty")
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(blob)
	for _, c := range g.Tavern {
		if strings.Contains(text, `"`+string(c.ID)+`"`) {
			t.Fatalf("snapshot leaks tavern card %s", c.ID)
		}
	}
	for _, c := range g.Castle {
		if c.ID == g.Enemy.ID {
			continue // the current enemy is revealed on purpose
		}
		if strings.Contains(text, `"`+string(c.ID)+`"`) {
			t.Fatalf("snapshot leaks castle card %s", c.ID)
		}
	}
}
