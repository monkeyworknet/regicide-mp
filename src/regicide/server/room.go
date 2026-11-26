package server

import (
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"time"

	"regicide/engine"
	"regicide/names"
)

// Co-op rules of engagement (docs/MP-PLAN.md, D-32):
//
//   - a co-op room plays with one of two card sets, because four hands and a seat
//     rail need the screen more than the art does;
//   - a player never types a name (D-38): the server assigns a royal one;
//   - a room is a Session, so persistence, GC and the single-writer discipline are
//     the ones that already exist.

// coopThemeOrder is the only card sets a co-op room may use, in the order the lobby
// shows them. The membership map is derived from it, so the two cannot drift.
var coopThemeOrder = []string{"official", "simple", "8bit", "pokemon"}

var coopThemes = func() map[string]bool {
	m := make(map[string]bool, len(coopThemeOrder))
	for _, id := range coopThemeOrder {
		m[id] = true
	}
	return m
}()

// CoopTheme reports whether a theme may be used for a multi-seat game.
func CoopTheme(theme string) bool { return coopThemes[theme] }

// pendingGame remembers how a lobby was configured so Start can build the game.
type pendingGame struct {
	Theme     string             `json:"theme"`
	Rules     engine.RuleOptions `json:"rules"`
	Seed      string             `json:"seed,omitempty"`
	OpenHands bool               `json:"openHands,omitempty"`
	// Jokers is the pair the host's browser asked for, resolved at the deal
	// against whatever theme the table finally plays. JokersCustom marks a hand
	// pick, which outranks the set's generated pair.
	Jokers       []string `json:"jokers,omitempty"`
	JokersCustom bool     `json:"jokersCustom,omitempty"`
}

// RoomSeat is one occupied seat. The raw token is never stored: only its hash.
type RoomSeat struct {
	Seat      int       `json:"seat"`
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Ready     bool      `json:"ready"`
	JoinedAt  time.Time `json:"joinedAt"`
	LastSeen  time.Time `json:"lastSeen"`
	// Bot marks a seat a bot is holding because its player went away (T7.5). It is set
	// by the host, or automatically when the table opted into bots filling missing
	// seats, and it is visible to everyone: a table must never wonder whether it is
	// playing a person.
	Bot bool `json:"bot,omitempty"`
	// Left marks a seat whose player left a dealt game on purpose. The seat stays in
	// the table (the rules never shrink) but has no owner until the host gives it to
	// a bot or a new device.
	Left bool `json:"left,omitempty"`

	// RejoinKeyHash is set while the host is holding a reconnect link open for this
	// seat (D-42). The key itself lives only in the host's browser: the server keeps
	// a hash and a deadline, so a stolen session file cannot be used to take a seat.
	RejoinKeyHash string    `json:"rejoinKeyHash,omitempty"`
	RejoinExpiry  time.Time `json:"rejoinExpiry,omitempty"`
}

// roomCapacity is the size of the waiting room, not a promise to the engine: a
// table starts with however many people actually sat down (2 to 4), and the rules
// for that number are derived when the game is dealt (D-40).
const roomCapacity = 4

// roomIdleTTL is how long a table lives without a write. The front page drops it at
// the hour, and the GC deletes the session (so a rejoin code stops working) within a
// minute after that. Finished tables leave the listing at once; they are kept only
// until this TTL so the end sheet and the replay can still be read.
const roomIdleTTL = time.Hour

// roomCapacity is also the pool of names reserved up front, so a full table never
// repeats a name.

// RoomInfo is the lobby and seat book for a co-op game.
type RoomInfo struct {
	// Cancelled ends the table for everyone: the host closed it, and the other
	// seats must be told why instead of hitting a 404 when the file is gone.
	Cancelled bool   `json:"cancelled,omitempty"`
	Code      string `json:"code"`
	HostSeat  int    `json:"hostSeat"`
	MaxSeats  int    `json:"maxSeats"`
	Lobby     bool   `json:"lobby"`
	// AllowWatch is the host's switch for spectators. Off means the watch endpoints
	// answer 403 and any open watcher is dropped on its next request.
	AllowWatch bool `json:"allowWatch"`
	// WatchHands lets a spectator look at a player's hand, one player at a time, by
	// viewing the board as if they were that player (D-45). Off by default: an
	// audience that can read every hand changes what the table is playing.
	WatchHands bool `json:"watchHands"`
	// BotFill gives a silent seat a bot after 60 s, and gives a left or kicked seat
	// a bot immediately. A bot seat is always replaceable by a human who joins with
	// the room code. Off means the table waits for its owner (D-41).
	BotFill bool       `json:"botFill"`
	Seats   []RoomSeat `json:"seats"`
	// Reserved holds the names this room will hand out, one per seat in join
	// order. Assigning them up front keeps a four-seat table free of repeats.
	Reserved []string `json:"reserved,omitempty"`
}

