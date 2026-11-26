package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"time"

	"regicide/engine"
	"regicide/names"
)

// newSeatToken returns a 64-character hex token and its SHA-256 hash.
func newSeatToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// seatForLocked authenticates a token. The caller must hold sess.mu: TakeSeat and
// ReclaimSeat rewrite TokenHash in place, so an unlocked read can compare against a
// half-written hash (F-3).
func (sess *Session) seatForLocked(token string) (int, bool) {
	if sess.Room == nil || token == "" {
		return 0, false
	}
	want := hashToken(token)
	for _, seat := range sess.Room.Seats {
		if subtle.ConstantTimeCompare([]byte(seat.TokenHash), []byte(want)) == 1 {
			return seat.Seat, true
		}
	}
	return 0, false
}

// SeatFor authenticates a token against a room and returns the seat it belongs to.
func (sess *Session) SeatFor(token string) (int, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.seatForLocked(token)
}

// seatNameLocked returns the name for a seat. The caller must hold sess.mu.
func seatNameLocked(sess *Session, seat int) string {
	if sess.Room == nil {
		return ""
	}
	for _, rs := range sess.Room.Seats {
		if rs.Seat == seat {
			return rs.Name
		}
	}
	return ""
}

// SeatName returns the assigned name for a seat, or "" for an unoccupied one.
func (sess *Session) SeatName(seat int) string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return seatNameLocked(sess, seat)
}

// nextName hands out the next reserved name that is not already in use.
func (sess *Session) nextName() string {
	used := map[string]bool{}
	for _, s := range sess.Room.Seats {
		used[s.Name] = true
	}
	for _, name := range sess.Room.Reserved {
		if !used[name] {
			return name
		}
	}
	// The pool is far larger than MaxSeats, so this is unreachable in practice; a
	// seat is still never left anonymous.
	return names.Assign(sess.Room.Code+"-seat-"+strconv.Itoa(len(sess.Room.Seats)), 1)[0]
}

// JoinRoom adds a seat and returns its number and token.
func (s *Store) JoinRoom(sess *Session) (int, string, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return 0, "", ErrNoRoom
	}
	if sess.Room.Cancelled {
		return 0, "", ErrCancelled
	}
	// A finished table is not a table to join. The host may still read its end sheet and
	// replay by code, but a newcomer must not take a bot/left chair into a won game.
	if !sess.Room.Lobby && (sess.Game.Phase == engine.Won || sess.Game.Phase == engine.Lost) {
		return 0, "", ErrRoomStarted
	}
	now := s.now()

	// A dealt table still has one kind of free chair: a bot's, or a seat its player
	// left. Taking it is how the host's shared link lets a person replace a bot
	// mid-game without shrinking or resetting the table.
	if !sess.Room.Lobby {
		target := -1
		for i := range sess.Room.Seats {
			if sess.Room.Seats[i].Bot || sess.Room.Seats[i].Left {
				target = i
				break
			}
		}
		if target < 0 {
			return 0, "", ErrRoomStarted
		}
		token, hash, err := newSeatToken()
		if err != nil {
			return 0, "", err
		}
		rs := &sess.Room.Seats[target]
		rs.Bot = false
		rs.Left = false
		rs.TokenHash = hash
		rs.Ready = true
		rs.LastSeen = now
		rs.RejoinKeyHash = ""
		rs.RejoinExpiry = time.Time{}
		start, stop := reconcileBotsLocked(sess)
		sess.Seq++
		sess.UpdatedAt = now
		if err := s.persistLocked(sess); err != nil {
			return 0, "", err
		}
		goStartBot(s, sess, start, stop)
		return rs.Seat, token, nil
	}

	if len(sess.Room.Seats) >= roomCapacity {
		return 0, "", ErrRoomFull
	}
	seat := len(sess.Room.Seats)
	token, hash, err := newSeatToken()
	if err != nil {
		return 0, "", err
	}
	sess.Room.Seats = append(sess.Room.Seats, RoomSeat{
		// Sitting down is consent: a seat is ready unless its player says otherwise.
		// "Ready" exists so someone can hold the table while they fetch a drink, not
		// as a hurdle four people have to clear before the first card.
		Seat: seat, Name: sess.nextName(), TokenHash: hash,
		Ready: true, JoinedAt: now, LastSeen: now,
	})
	sess.Seq++
	sess.UpdatedAt = now
	if err := s.persistLocked(sess); err != nil {
		return 0, "", err
	}
	return seat, token, nil
}

