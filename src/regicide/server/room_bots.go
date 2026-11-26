package server

import (
	"time"

	"regicide/bot"
	"regicide/engine"
)

// AddBotSeat puts a bot in the next empty lobby chair. Bots are seats like any
// other: they count toward the 2-4 player table and are replaceable mid-game.
func (s *Store) AddBotSeat(sess *Session, hostSeat int) (int, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return 0, ErrNoRoom
	}
	if sess.Room.Cancelled {
		return 0, ErrCancelled
	}
	if hostSeat != sess.Room.HostSeat {
		return 0, ErrNotHost
	}
	if !sess.Room.Lobby {
		return 0, ErrRoomStarted
	}
	if len(sess.Room.Seats) >= roomCapacity {
		return 0, ErrRoomFull
	}
	now := s.now()
	seatNo := len(sess.Room.Seats)
	sess.Room.Seats = append(sess.Room.Seats, RoomSeat{
		Seat: seatNo, Name: sess.nextName(), Ready: true, Bot: true,
		JoinedAt: now, LastSeen: now,
	})
	sess.Seq++
	sess.UpdatedAt = now
	return seatNo, s.persistLocked(sess)
}

// SetBotFill turns the 60-second disconnect recovery on or off. Host only, lobby only.
func (s *Store) SetBotFill(sess *Session, hostSeat int, on bool) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return ErrNoRoom
	}
	if sess.Room.Cancelled {
		return ErrCancelled
	}
	if hostSeat != sess.Room.HostSeat {
		return ErrNotHost
	}
	if !sess.Room.Lobby {
		return ErrRoomStarted
	}
	sess.Room.BotFill = on
	sess.Seq++
	sess.UpdatedAt = s.now()
	return s.persistLocked(sess)
}

// SetBotSeat hands an away seat to the bot, or takes it back. Host only, and only for a
// seat the table has already lost: a bot replaces an absent player, it never takes a
// seat from someone who is there (T7.5).
func (s *Store) SetBotSeat(sess *Session, hostSeat, seat int, on bool) error {
	sess.mu.Lock()
	if sess.Room == nil {
		sess.mu.Unlock()
		return ErrNoRoom
	}
	if sess.Room.Cancelled {
		sess.mu.Unlock()
		return ErrCancelled
	}
	if hostSeat != sess.Room.HostSeat {
		sess.mu.Unlock()
		return ErrNotHost
	}
	if sess.Room.Lobby {
		sess.mu.Unlock()
		return ErrRoomStarted
	}
	if seat < 0 || seat >= len(sess.Room.Seats) {
		sess.mu.Unlock()
		return ErrNoRoom
	}
	if on {
		// A bot cannot hold the menu, so it must never hold the crown: the host handing
		// its own chair over would leave cancel/again/watching dead until that seat fell
		// silent for takeAfter, with nothing on screen to explain it.
		if seat == sess.Room.HostSeat {
			sess.mu.Unlock()
			return ErrNotReady
		}
		rs := &sess.Room.Seats[seat]
		if !rs.Bot && !rs.Left && s.now().Sub(rs.LastSeen) < awayAfter {
			sess.mu.Unlock()
			return ErrSeatPresent
		}
		rs.Bot = true
	} else {
		sess.Room.Seats[seat].Bot = false
	}
	start, stop := reconcileBotsLocked(sess)
	// Every other seat-book write bumps the sequence and persists; this one used to
	// only set UpdatedAt, so a bot toggle was invisible to the sequence guard on the
	// next frame and did not survive a restart.
	sess.Seq++
	sess.UpdatedAt = s.now()
	persistErr := s.persistLocked(sess)
	sess.mu.Unlock()

	if start {
		go s.runBot(sess, stop)
	}
	return persistErr
}

// reconcileBotsLocked makes the one bot goroutine match the seat book. The caller
// holds sess.mu. It returns start=true exactly once per bot run, with the stop
// channel that run must watch.
func reconcileBotsLocked(sess *Session) (start bool, stop chan struct{}) {
	anyBots := false
	for _, rs := range sess.Room.Seats {
		if rs.Bot {
			anyBots = true
			break
		}
	}
	if !anyBots {
		if sess.botStop != nil {
			close(sess.botStop)
			sess.botStop = nil
		}
		sess.botRunning = false
		return false, nil
	}
	if sess.botStop == nil {
		sess.botStop = make(chan struct{})
	}
	if !sess.botRunning {
		sess.botRunning = true
		return true, sess.botStop
	}
	return false, sess.botStop
}

// botTickFloor is the shortest wait the bot loop accepts. A step that returns a tiny
// wait (a reveal about to clear) must not turn the loop into a spin.
const botTickFloor = 50 * time.Millisecond