// Room-level refusals a client has to understand.
var (
	ErrRoomFull     = errors.New("room_full")
	ErrRoomStarted  = errors.New("game_started")
	ErrNotHost      = errors.New("not_host")
	ErrNotReady     = errors.New("not_ready")
	ErrSeatRequired = errors.New("seat_required")
	ErrNotYourTurn  = errors.New("not_your_turn")
	ErrBadTheme     = errors.New("bad_theme")
	ErrNotAway      = errors.New("seat_not_away")
	ErrBadKey       = errors.New("bad_rejoin_key")
	ErrCancelled    = errors.New("game_cancelled")
	ErrWatchOff     = errors.New("watching_disabled")
	ErrWatchFull    = errors.New("watchers_full")
	ErrNoRoom       = errors.New("room_not_found")
)

// newRoomCode returns a 5-character code from the Crockford alphabet: no I, L, O
// or U, because those are the characters a spoken code gets wrong.
// roomCodeFn is the code generator. Tests replace it to force a collision, which is
// the only way to exercise F-26 on purpose: 32^5 codes never collide by accident.
var roomCodeFn = newRoomCode

// testInCodeWindow, when set, runs between choosing the code and registering the room.
// The test asserts the store lock is still held there, which is the whole of F-26: the
// choice and the registration must be one critical section.
var testInCodeWindow func()