// seatIndexLocked returns the slice index of a seat number, or -1. Caller holds
// sess.mu. Seat numbers are contiguous in a lobby and fixed after the deal.
func seatIndexLocked(sess *Session, seat int) int {
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat == seat {
			return i
		}
	}
	return -1
}

// promoteHostLocked hands the crown to the lowest human seat that is still at the
// table. If nobody is left, the table is cancelled. Caller holds sess.mu.
func promoteHostLocked(sess *Session) {
	for _, rs := range sess.Room.Seats {
		if !rs.Left && !rs.Bot {
			sess.Room.HostSeat = rs.Seat
			return
		}
	}
	sess.Room.Cancelled = true
}

// removeSeatLocked takes a seat out of a lobby, or marks it left in a dealt game.
// A lobby removal renumbers the seats above it and returns the old-to-new map, so
// the caller can remap live streams. Caller holds sess.mu and persists afterwards.
func removeSeatLocked(sess *Session, target int, now time.Time) (remap map[int]int, err error) {
	i := seatIndexLocked(sess, target)
	if i < 0 {
		return nil, ErrSeatRequired
	}
	if !sess.Room.Lobby {
		rs := &sess.Room.Seats[i]
		rs.Left = true
		rs.Bot = false
		rs.TokenHash = ""
		rs.RejoinKeyHash = ""
		rs.RejoinExpiry = time.Time{}
		rs.LastSeen = now
		if target == sess.Room.HostSeat {
			promoteHostLocked(sess)
		}
		// A seat that chose to leave is exactly the "missing player" the table
		// option exists for: give it a bot at once rather than waiting 60 s.
		if sess.Room.BotFill && !sess.Room.Cancelled {
			rs.Bot = true
		}
		return nil, nil
	}
	// Lobby: physically remove the chair and close the gap. Seat numbers are what the
	// engine indexes, so a hole at deal time is not an option.
	sess.Room.Seats = append(sess.Room.Seats[:i], sess.Room.Seats[i+1:]...)
	remap = map[int]int{}
	for j := range sess.Room.Seats {
		old := sess.Room.Seats[j].Seat
		if old > target {
			sess.Room.Seats[j].Seat = old - 1
			remap[old] = old - 1
		}
	}
	if sess.Room.HostSeat == target {
		if len(sess.Room.Seats) == 0 {
			sess.Room.Cancelled = true
		} else {
			sess.Room.HostSeat = 0
		}
	} else if sess.Room.HostSeat > target {
		sess.Room.HostSeat--
	}
	if len(sess.Room.Seats) == 0 {
		sess.Room.Cancelled = true
	}
	return remap, nil
}

// LeaveSeat is a player choosing to leave. In a lobby the chair is freed; in a
// dealt game the seat stays (the rules never shrink) and is marked left.
func (s *Store) LeaveSeat(sess *Session, seat int) error {
	sess.mu.Lock()
	if sess.Room == nil {
		sess.mu.Unlock()
		return ErrNoRoom
	}
	if sess.Room.Cancelled {
		sess.mu.Unlock()
		return ErrCancelled
	}
	remap, err := removeSeatLocked(sess, seat, s.now())
	if err != nil {
		sess.mu.Unlock()
		return err
	}
	start, stop := reconcileBotsLocked(sess)
	sess.Seq++
	sess.UpdatedAt = s.now()
	err = s.persistLocked(sess)
	sess.mu.Unlock()
	goStartBot(s, sess, start, stop)
	s.remapStreams(sess, seat, remap)
	s.Publish(sess, "leave")
	return err
}

