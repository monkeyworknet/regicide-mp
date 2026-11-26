package server

import (
	"testing"
	"time"

	"regicide/engine"
)

// TestRevealBudget pins the server's mirror of the browser reveal. The numbers are the
// client's own constants (app.js REVEAL_HOLD/REVEAL_FLY/ROYAL_HOLD and their teardown
// timeouts); if either side changes, this test and the browser stage catch it.
func TestRevealBudget(t *testing.T) {
	play := engine.Event{Type: engine.EvPlay, Cards: []engine.CardID{"10S"}}
	suffer := func(seat int) engine.Event {
		return engine.Event{Type: engine.EvSuffer, Seat: seat, Cards: []engine.CardID{"2D"}}
	}
	royal := func(kind string) engine.Event {
		return engine.Event{Type: kind, Cards: []engine.CardID{"JS"}}
	}
	cases := []struct {
		name   string
		events []engine.Event
		want   time.Duration
	}{
		{"nothing", nil, 0},
		{"a play holds and flies", []engine.Event{play}, 1860 * time.Millisecond},
		{"a Jester play is one panel", []engine.Event{{Type: engine.EvPlay, Cards: []engine.CardID{"J1"}}}, 1860 * time.Millisecond},
		{"a play with no cards shows nothing", []engine.Event{{Type: engine.EvPlay}}, 0},
		{"a payment of three cards is one panel", []engine.Event{suffer(1), suffer(1), suffer(1)}, 1860 * time.Millisecond},
		{"two seats paying is two panels", []engine.Event{suffer(0), suffer(1)}, 3720 * time.Millisecond},
		{"a separated payment is a new panel", []engine.Event{suffer(1), {Type: engine.EvDamage}, suffer(1)}, 3720 * time.Millisecond},
		{"a defeat and the next royal enters", []engine.Event{royal(engine.EvDefeat), royal(engine.EvEnemyReveal)}, 3120 * time.Millisecond},
		{"a royal with no cards shows nothing", []engine.Event{{Type: engine.EvDefeat}}, 0},
		{"a long turn is not capped", []engine.Event{play, royal(engine.EvDefeat), royal(engine.EvEnemyReveal)}, 4980 * time.Millisecond},
		{"a malformed log is capped", []engine.Event{play, play, play, play, play}, maxRevealBudget},
	}
	for _, tc := range cases {
		if got := revealBudget(tc.events); got != tc.want {
			t.Errorf("%s: revealBudget = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// botRoom gives a started room a bot in seat 1 holding the move, with a frozen clock so
// the gate can be stepped without sleeping.
func botRoom(t *testing.T) (*roomClient, *Session, *time.Time) {
	t.Helper()
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	base := time.Now()
	host.store.now = func() time.Time { return base }
	sess.mu.Lock()
	sess.Room.Seats[1].Bot = true
	sess.Game.ActiveSeat = 1
	sess.Game.SeatChoice = nil
	sess.mu.Unlock()
	return host, sess, &base
}

// TestBotWaitsForReveal is the operator's request as a test: while a reveal is on the
// table, the bot does not act; once it clears, the next action follows.
func TestBotWaitsForReveal(t *testing.T) {
	host, sess, base := botRoom(t)

	sess.mu.Lock()
	sess.revealUntil = base.Add(900 * time.Millisecond)
	seq := sess.Seq
	sess.mu.Unlock()

	if wait := host.store.botStep(sess); wait < 800*time.Millisecond || wait > 900*time.Millisecond {
		t.Fatalf("botStep wait = %v, want the 900ms of reveal left", wait)
	}
	sess.mu.Lock()
	if sess.Seq != seq {
		t.Fatalf("bot acted while a reveal was on the table: seq %d -> %d", seq, sess.Seq)
	}
	sess.mu.Unlock()

	// The clock passes the reveal: the next look acts once, then waits a beat.
	later := base.Add(time.Second)
	host.store.now = func() time.Time { return later }
	if wait := host.store.botStep(sess); wait != host.store.botInterval() {
		t.Fatalf("botStep wait after the reveal = %v, want one pace", wait)
	}
	sess.mu.Lock()
	if sess.Seq == seq {
		t.Fatalf("bot did not act after the reveal cleared")
	}
	sess.mu.Unlock()
}

// TestHumanActionStampsReveal: a human play is news to the other seats, so it stamps
// the same gate a bot's play does. The budget must equal the reveal of the events the
// action produced, not a constant.
func TestHumanActionStampsReveal(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	base := time.Now()
	host.store.now = func() time.Time { return base }

	sess.mu.Lock()
	sess.Game.ActiveSeat = 0
	sess.Game.SeatChoice = nil
	var play engine.Play
	for _, act := range engine.LegalActions(&sess.Game) {
		if p, ok := act.(engine.Play); ok {
			play = p
			break
		}
	}
	sess.mu.Unlock()
	if len(play.CardIDs) == 0 {
		t.Fatalf("the deal offered no play action")
	}

	_, events, _, err := host.store.ApplyFor(sess, 0, "", -1, play)
	if err != nil {
		t.Fatalf("ApplyFor: %v", err)
	}
	want := revealBudget(events)
	if want == 0 {
		t.Fatalf("a play event batch had no reveal budget: %v", events)
	}
	sess.mu.Lock()
	got := sess.revealUntil.Sub(base)
	sess.mu.Unlock()
	if got != want {
		t.Fatalf("revealUntil = %v, want %v", got, want)
	}
}

// TestPlayAgainClearsRevealGate: a new deal must not inherit the finished game's gate.
func TestPlayAgainClearsRevealGate(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.revealUntil = time.Now().Add(time.Minute)
	sess.Game.Phase = engine.Won
	sess.mu.Unlock()

	if err := host.store.PlayAgain(sess, 0, -1); err != nil {
		t.Fatalf("PlayAgain: %v", err)
	}
	sess.mu.Lock()
	gate := sess.revealUntil
	sess.mu.Unlock()
	if !gate.IsZero() {
		t.Fatalf("re-deal kept the old gate: %v", gate)
	}
}

// TestGCKeepsASessionThatActedDuringTheDrain pins the race the 0.9.8 whole-tree review
// found: GC releases the store lock to drain a stale session's writes, and an action
// can land in that window. Deleting the table then loses a live game (the next poll
// 404s and the queued persist recreates a headerless file).
func TestGCKeepsASessionThatActedDuringTheDrain(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	code := sess.Room.Code
	// The table is stale by the clock GC will use.
	base := time.Now()
	host.store.now = func() time.Time { return base.Add(3 * time.Hour) }
	// In the window between GC's idle check and its drain, a player acts. The hook is
	// the same kind of test seam as writeHook: it makes an otherwise timing-dependent
	// window deterministic.
	host.store.gcHook = func(s *Session) {
		s.mu.Lock()
		seat := s.Game.ActiveSeat
		var play engine.Play
		for _, act := range engine.LegalActions(&s.Game) {
			if p, ok := act.(engine.Play); ok {
				play = p
				break
			}
		}
		s.mu.Unlock()
		if len(play.CardIDs) == 0 {
			t.Errorf("no play in the hook")
			return
		}
		if _, _, _, err := host.store.ApplyFor(s, seat, "", -1, play); err != nil {
			t.Errorf("action during the drain: %v", err)
		}
	}

	if n := host.store.GC(time.Hour); n != 0 {
		t.Fatalf("GC deleted %d live session(s)", n)
	}
	if _, ok := host.store.ByCode(code); !ok {
		t.Fatalf("the table that acted during the drain is gone")
	}
	sess.mu.Lock()
	gate := sess.revealUntil
	sess.mu.Unlock()
	if gate.IsZero() {
		t.Fatalf("the action in the hook did not reach the session")
	}
}
