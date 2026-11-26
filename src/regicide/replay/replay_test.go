package replay

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"regicide/engine"
)

// record plays a game with a simple legal-move policy and returns the finished file.
// It is the same code path the server uses: seed the game, apply one action at a
// time, record the hash after each.
func record(t *testing.T, seats int, seed string, maxActions int) (*Replay, *bytes.Buffer) {
	t.Helper()
	g := engine.NewGame(engine.SetupOptions{
		Seed: seed, Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: seats,
	}, engine.NewSeededRand(seed))

	buf := &bytes.Buffer{}
	w := NewWriter(buf)
	players := make([]Player, seats)
	for i := 0; i < seats; i++ {
		players[i] = Player{Seat: i, Name: "seat" + string(rune('A'+i))}
	}
	if err := w.WriteHeader(Header{
		Mode: "coop", Seed: seed, Theme: "simple", Rules: engine.DefaultRules(),
		NumPlayers: seats, Players: players,
	}); err != nil {
		t.Fatalf("header: %v", err)
	}

	seq := 0
	for step := 0; step < maxActions; step++ {
		if g.Phase == engine.Won || g.Phase == engine.Lost {
			break
		}
		legal := engine.LegalActions(&g)
		if len(legal) == 0 {
			t.Fatalf("no legal actions at step %d", step)
		}
		act := legal[0]
		rng := engine.NewRandAt(g.Seed, g.RGNDraws)
		next, _, err := engine.Transition(g, act, rng)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		seat := g.ActiveSeat
		if g.SeatChoice != nil {
			seat = g.SeatChoice.By
		}
		raw, err := engine.MarshalAction(act)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		seq++
		if err := w.WriteStep(Step{
			Seq: seq, Seat: seat, Action: raw, Hash: engine.StateHash(next),
			Turn: next.TurnNo, Phase: next.Phase.String(), Hands: engine.HandSizes(next),
		}); err != nil {
			t.Fatalf("step: %v", err)
		}
		g = next
	}
	res := Result{
		Phase: g.Phase.String(), Won: g.Phase == engine.Won, TurnNo: g.TurnNo,
		CastleLeft: len(g.Castle), JestersUsed: g.JestersUsed,
	}
	if g.Phase == engine.Lost {
		res.LostReason = g.LostReason.String()
		res.LostSeat = g.LostSeat
	}
	if err := w.WriteResult(res); err != nil {
		t.Fatalf("result: %v", err)
	}

	r, err := Read(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return r, buf
}

func TestReplayRoundTripVerifies(t *testing.T) {
	for _, seats := range []int{1, 2, 3, 4} {
		r, buf := record(t, seats, "REPLAY-ROUNDTRIP-"+string(rune('0'+seats)), 200)
		if len(r.Steps) == 0 {
			t.Fatalf("%d seats: no steps recorded", seats)
		}
		if _, err := r.Verify(); err != nil {
			t.Fatalf("%d seats: verify: %v\n%s", seats, err, buf.String())
		}
	}
}

// TestReplayRefusesANonIncreasingSeq: a file with a step cut out of the middle still
// matches its own hashes, so ordering is checked on its own (F-15).
func TestReplayRefusesANonIncreasingSeq(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(r *Replay)
	}{
		{"equal", func(r *Replay) { r.Steps[1].Seq = r.Steps[0].Seq }},
		{"backwards", func(r *Replay) { r.Steps[1].Seq = r.Steps[0].Seq - 1 }},
	} {
		r, _ := record(t, 3, "REPLAY-SEQ-ORDER", 30)
		if len(r.Steps) < 3 {
			t.Fatalf("%s: only %d steps", tc.name, len(r.Steps))
		}
		tc.mutate(r)
		if _, err := r.Verify(); err == nil || !strings.Contains(err.Error(), "does not advance") {
			t.Errorf("%s: verify = %v, want a sequence-order error", tc.name, err)
		}
	}
}

func TestReplayDetectsATamperedAction(t *testing.T) {
	r, _ := record(t, 3, "REPLAY-TAMPER", 40)
	if len(r.Steps) < 3 {
		t.Fatalf("only %d steps", len(r.Steps))
	}
	// Swap an action for a different legal one: hashes can no longer agree.
	r.Steps[1].Action = json.RawMessage(`{"type":"yield"}`)
	if _, err := r.Verify(); err == nil {
		t.Fatal("a rewritten action verified; the hash chain is not doing its job")
	} else if !strings.Contains(err.Error(), "step 2") {
		t.Fatalf("error does not name the step: %v", err)
	}
}