// runBot plays every seat a bot holds, one action per tick. It asks the engine for the
// seat's own projection and nothing else: the bot cannot know another hand, the tavern
// order or the seed, because the projection does not contain them.
//
// The timer, not a ticker: botStep returns how long the table needs, so the next action
// starts when the reveal clears rather than up to a whole pace late (0.9.8).
func (s *Store) runBot(sess *Session, stop chan struct{}) {
	timer := time.NewTimer(s.botInterval())
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			wait := s.botStep(sess)
			if wait < botTickFloor {
				wait = botTickFloor
			}
			timer.Reset(wait)
		}
	}
}

// botInterval is the wait between bot actions. A zero field (a hand-built Store in a
// test) falls back to the default rather than spinning.
func (s *Store) botInterval() time.Duration {
	if s.botPace > 0 {
		return s.botPace
	}
	return defaultBotPace
}

// botTurnLocked reports whose move it is, whether the reveal still owns the screen, and
// whether that seat is a bot. Caller holds sess.mu. wait is the reveal time left, and it
// is returned even when a bot may not act, so the loop can sleep exactly that long.
func (s *Store) botTurnLocked(sess *Session) (actor int, wait time.Duration, ok bool) {
	if sess.Room == nil || sess.Room.Cancelled || sess.Room.Lobby ||
		sess.Game.Phase == engine.Won || sess.Game.Phase == engine.Lost {
		return -1, 0, false
	}
	if wait = s.revealWaitLocked(sess); wait > 0 {
		return -1, wait, false
	}
	actor = sess.Game.ActiveSeat
	if sess.Game.SeatChoice != nil {
		actor = sess.Game.SeatChoice.By
	}
	if actor < 0 || actor >= len(sess.Room.Seats) || !sess.Room.Seats[actor].Bot {
		return -1, 0, false
	}
	return actor, 0, true
}

// botStep plays one bot action and returns how long to wait before the next look. A
// bot never acts while a reveal is on the table: the check runs before the projection
// and again just before the write, because a human action can land in between.
func (s *Store) botStep(sess *Session) time.Duration {
	pace := s.botInterval()
	sess.mu.Lock()
	actor, wait, ok := s.botTurnLocked(sess)
	sess.mu.Unlock()
	if !ok {
		if wait > 0 {
			return wait
		}
		return pace
	}

	// The bot's whole world: this seat's projection, exactly as its browser would see it.
	p := s.ProjectFor(sess, actor)
	if !p.HasGame {
		return pace
	}
	obs, ok := bot.FromView(p.State)
	if !ok {
		return pace
	}
	act := bot.DefaultPolicy()(obs)

	// The gate read here is the one that decides. ApplyFor only refuses a wrong actor,
	// not a wrong moment, so this closes the window between the check and the write.
	sess.mu.Lock()
	_, wait, still := s.botTurnLocked(sess)
	sess.mu.Unlock()
	if !still {
		if wait > 0 {
			return wait
		}
		return pace
	}

	if _, _, _, err := s.ApplyFor(sess, actor, "", -1, act); err != nil {
		if s.logf != nil {
			s.logf("bot seat %d: %s refused: %v", actor, engine.ActionName(act), err)
		}
		return pace
	}
	s.Publish(sess, "bot")
	return pace
}

// goStartBot launches the bot loop outside a critical section. A nil stop channel
// means no loop is needed, which is the common case.
func goStartBot(s *Store, sess *Session, start bool, stop chan struct{}) {
	if start && stop != nil {
		go s.runBot(sess, stop)
	}
}

// botFillGrace is the configured grace, falling back to botFillAfter. A zero field
// means unset; only a test harness sets it, and only to a shorter value.
func (s *Store) botFillGrace() time.Duration {
	if s.botFill > 0 {
		return s.botFill
	}
	return botFillAfter
}

// sweepBotFill gives a bot the chair of every human seat that has been silent for
// the configured grace, when the room has the option on. It never touches the host
// seat: a bot cannot hold the menu. It returns whether any seat changed.
func (s *Store) sweepBotFill(sess *Session) bool {
	now := s.now()
	sess.mu.Lock()
	if sess.Room == nil || sess.Room.Cancelled || sess.Room.Lobby || !sess.Room.BotFill {
		sess.mu.Unlock()
		return false
	}
	changed := false
	grace := s.botFillGrace()
	for i := range sess.Room.Seats {
		rs := &sess.Room.Seats[i]
		if rs.Bot || rs.Left || rs.Seat == sess.Room.HostSeat {
			continue
		}
		if now.Sub(rs.LastSeen) > grace {
			rs.Bot = true
			changed = true
		}
	}
	start, stop := reconcileBotsLocked(sess)
	if changed {
		sess.Seq++
		sess.UpdatedAt = now
		s.enqueuePersistLocked(sess)
	}
	sess.mu.Unlock()
	goStartBot(s, sess, start, stop)
	return changed
}

// botFillAfter is the grace a silent seat gets before a bot takes its chair. The
// table stops for that long and no longer: a bad connection must not stall a game.
const botFillAfter = 60 * time.Second