func newRoomCode() (string, error) {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// CreateRoom starts a lobby and returns its host seat and token. The host is seat
// 0 and holds no cards until the game starts.
func (s *Store) CreateRoom(theme string, rules engine.RuleOptions, openHands bool, seed string) (*Session, int, string, error) {
	return s.CreateRoomWith(theme, rules, openHands, seed, JokerPick{})
}

// CreateRoomWith is CreateRoom plus the host browser's Jester request.
func (s *Store) CreateRoomWith(theme string, rules engine.RuleOptions, openHands bool, seed string, pick JokerPick) (*Session, int, string, error) {
	if !CoopTheme(theme) {
		return nil, 0, "", ErrBadTheme
	}
	code, err := roomCodeFn()
	if err != nil {
		return nil, 0, "", err
	}
	// The code is chosen and the room registered under one hold of the store lock.
	// Choosing it here and registering it 40 lines later let two creators pick the
	// same free code: the second insert overwrote roomIndex, orphaning the first room
	// by code (F-26).
	s.mu.Lock()
	for s.roomIndex[code] != "" {
		if code, err = roomCodeFn(); err != nil {
			s.mu.Unlock()
			return nil, 0, "", err
		}
	}

	sess := &Session{
		ID:        newID(),
		CreatedAt: s.now(),
		UpdatedAt: s.now(),
		cache:     map[string]cachedAction{},
		watchers:  map[string]*Watcher{},
		Room: &RoomInfo{
			Code:       code,
			HostSeat:   0,
			MaxSeats:   roomCapacity,
			Lobby:      true,
			AllowWatch: true,
			// A table that fills a silent seat with a bot keeps a group game from
			// stalling on one bad connection. The host may switch it off in the lobby.
			BotFill:  true,
			Reserved: names.Assign(code, roomCapacity),
		},
		Pending: &pendingGame{Theme: theme, Rules: rules, Seed: strings.TrimSpace(seed), OpenHands: openHands, Jokers: pick.Pair, JokersCustom: pick.Custom},
	}
	token, hash, err := newSeatToken()
	if err != nil {
		// The store lock was taken for the code reservation above; returning with it
		// held would wedge every later room operation.
		s.mu.Unlock()
		return nil, 0, "", err
	}
	sess.Room.Seats = []RoomSeat{{
		Seat: 0, Name: sess.Room.Reserved[0], TokenHash: hash,
		Ready: true, JoinedAt: s.now(), LastSeen: s.now(),
	}}

	if testInCodeWindow != nil {
		testInCodeWindow()
	}
	// Still holding s.mu from the code reservation above.
	s.sess[sess.ID] = sess
	s.roomIndex[code] = sess.ID
	s.mu.Unlock()
	if err := s.persist(sess); err != nil {
		return nil, 0, "", err
	}
	return sess, 0, token, nil
}

// remapStreams closes a removed seat's stream and renumbers everybody above it.
// It runs outside sess.mu, which is why it takes copies of the facts it needs.
func (s *Store) remapStreams(sess *Session, removed int, remap map[int]int) {
	sess.mu.Lock()
	h := sess.hub
	sess.mu.Unlock()
	if h == nil {
		return
	}
	h.dropSeat(removed, "seat_removed")
	if len(remap) > 0 {
		h.renumber(remap)
	}
}

// SetRoomRules changes the rule switches a lobby will deal with. Host only, and
// only before the deal: rules are frozen per run.
func (s *Store) SetRoomRules(sess *Session, hostSeat int, rules engine.RuleOptions) error {
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
	if sess.Pending == nil {
		return ErrNotReady
	}
	// The solo Jester is a single-player power; a room ignores it.
	rules.SoloJester = false
	sess.Pending.Rules = rules
	sess.Seq++
	sess.UpdatedAt = s.now()
	return s.persistLocked(sess)
}

// SetRoomTheme changes the card set the next deal uses. Host only, and only before
// the deal: the deck is frozen per run, like the rules (decision 8).
func (s *Store) SetRoomTheme(sess *Session, hostSeat int, theme string) error {
	return s.SetRoomThemeWith(sess, hostSeat, theme, JokerPick{})
}

// SetRoomThemeWith is SetRoomTheme plus the host browser's Jester request. A nil
// pick (a caller that does not care) keeps the request on file; a list replaces it,
// and an empty list clears it. The deal resolves whatever is on file against the new
// theme, so a generated pair follows the deck and a hand pick stays put.
func (s *Store) SetRoomThemeWith(sess *Session, hostSeat int, theme string, pick JokerPick) error {
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
	if sess.Pending == nil {
		return ErrNotReady
	}
	theme = strings.ToLower(strings.TrimSpace(theme))
	if !CoopTheme(theme) {
		return ErrBadTheme
	}
	sess.Pending.Theme = theme
	if pick.Pair != nil {
		sess.Pending.Jokers = pick.Pair
		sess.Pending.JokersCustom = pick.Custom
	}
	sess.Seq++
	sess.UpdatedAt = s.now()
	return s.persistLocked(sess)
}

// maxWatchers bounds the presence budget: each spectator holds a stream and posts
// cursor frames, and eight is far more than a LAN table needs.
const maxWatchers = 8

// Watcher is one spectator. The id is ephemeral and lives only in memory: a
// spectator has no seat, no hand and nothing to persist.
type Watcher struct {
	ID       string
	JoinedAt time.Time
	LastSeen time.Time
	// Cursor is the last normalised pointer position this spectator sent, so the
	// relay can include it without another round trip.
	Cursor Cursor
	// LastCursor is when a frame was last accepted; the floor is measured from it.
	LastCursor time.Time
}

// Cursor is one pointer frame: a position the whole table shares.
//
// X and Y are fractions of the BOARD, so a dot follows the pointer over the royal, the
// tavern and the discard as well as the felt (playtest 3, note 5). Mark still names the
// felt mark the pointer is over (-1 for none) with MX, MY inside it: that pair is what
// makes "over the smiley" the same place on a phone and on a desktop (D-51, D-58), and
// a receiver resolves the mark against its own layout before falling back to X/Y.
type Cursor struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Mark   int     `json:"mark"`
	MX     float64 `json:"mx"`
	MY     float64 `json:"my"`
	Hidden bool    `json:"hidden,omitempty"`
	At     int64   `json:"at,omitempty"`
}

// Normalised returns the cursor with every fraction clamped to the board and the mark
// range checked. A stray value must never widen the board.
func (c Cursor) Normalised() Cursor {
	c.X = clamp01(c.X)
	c.Y = clamp01(c.Y)
	c.MX = clamp01(c.MX)
	c.MY = clamp01(c.MY)
	if c.Mark < -1 || c.Mark >= feltMarks {
		c.Mark = -1
	}
	return c
}

// feltMarks is how many landmarks the table has (D-51). A pointer frame naming a mark
// outside this range is treated as naming none.
const feltMarks = 4

// AddWatcher registers a spectator and returns its ephemeral id.
func (s *Store) AddWatcher(sess *Session) (string, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", ErrNoRoom
	}
	if !sess.Room.Lobby && (sess.Game.Phase == engine.Won || sess.Game.Phase == engine.Lost) {
		return "", ErrRoomStarted
	}
	if !sess.Room.AllowWatch {
		return "", ErrWatchOff
	}
	if len(sess.watchers) >= maxWatchers {
		s.pruneWatchersLocked(sess, s.now())
		if len(sess.watchers) >= maxWatchers {
			return "", ErrWatchFull
		}
	}
	if sess.watchers == nil {
		sess.watchers = map[string]*Watcher{}
	}
	id := newID()
	sess.watchers[id] = &Watcher{ID: id, JoinedAt: s.now(), LastSeen: s.now()}
	return id, nil
}