func TestReplayDetectsATamperedHash(t *testing.T) {
	r, _ := record(t, 2, "REPLAY-HASH", 30)
	r.Steps[0].Hash = strings.Repeat("0", 32)
	if _, err := r.Verify(); err == nil || !strings.Contains(err.Error(), "state hash") {
		t.Fatalf("tampered hash not caught: %v", err)
	}
}

func TestReplayRefusesAForeignFormatOrEngine(t *testing.T) {
	r, buf := record(t, 2, "REPLAY-FORMAT", 10)
	_ = r

	wrongEngine := strings.Replace(buf.String(), `"engine":2`, `"engine":1`, 1)
	if wrongEngine == buf.String() {
		t.Skip("engine version is not 2; the replacement below is meaningless")
	}
	if _, err := Read(strings.NewReader(wrongEngine)); err == nil {
		t.Fatal("a replay from another engine version was accepted")
	}

	wrongFormat := strings.Replace(buf.String(), Format, "some.other.format", 1)
	if _, err := Read(strings.NewReader(wrongFormat)); err == nil {
		t.Fatal("a file of another format was accepted")
	}

	noHeader := strings.SplitN(buf.String(), "\n", 2)[1]
	if _, err := Read(strings.NewReader(noHeader)); err == nil {
		t.Fatal("a file with no header was accepted")
	}
}

func TestReplaySummaryAndTraining(t *testing.T) {
	r, _ := record(t, 3, "REPLAY-SUMMARY", 60)
	sum, err := r.Summarise()
	if err != nil {
		t.Fatalf("summarise: %v", err)
	}
	if sum.Steps != len(r.Steps) {
		t.Errorf("summary steps %d, want %d", sum.Steps, len(r.Steps))
	}
	if sum.Turns == 0 || sum.Theme != "simple" || len(sum.ActionsBySeat) != 3 {
		t.Errorf("summary is thin: %+v", sum)
	}

	recs, err := r.Training()
	if err != nil {
		t.Fatalf("training: %v", err)
	}
	if len(recs) != len(r.Steps) {
		t.Fatalf("training records %d, want %d", len(recs), len(r.Steps))
	}
	// Every record must carry a position, the legal set, and the action taken — and
	// the chosen action must be in the legal set.
	for i, rec := range recs {
		var state engine.ClientState
		if err := json.Unmarshal(rec.State, &state); err != nil {
			t.Fatalf("record %d state: %v", i, err)
		}
		var legal []engine.ActionView
		if err := json.Unmarshal(rec.Legal, &legal); err != nil {
			t.Fatalf("record %d legal: %v", i, err)
		}
		if len(legal) == 0 {
			t.Fatalf("record %d has no legal actions", i)
		}
		if state.ViewerSeat != rec.Seat {
			t.Fatalf("record %d: state is projected for seat %d, not %d", i, state.ViewerSeat, rec.Seat)
		}
		var chosen struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rec.Chosen, &chosen); err != nil {
			t.Fatalf("record %d chosen: %v", i, err)
		}
		found := false
		for _, a := range legal {
			if a.Type == chosen.Type {
				found = true
			}
		}
		if !found {
			t.Fatalf("record %d chose %q, which is not in its legal set", i, chosen.Type)
		}
	}
}

// TestReplayDetectsATamperedActor: the recorded seat is attribution, and the state hash
// covers position, not attribution, so an edited seat used to verify cleanly and then
// credit one player's cards to another in the training corpus (F-44).
func TestReplayDetectsATamperedActor(t *testing.T) {
	r, buf := record(t, 3, "REPLAY-ACTOR", 60)
	// Find a step whose actor is not seat 0, so "0" is a lie that the table can refute.
	at := -1
	for i, step := range r.Steps {
		if step.Seat != 0 {
			at = i
			break
		}
	}
	if at < 0 {
		t.Skip("no non-host step in this game")
	}
	r.Steps[at].Seat = 0
	if _, err := r.Verify(); err == nil {
		t.Fatalf("an edited seat verified cleanly:\n%s", buf.String())
	} else if !strings.Contains(err.Error(), "recorded seat") {
		t.Fatalf("wrong error: %v", err)
	}
}

// TestReplayRefusesADuplicateSeatBook: the seat book is part of the evidence. A file
// that names one seat twice, or a seat outside the deal, cannot be trusted.
func TestReplayRefusesADuplicateSeatBook(t *testing.T) {
	r, _ := record(t, 3, "REPLAY-SEATBOOK", 20)
	r.Header.Players[1].Seat = 0
	if _, err := r.Verify(); err == nil {
		t.Fatal("a seat book with a duplicate seat verified cleanly")
	}
	r2, _ := record(t, 3, "REPLAY-SEATBOOK", 20)
	r2.Header.Players[2].Seat = 9
	if _, err := r2.Verify(); err == nil {
		t.Fatal("a seat outside the deal verified cleanly")
	}
}