// KickSeat is the host removing somebody. It uses the same chair rules as Leave:
// a lobby chair disappears, a dealt seat is left to a bot or the host's menu.
func (s *Store) KickSeat(sess *Session, hostSeat, target int) error {
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
	if target == hostSeat {
		sess.mu.Unlock()
		return ErrNotReady
	}
	remap, err := removeSeatLocked(sess, target, s.now())
	if err != nil {
		sess.mu.Unlock()
		return err
	}
	start, stop := reconcileBotsLocked(sess)
	sess.Seq++
	sess.UpdatedAt = s.now()
	err = s.persistLocked(sess)
	sess.mu.Unlock()
	goStartBot(s, sess, start, stop)
	s.remapStreams(sess, target, remap)
	s.Publish(sess, "kick")
	return err
}

// SetReady marks a seat ready (or not) in the lobby.
func (s *Store) SetReady(sess *Session, seat int, ready bool) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return ErrNoRoom
	}
	if !sess.Room.Lobby {
		return ErrRoomStarted
	}
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat == seat {
			sess.Room.Seats[i].Ready = ready
			sess.Room.Seats[i].LastSeen = s.now()
			sess.Seq++
			sess.UpdatedAt = s.now()
			return s.persistLocked(sess)
		}
	}
	return ErrSeatRequired
}

// electHostLocked moves host authority to the lowest-numbered seat that is actually
// present, when the current host has been silent for takeAfter. D-60: a table must never
// be stranded by the one seat that holds every administrative action.
//
// It is called with sess.mu held, from the paths that already touch presence, so there is
// no window in which the room has no host. It reports whether authority moved.
func (s *Store) electHostLocked(sess *Session, now time.Time) bool {
	if sess.Room == nil || sess.Room.Lobby && len(sess.Room.Seats) < 2 {
		return false
	}
	host := sess.Room.HostSeat
	if host < 0 || host >= len(sess.Room.Seats) {
		return false
	}
	if now.Sub(sess.Room.Seats[host].LastSeen) <= takeAfter {
		return false // the host is still here, or barely gone
	}
	for _, rs := range sess.Room.Seats {
		if rs.Seat == host || rs.Bot || rs.Left {
			// A bot cannot hold the menu, and a seat that chose to leave has no device
			// to hold it either: its token is dead, so electing it strands the table
			// until the next election (P2-authority Q5).
			continue
		}
		if now.Sub(rs.LastSeen) > awayAfter {
			continue // away seats cannot inherit: the point is a present owner
		}
		sess.Room.HostSeat = rs.Seat
		sess.Seq++
		sess.UpdatedAt = now
		if sess.Game.Version != 0 {
			sess.Game.NoteEvent(engine.Event{
				Type: engine.EvHostChange, Seat: host, Amount: rs.Seat, Note: "host moved",
			})
		}
		s.enqueuePersistLocked(sess)
		return true
	}
	return false
}

// HandOverHost gives the crown to a named seat. The host may do this at any time; the
// target must be a present human, so the crown can never land on an empty chair or a bot.
func (s *Store) HandOverHost(sess *Session, hostSeat, target int) error {
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
	if target == hostSeat {
		return ErrNotReady
	}
	if target < 0 || target >= len(sess.Room.Seats) {
		return ErrNoRoom
	}
	rs := sess.Room.Seats[target]
	if rs.Bot || rs.Left {
		// The crown must land on somebody who can actually use it: a bot has no menu,
		// and a left seat's token is gone (P2-authority Q5).
		return ErrNotReady
	}
	now := s.now()
	if now.Sub(rs.LastSeen) > awayAfter {
		return ErrNotAway
	}
	previous := sess.Room.HostSeat
	sess.Room.HostSeat = target
	sess.Seq++
	sess.UpdatedAt = now
	if sess.Game.Version != 0 {
		sess.Game.NoteEvent(engine.Event{
			Type: engine.EvHostChange, Seat: previous, Amount: target, Note: "host handed over",
		})
	}
	s.enqueuePersistLocked(sess)
	return nil
}

// IdleFor reports how long a seat has been silent, and whether it is away or can
// be taken over.
func (s *Store) IdleFor(sess *Session, seat int) (time.Duration, bool, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return 0, false, false
	}
	for _, rs := range sess.Room.Seats {
		if rs.Seat != seat {
			continue
		}
		idle := s.now().Sub(rs.LastSeen)
		return idle, idle > awayAfter, idle > takeAfter
	}
	return 0, false, false
}