// WatcherAuth reports whether a watch id is live (and drops it if watching was
// switched off after it joined).
func (s *Store) WatcherAuth(sess *Session, id string) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return s.watcherAuthLocked(sess, id)
}

// defaultBotPace is deliberately slower than a person: the table should be watchable,
// and a bot that answers instantly looks like a bug. It must also outlast the table
// reveal, or the bot's next action arrives while the last one is still flying and the
// defence is never seen. The reveal holds 1.4 s and flies 0.42 s; the extra 0.5 s is the
// pause the playtest asked for after the fade, so one bot move is one readable beat.
const defaultBotPace = 2400 * time.Millisecond

// DropWatcherIfAuth removes a watcher only if its id authenticates, in one critical
// section: two calls let a slot be re-added between the check and the drop.
func (s *Store) DropWatcherIfAuth(sess *Session, id string) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !s.watcherAuthLocked(sess, id) {
		return false
	}
	delete(sess.watchers, id)
	if sess.hub != nil {
		sess.hub.dropWatcher(id)
	}
	return true
}

// watcherTTL is how long a silent spectator keeps its slot. The client sends a ping
// with every read, so a live tab never gets near it.
const watcherTTL = 60 * time.Second

// watcherAuthLocked reports whether a watch id is live. Caller holds sess.mu. It is a
// method so it can read the store's clock, which tests inject.
func (s *Store) watcherAuthLocked(sess *Session, id string) bool {
	if sess.Room == nil || !sess.Room.AllowWatch || id == "" {
		return false
	}
	w, ok := sess.watchers[id]
	if !ok {
		return false
	}
	// An expired slot is nobody's: it frees a seat at the window for someone else.
	if s.now().Sub(w.LastSeen) > watcherTTL {
		delete(sess.watchers, id)
		return false
	}
	return true
}

// pruneWatchersLocked drops spectators that went quiet. Caller holds sess.mu.
func (s *Store) pruneWatchersLocked(sess *Session, now time.Time) {
	for id, w := range sess.watchers {
		if now.Sub(w.LastSeen) > watcherTTL {
			if sess.hub != nil {
				// The hub keeps the last pointer per watch id; a pruned watcher's entry
				// used to live for ever, drawing a ghost dot on every table (X3).
				sess.hub.dropWatcher(id)
			}
			delete(sess.watchers, id)
		}
	}
}

// touchWatcherLocked stamps a spectator as alive. Caller holds sess.mu.
func (s *Store) touchWatcherLocked(sess *Session, id string, now time.Time) {
	if w, ok := sess.watchers[id]; ok {
		w.LastSeen = now
	}
}

// TouchWatcher stamps a spectator as alive.
func (s *Store) TouchWatcher(sess *Session, id string) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	s.touchWatcherLocked(sess, id, s.now())
}

// cursorFloor is the shortest interval between two accepted pointer frames from one
// client. Twelve frames a second is faster than a pointer can be followed and far
// slower than a loop can post (F-5).
// cursorFloor bounds how often one seat may move its dot: 25 frames a second is well
// above a hand's speed and far below the client's own 30 Hz ceiling, so the floor is a
// guard rather than the thing that sets the feel.
const cursorFloor = 40 * time.Millisecond

// SetWatcherCursor stores the last pointer frame from a spectator. It returns
// ok=false when the watcher may not draw at all, and applied=false when the frame is
// inside the floor: a flood is dropped, not answered with an error, so a fast client
// costs one map lookup instead of a response.
func (s *Store) SetWatcherCursor(sess *Session, id string, c Cursor) (applied, ok bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	w, found := sess.watchers[id]
	if !found || sess.Room == nil || !sess.Room.AllowWatch {
		return false, false
	}
	now := s.now()
	if !w.LastCursor.IsZero() && now.Sub(w.LastCursor) < cursorFloor {
		return false, true
	}
	w.Cursor = c
	w.Cursor.At = now.UnixMilli()
	w.LastSeen = now
	w.LastCursor = now
	return true, true
}

// DropWatcher forgets a spectator that left.
func (s *Store) DropWatcher(sess *Session, id string) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	delete(sess.watchers, id)
}

// WatcherCount is how many people are watching.
func (s *Store) WatcherCount(sess *Session) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	s.pruneWatchersLocked(sess, s.now())
	return len(sess.watchers)
}

