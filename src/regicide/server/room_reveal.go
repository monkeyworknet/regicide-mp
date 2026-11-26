package server

import (
	"time"

	"regicide/engine"
)

// The table reveal the browser plays for one action. These mirror web/app.js
// (REVEAL_HOLD, REVEAL_FLY and the `REVEAL_FLY + 40` teardown; ROYAL_HOLD and the
// 260 ms fade). They are duplicated here on purpose: the server paces the bots, and
// it must not read the client's DOM to learn how long a panel lasts.
// TestRevealBudget pins them, so a change on either side is a red test.
const (
	revealHold  = 1400 * time.Millisecond
	revealFly   = 420 * time.Millisecond
	revealTail  = 40 * time.Millisecond
	royalHold   = 1300 * time.Millisecond
	royalFade   = 260 * time.Millisecond
	revealPanel = revealHold + revealFly + revealTail
	royalPanel  = royalHold + royalFade
)

// maxRevealBudget bounds one action's hold. The longest real action is a play that
// defeats a royal and reveals the next one: play 1.86 s + defeat 1.56 s + entry 1.56 s
// = 4.98 s. Eight seconds leaves room for a future four-panel action and still stops a
// malformed log from stalling a bot game.
const maxRevealBudget = 8 * time.Second

// revealBudget is how long the reveal for this event batch stays on the screen. It
// counts panels the way web/format.js `revealsFor()` does, because the gate must match
// what a player is shown rather than the number of events: a payment of three cards is
// one panel, and a Jester play is one panel with a different word.
func revealBudget(events []engine.Event) time.Duration {
	var total time.Duration
	paymentSeat := -1 // no payment run open: seats are >= 0
	for _, ev := range events {
		switch ev.Type {
		case engine.EvPlay:
			paymentSeat = -1
			if len(ev.Cards) > 0 {
				total += revealPanel
			}
		case engine.EvSuffer:
			// Consecutive suffer events for one seat are one payment and one panel.
			if ev.Seat != paymentSeat {
				total += revealPanel
				paymentSeat = ev.Seat
			}
		case engine.EvDefeat, engine.EvEnemyReveal:
			paymentSeat = -1
			if len(ev.Cards) > 0 {
				total += royalPanel
			}
		default:
			paymentSeat = -1
		}
	}
	if total > maxRevealBudget {
		return maxRevealBudget
	}
	return total
}

// revealWaitLocked is the remaining blocked time for the table reveal, or zero when
// nothing is on screen. Caller holds sess.mu. It is the one gate every bot driver must
// ask, so the reveal cannot be bypassed by a second caller later.
func (s *Store) revealWaitLocked(sess *Session) time.Duration {
	if sess.revealUntil.IsZero() {
		return 0
	}
	if wait := sess.revealUntil.Sub(s.now()); wait > 0 {
		return wait
	}
	return 0
}