// TakeSeat hands an away seat to a new device and returns a fresh token. The old
// token stops working, which is what makes this safe: two devices can never both
// believe they own the seat.
func (s *Store) TakeSeat(sess *Session, hostSeat, target int) (string, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", ErrNoRoom
	}
	if sess.Room.Cancelled {
		// Every other lifecycle route refuses a closed table; this one used to mint a
		// fresh token for a room nobody can play (P2-authority Q5).
		return "", ErrCancelled
	}
	if hostSeat != sess.Room.HostSeat {
		return "", ErrNotHost
	}
	if target == hostSeat {
		return "", ErrNotReady
	}
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat != target {
			continue
		}
		rs := &sess.Room.Seats[i]
		// A left seat, or one a bot already holds, is free at once. A merely silent
		// seat keeps the five-minute reconnect window.
		if !rs.Left && !rs.Bot && s.now().Sub(rs.LastSeen) < takeAfter {
			return "", ErrNotAway
		}
		token, hash, err := newSeatToken()
		if err != nil {
			return "", err
		}
		rs.TokenHash = hash
		rs.Left = false
		rs.Bot = false
		// One owner at a time: a live reconnect link dies with the takeover.
		rs.RejoinKeyHash = ""
		rs.RejoinExpiry = time.Time{}
		rs.LastSeen = s.now()
		start, stop := reconcileBotsLocked(sess)
		goStartBot(s, sess, start, stop)
		sess.Seq++
		sess.UpdatedAt = s.now()
		return token, s.persistLocked(sess)
	}
	return "", ErrSeatRequired
}

// SeatInvite mints a one-time reconnect link for an away seat. The host is the only
// seat that may do this, and the link is the only way back for a player who lost
// their browser: it carries a key, not the seat token (D-42).
func (s *Store) SeatInvite(sess *Session, hostSeat, target int) (string, time.Time, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", time.Time{}, ErrNoRoom
	}
	if sess.Room.Cancelled {
		return "", time.Time{}, ErrCancelled
	}
	if hostSeat != sess.Room.HostSeat {
		return "", time.Time{}, ErrNotHost
	}
	if target == hostSeat {
		return "", time.Time{}, ErrNotReady
	}
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat != target {
			continue
		}
		key, hash, err := newSeatToken()
		if err != nil {
			return "", time.Time{}, err
		}
		expiry := s.now().Add(rejoinTTL)
		sess.Room.Seats[i].RejoinKeyHash = hash
		sess.Room.Seats[i].RejoinExpiry = expiry
		sess.Seq++
		sess.UpdatedAt = s.now()
		if err := s.persistLocked(sess); err != nil {
			return "", time.Time{}, err
		}
		return key, expiry, nil
	}
	return "", time.Time{}, ErrSeatRequired
}

// ReclaimSeat trades a live reconnect key for a fresh seat token. The old token
// dies, so the returning player and the device they left cannot both act.
func (s *Store) ReclaimSeat(sess *Session, target int, key string) (string, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", ErrNoRoom
	}
	if sess.Room.Cancelled {
		return "", ErrCancelled
	}
	if len(key) < 16 {
		return "", ErrBadKey
	}
	want := hashToken(key)
	for i := range sess.Room.Seats {
		seat := &sess.Room.Seats[i]
		if seat.Seat != target {
			continue
		}
		if seat.RejoinKeyHash == "" || s.now().After(seat.RejoinExpiry) {
			return "", ErrBadKey
		}
		if subtle.ConstantTimeCompare([]byte(seat.RejoinKeyHash), []byte(want)) != 1 {
			return "", ErrBadKey
		}
		token, hash, err := newSeatToken()
		if err != nil {
			return "", err
		}
		seat.TokenHash = hash
		seat.RejoinKeyHash = ""
		seat.RejoinExpiry = time.Time{}
		seat.LastSeen = s.now()
		sess.Seq++
		sess.UpdatedAt = s.now()
		return token, s.persistLocked(sess)
	}
	return "", ErrSeatRequired
}