// SetWatchSettings is the host's spectator switch: on or off, and whether a
// spectator may look at hands.
func (s *Store) SetWatchSettings(sess *Session, hostSeat int, allowed, hands bool) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return ErrNoRoom
	}
	if hostSeat != sess.Room.HostSeat {
		return ErrNotHost
	}
	sess.Room.AllowWatch = allowed
	// Hands cannot be open in a room nobody may watch.
	sess.Room.WatchHands = allowed && hands
	if !allowed {
		sess.watchers = map[string]*Watcher{}
	}
	sess.Seq++
	sess.UpdatedAt = s.now()
	return s.persistLocked(sess)
}

// Projection is one viewer's whole view of a table, taken under a single lock.
//
// It exists because the parts used to be fetched separately — ViewFor here, viewRoom
// there — and every gap between them was a frame in which a seat could be taken over,
// a watcher switched off, or a room cancelled. Handlers must not read sess.Room or
// sess.Game themselves (F-3, F-11, F-13).
type Projection struct {
	Seq        int
	State      engine.ClientState
	HasGame    bool
	Lobby      bool
	Room       *publicRoom
	Seat       int
	Spectator  bool
	WatchingAs int
}

// projectLocked builds the projection. The caller holds sess.mu, and the returned
// values are copies: nothing in them points back into the session.
func (s *Store) projectLocked(sess *Session, seat int, spectator bool, as int, now time.Time) Projection {
	p := Projection{Seat: seat, Spectator: spectator, WatchingAs: -1}
	if sess.Room != nil {
		p.Lobby = sess.Room.Lobby
	}
	p.Room = viewRoomLocked(sess, seat, now)
	// The sequence belongs to the room, not to the game: a lobby has one too, and a
	// client that is about to deal needs it (F-33).
	p.Seq = sess.Seq
	if p.Lobby {
		// A lobby has no game to project; the room view is the whole answer.
		return p
	}
	// F-17: a cancelled table has no board for a spectator. A seat keeps its last board
	// (the table can still be read, and the reason is on the room view); a spectator
	// used to keep receiving a live-looking one, which read as "the game continues".
	if spectator && sess.Room != nil && sess.Room.Cancelled {
		return p
	}
	if spectator {
		// The spectator projection is the public table with no hand at all — not
		// even in an open-hands room — unless the host has allowed spectator hands
		// and a seat was named. Then it is that player's board, with no legal
		// actions, because a spectator cannot act (D-45).
		allowed := sess.Room != nil && sess.Room.WatchHands
		players := len(sess.Game.Players)
		if allowed && as >= 0 && as < players {
			p.State = engine.SnapshotFor(sess.Game, engine.View{Seat: as, OpenHands: false})
			// Empty slice, not nil: the wire contract uses [] so the client never
			// has to null-guard.
			p.State.Legal = []engine.ActionView{}
			p.WatchingAs = as
		} else {
			p.State = engine.SnapshotFor(sess.Game, engine.View{Seat: -1, OpenHands: false})
		}
	} else {
		openHands := sess.Pending != nil && sess.Pending.OpenHands
		p.State = engine.SnapshotFor(sess.Game, engine.View{Seat: seat, OpenHands: openHands})
	}
	p.HasGame = true
	// A co-op table never publishes its seed while it is being played: in a room a
	// known seed fixes the shuffle and therefore every hand, so it is a full
	// hidden-information break (F-2). Solo keeps its practice seed; the table gets
	// it back when the game ends, which is when it stops being a secret.
	if len(p.State.Players) > 1 && sess.Game.Phase != engine.Won && sess.Game.Phase != engine.Lost {
		p.State.Seed = ""
		p.State.SeedVisible = false
	}
	// Names are resolved here, inside the lock: a rename is a seat-book write.
	for i := range p.State.Players {
		p.State.Players[i].Name = seatNameLocked(sess, p.State.Players[i].Seat)
	}
	return p
}

// lobbyFacts answers the one question the DELETE path asks — the room's code and
// whether it has been dealt to — in one lock. It used to read the code first and the
// lobby flag second, which is two lock round trips for one fact.
func (s *Store) lobbyFacts(sess *Session) (code string, lobby bool, started bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", false, false
	}
	return sess.Room.Code, sess.Room.Lobby, !sess.Room.Lobby
}

// roomFacts reads the room's code and host seat under the session lock.
func (s *Store) roomFacts(sess *Session) (code string, hostSeat int, ok bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return "", 0, false
	}
	return sess.Room.Code, sess.Room.HostSeat, true
}

// ProjectFor takes and releases the session lock itself and returns a projection that
// shares nothing mutable with the session: every slice in the state is rebuilt. The
// room view's Themes field is the shared coopThemeOrder slice, which is read-only, so
// it is not copied per frame.
// This is the only projection a seat route may use.
func (s *Store) ProjectFor(sess *Session, seat int) Projection {
	now := s.now()
	sess.mu.Lock()
	p := s.projectLocked(sess, seat, false, -1, now)
	sess.mu.Unlock()
	return p
}

// ProjectForWatcher authenticates the watch id and projects in the same critical
// section, so a watch id cannot be revoked between the check and the read.
func (s *Store) ProjectForWatcher(sess *Session, id string, as int) (Projection, bool) {
	now := s.now()
	sess.mu.Lock()
	ok := s.watcherAuthLocked(sess, id)
	var p Projection
	if ok {
		s.touchWatcherLocked(sess, id, now)
		p = s.projectLocked(sess, -1, true, as, now)
	}
	sess.mu.Unlock()
	return p, ok
}

// ViewFor is the seat projection in the older tuple shape, kept so callers that only
// need the state do not have to know about Projection.
func (s *Store) ViewFor(sess *Session, seat int) (int, engine.ClientState, bool) {
	p := s.ProjectFor(sess, seat)
	return p.Seq, p.State, p.HasGame
}

// ViewForWatcher is the spectator projection in the older tuple shape. It does not
// authenticate a watch id; use ProjectForWatcher when there is one.
func (s *Store) ViewForWatcher(sess *Session, as int) (int, engine.ClientState, bool, int) {
	now := s.now()
	sess.mu.Lock()
	p := s.projectLocked(sess, -1, true, as, now)
	sess.mu.Unlock()
	return p.Seq, p.State, p.HasGame, p.WatchingAs
}

// Seat policies for an absent player (D-41). The table waits; it never plays for
// someone, and it never shrinks itself by a seat.
const (
	// awayAfter is when a seat is shown as away: a few missed heartbeats.
	awayAfter = 90 * time.Second
	// takeAfter is when the host may hand that seat to a new device.
	takeAfter = 5 * time.Minute
	// rejoinTTL is how long a reconnect link the host shared stays usable.
	rejoinTTL = 10 * time.Minute
)

// touchAt stamps a seat's last-seen time with the caller's clock. Caller holds sess.mu.
// ApplyFor repeats this loop for the acting seat, so it lives here once.
func (s *Store) touchAt(sess *Session, seat int, now time.Time) {
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat == seat {
			sess.Room.Seats[i].LastSeen = now
		}
	}
}

// touch stamps a seat as alive now; the caller holds the session lock.
func (s *Store) touch(sess *Session, seat int) {
	s.touchAt(sess, seat, s.now())
}

// TouchSeat records that a seat's client is alive. A poll or a stream heartbeat is
// the signal, not tab visibility: a player watching the board is present even if
// the browser window is in the background.
func (s *Store) TouchSeat(sess *Session, seat int) {
	sess.mu.Lock()
	if sess.Room == nil {
		sess.mu.Unlock()
		return
	}
	s.touch(sess, seat)
	now := s.now()
	sess.UpdatedAt = now
	// A human touch on a bot seat is the owner coming back: stop playing for them.
	reclaimed := false
	if i := seatIndexLocked(sess, seat); i >= 0 && sess.Room.Seats[i].Bot {
		sess.Room.Seats[i].Bot = false
		sess.Room.Seats[i].Left = false
		reclaimed = true
		sess.Seq++
		s.enqueuePersistLocked(sess)
	}
	start, stop := reconcileBotsLocked(sess)
	// Someone is here, and this is the cheapest place to notice that the host is not.
	s.electHostLocked(sess, now)
	sess.mu.Unlock()
	goStartBot(s, sess, start, stop)
	if reclaimed {
		s.Publish(sess, "bot")
	}
	if s.sweepBotFill(sess) {
		s.Publish(sess, "botfill")
	}
}

// Cancel ends the table for everyone. The room file stays until the GC so the other
// seats can read the reason instead of seeing the room vanish mid-turn.
func (s *Store) Cancel(sess *Session, hostSeat int) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return ErrNoRoom
	}
	if hostSeat != sess.Room.HostSeat {
		return ErrNotHost
	}
	sess.Room.Cancelled = true
	sess.Room.Lobby = false
	sess.Seq++
	sess.UpdatedAt = s.now()
	return s.persistLocked(sess)
}

// Start deals the game. Every seat must be ready and at least two seats must be
// taken; after that the lobby is closed and the seats are fixed.
func (s *Store) Start(sess *Session, seat int, expectedSeq int) error {
	sess.mu.Lock()
	if expectedSeq >= 0 && expectedSeq != sess.Seq {
		sess.mu.Unlock()
		return ErrStaleSeq
	}
	if sess.Room == nil {
		sess.mu.Unlock()
		return ErrNoRoom
	}
	if !sess.Room.Lobby {
		sess.mu.Unlock()
		return ErrRoomStarted
	}
	if seat != sess.Room.HostSeat {
		sess.mu.Unlock()
		return ErrNotHost
	}
	now := s.now()
	if len(sess.Room.Seats) < 2 {
		sess.mu.Unlock()
		return ErrNotReady
	}
	if len(sess.Room.Seats) > roomCapacity {
		sess.mu.Unlock()
		return ErrNotReady // unreachable: joins are capped
	}
	for _, rs := range sess.Room.Seats {
		if !rs.Ready {
			sess.mu.Unlock()
			return ErrNotReady
		}
	}
	if sess.Pending == nil {
		sess.mu.Unlock()
		return ErrNotReady
	}

	seed := sess.Pending.Seed
	seedRandom := seed == ""
	if seedRandom {
		seed = engine.RandomSeed()
	}
	sess.Game = engine.NewGame(engine.SetupOptions{
		Seed:       seed,
		SeedRandom: seedRandom,
		Theme:      sess.Pending.Theme,
		Jokers:     dealJokers(sess.Pending.Theme, sess.Pending.Jokers, sess.Pending.JokersCustom, seed),
		Rules:      sess.Pending.Rules,
		NumPlayers: len(sess.Room.Seats),
	}, engine.NewSeededRand(seed))
	sess.Room.Lobby = false
	sess.revealUntil = time.Time{}
	start, stop := reconcileBotsLocked(sess)
	sess.Seq++
	sess.UpdatedAt = now
	sess.cache = map[string]cachedAction{}
	s.startReplay(sess)
	err := s.persistLocked(sess)
	sess.mu.Unlock()
	goStartBot(s, sess, start, stop)
	return err
}

// PlayAgain re-deals the table that just finished: the same seats, the same rules, a
// fresh seed. The previous log is renamed, never truncated — a host who downloaded it
// after the result still has the game they played (D-47).
func (s *Store) PlayAgain(sess *Session, hostSeat, expectedSeq int) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return ErrNoRoom
	}
	if hostSeat != sess.Room.HostSeat {
		return ErrNotHost
	}
	if sess.Room.Lobby {
		return ErrRoomStarted // there is no finished game to re-deal
	}
	// Only a finished game may be re-dealt. Without this one POST /again during a live
	// game rotated the log and re-dealt the table from under three players.
	if sess.Game.Phase != engine.Won && sess.Game.Phase != engine.Lost {
		return ErrRoomStarted
	}
	if expectedSeq >= 0 && expectedSeq != sess.Seq {
		return ErrStaleSeq
	}
	if sess.Pending == nil {
		return ErrNotReady
	}
	// The old log keeps its evidence under a numbered name.
	s.rotateReplay(sess)

	seed := sess.Pending.Seed
	seedRandom := seed == ""
	if seedRandom {
		seed = engine.RandomSeed()
	}
	sess.Game = engine.NewGame(engine.SetupOptions{
		Seed:       seed,
		SeedRandom: seedRandom,
		Theme:      sess.Pending.Theme,
		Jokers:     dealJokers(sess.Pending.Theme, sess.Pending.Jokers, sess.Pending.JokersCustom, seed),
		Rules:      sess.Pending.Rules,
		NumPlayers: len(sess.Room.Seats),
	}, engine.NewSeededRand(seed))
	sess.Room.Cancelled = false
	sess.revealUntil = time.Time{}
	sess.Seq++
	sess.UpdatedAt = s.now()
	// The idempotency cache belongs to the finished game: a stale action id must not
	// answer from it, and a pinger is not a player.
	sess.cache = map[string]cachedAction{}
	sess.degraded = false
	s.startReplay(sess)
	s.enqueuePersistLocked(sess)
	return nil
}

// ViewFor returns a seat's projection: its own hand, the seat rail, and legal
// actions only when it may act.
// ok is false while the room is still a lobby: there is no game to project yet.
// ApplyFor runs an action for a seat. It is the only writer of room state, and it
// holds the lock across the turn gate and the engine call so a second request
// cannot slip between them. Seat authorisation lives here, not in the engine
// (R-056).
func (s *Store) ApplyFor(sess *Session, seat int, actionID string, expectedSeq int, act engine.Action) (int, []engine.Event, []byte, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	// The gates come before the cache: a retry must be refused for the reason the
	// room is refusing now, not handed the answer it got before the host cancelled it.
	// An idempotent answer that survives its round is a way to act on a dead table
	// (F-10).
	if sess.Room != nil && sess.Room.Cancelled {
		return sess.Seq, nil, nil, ErrCancelled
	}
	if sess.Room != nil && sess.Room.Lobby {
		return sess.Seq, nil, nil, ErrRoomStarted
	}
	key := ""
	if actionID != "" {
		key = seatKey(seat, actionID)
		if cached, ok := sess.cache[key]; ok {
			return cached.seq, cached.events, cached.body, nil
		}
	}
	actor := sess.Game.ActiveSeat
	if sess.Game.SeatChoice != nil {
		actor = sess.Game.SeatChoice.By
	}
	if seat != actor {
		return sess.Seq, nil, nil, ErrNotYourTurn
	}
	if expectedSeq >= 0 && expectedSeq != sess.Seq {
		return sess.Seq, nil, nil, ErrStaleSeq
	}

	rng := engine.NewRandAt(sess.Game.Seed, sess.Game.RGNDraws)
	next, events, err := engine.Transition(sess.Game, act, rng)
	if err != nil {
		return sess.Seq, nil, nil, err
	}
	sess.Game = next
	sess.Seq++
	sess.UpdatedAt = s.now()
	s.touchAt(sess, seat, sess.UpdatedAt)
	// Hold the bots for as long as this action's reveal plays. Every actor stamps it:
	// a human play is news to the other seats exactly as a bot's play is.
	sess.revealUntil = time.Time{}
	if budget := revealBudget(events); budget > 0 {
		sess.revealUntil = sess.UpdatedAt.Add(budget)
	}

	body, err := actionBody(sess.Seq, events)
	if err != nil {
		return sess.Seq, nil, nil, err
	}
	if key != "" {
		sess.remember(key, cachedAction{seq: sess.Seq, events: events, body: body})
	}
	// Committed in memory; persist then the replay step go to this session's writer
	// in that order, with the lock released (F-29).
	s.enqueuePersistLocked(sess)
	s.enqueueStepLocked(sess, seat, act, sess.Game)
	return sess.Seq, events, body, nil
}

// seatKey scopes an action id to its seat: two clients may pick the same id, and a
// retry must only ever return its own cached answer.
func seatKey(seat int, actionID string) string {
	return strconv.Itoa(seat) + ":" + actionID
}

// RoomListing is one row of the join screen.
type RoomListing struct {
	Code     string `json:"code"`
	Seats    int    `json:"seats"`
	Capacity int    `json:"capacity"`
	Lobby    bool   `json:"lobby"`
	Theme    string `json:"theme"`
	// Phase reads "lobby" until the deal, because a table that has not started has
	// no turn to be in.
	Phase     string    `json:"phase"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Rooms lists rooms for the join screen.
func (s *Store) Rooms() []RoomListing {
	sessions := s.sessionList()

	out := make([]RoomListing, 0, len(sessions))
	now := s.now()
	for _, sess := range sessions {
		sess.mu.Lock()
		// A cancelled table is not joinable and not watchable: it waits for the GC so
		// its own seats can read why, but it must not stay in the listing. A finished
		// table is no longer a table to join or watch, and a room with no write for an
		// hour is abandoned: neither belongs on the front page.
		finished := sess.Room != nil && !sess.Room.Lobby && (sess.Game.Phase == engine.Won || sess.Game.Phase == engine.Lost)
		stale := now.Sub(sess.UpdatedAt) > roomIdleTTL
		if sess.Room != nil && !sess.Room.Cancelled && !finished && !stale {
			entry := RoomListing{
				Code:      sess.Room.Code,
				Seats:     len(sess.Room.Seats),
				Capacity:  roomCapacity,
				Lobby:     sess.Room.Lobby,
				Phase:     "lobby",
				UpdatedAt: sess.UpdatedAt,
			}
			if !sess.Room.Lobby {
				entry.Phase = sess.Game.Phase.String()
			}
			if sess.Pending != nil {
				entry.Theme = sess.Pending.Theme
			}
			out = append(out, entry)
		}
		sess.mu.Unlock()
	}
	return out
}

// ByCode finds a room session by its code.
func (s *Store) ByCode(code string) (*Session, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	s.mu.RLock()
	id := s.roomIndex[code]
	s.mu.RUnlock()
	if id == "" {
		return nil, false
	}
	sess, ok := s.Get(id)
	if !ok {
		return nil, false
	}
	// A room past the idle hour is gone even if the once-a-minute GC has not reached
	// the file yet: the front page already hides it, and a rejoin must not race the
	// sweep.
	sess.mu.Lock()
	stale := sess.Room != nil && s.now().Sub(sess.UpdatedAt) > roomIdleTTL
	sess.mu.Unlock()
	if stale {
		return nil, false
	}
	return sess, true
}

// Now exposes the store clock so the API layer can age seats consistently (and so
// tests can move it).
func (s *Store) Now() time.Time { return s.now() }
