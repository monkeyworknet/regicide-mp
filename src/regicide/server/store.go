package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"regicide/engine"
)

// Session is one player's game. The engine state is authoritative; Seq guards
// against out-of-order requests and ActionCache makes retries idempotent.
type Session struct {
	ID        string      `json:"id"`
	Seq       int         `json:"seq"`
	Game      engine.Game `json:"game"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Claimed   bool        `json:"claimed"`
	// Room is set for a co-op session and nil for a solo one. Pending remembers how
	// the lobby was configured until the game is dealt.
	Room    *RoomInfo    `json:"room,omitempty"`
	Pending *pendingGame `json:"pending,omitempty"`

	// watchers are spectators: ephemeral, in memory only, never persisted.
	watchers map[string]*Watcher

	// hub is this room's set of live streams, in memory only: a restart is a
	// reconnect, not a session.
	hub *hub

	// outq carries this session's disk writes, in apply order, to a single goroutine
	// so no file write ever happens under mu (F-29).
	outq       chan writeJob
	writerDone chan struct{}
	// replayTickets are the host's short-lived grants for the browser viewer. In
	// memory only: a ticket that survives a restart is not short-lived.
	replayTickets map[string]time.Time

	// games counts the deals this session has had, so an old log can be kept under a
	// numbered name instead of being overwritten (PlayAgain).
	games int

	// degraded is set when the log has fallen behind and the replay is no longer
	// the whole game (F-8). The room view reports it.
	degraded bool
	// writing is set while the writer goroutine is inside a job, so GC can tell a
	// quiet queue from a quiet disk.
	writing bool
	// botStop closes when no seat is held by a bot any more. botRunning says the
	// single bot goroutine is alive, so reconciling the seat book never starts a
	// second one.
	botStop    chan struct{}
	botRunning bool
	// revealUntil is when the table's reveal for the last action has finished on the
	// screen. No bot acts before it: the player is still reading the last move
	// (0.9.8). It is in-memory only, like botStop; a restart is a fresh table.
	revealUntil time.Time

	mu    sync.Mutex
	cache map[string]cachedAction
}

type cachedAction struct {
	seq    int
	events []engine.Event
	body   []byte
}

// maxCachedActions bounds the per-session idempotency cache. Action ids are
// client-supplied, so an unhelpful client could otherwise grow the map without
// limit. Older entries are dropped first; a retry of a very old id simply runs
// again, which the sequence check still rejects if it is stale.
const maxCachedActions = 64

// sessionList snapshots the store's sessions under the read lock. Four paths (room
// listing, shutdown, ticket lookup, write flush) each walked the map by hand with the
// same three lines; the snapshot is what lets them work without the store lock.
func (s *Store) sessionList() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Session, 0, len(s.sess))
	for _, sess := range s.sess {
		list = append(list, sess)
	}
	return list
}

// actionBody is the answer one applied action returns: the sequence and the events it
// produced. Apply and ApplyFor built the same struct with the same tags by hand.
func actionBody(seq int, events []engine.Event) ([]byte, error) {
	return json.Marshal(struct {
		Seq    int            `json:"seq"`
		Events []engine.Event `json:"events"`
	}{Seq: seq, Events: events})
}

// remember stores one idempotency entry, evicting the oldest when full.
func (sess *Session) remember(id string, c cachedAction) {
	if sess.cache == nil {
		sess.cache = map[string]cachedAction{}
	}
	if _, exists := sess.cache[id]; !exists && len(sess.cache) >= maxCachedActions {
		oldestID, oldestSeq := "", 0
		first := true
		for k, v := range sess.cache {
			if first || v.seq < oldestSeq {
				oldestID, oldestSeq, first = k, v.seq, false
			}
		}
		if oldestID != "" {
			delete(sess.cache, oldestID)
		}
	}
	sess.cache[id] = c
}

// Store owns sessions and the leaderboard, with atomic JSON persistence.
type Store struct {
	dir  string
	now  func() time.Time
	mu   sync.RWMutex
	sess map[string]*Session
	// roomIndex maps a room code to its session id. It is rebuilt from disk at
	// startup so a restart does not forget the rooms people are sitting in.
	roomIndex map[string]string

	boardMu sync.Mutex
	board   Leaderboard

	limits *rateLimiter
	// seatLimits is the per-seat action budget. The IP limiter bounds a host; this
	// bounds one seat's client, so a loop in one tab cannot spend another player's
	// turns or drown the hub.
	seatLimits *rateLimiter
	// cursorLimits bounds pointer frames only: sharing the IP bucket made the client's
	// own 30 Hz rate exceed it, so most dots were dropped as 429s.
	cursorLimits *rateLimiter
	// gcHook runs inside GC between the idle check and the write drain, with the store
	// lock held and the session lock free. Only a test sets it: it is how the
	// delete-after-drain race is made deterministic.
	gcHook func(*Session)
	// botPace is how long a bot waits between actions. It is a field so a test can move
	// the clock without sleeping through a real table's rhythm.
	botPace time.Duration
	// botFill is how long a silent seat waits before a bot takes its chair. It is a
	// field for the same reason as botPace: a browser check must not sit through the
	// real minute. The shipped value is botFillAfter.
	botFill time.Duration
	// logf is where queue failures are reported. A nil logf stays silent, which is
	// what tests want.
	logf func(format string, args ...any)
	// writeHook, when set, is called for every job the writer completes (tests only).
	writeHook func(sess *Session, job writeJob)
}

// NewStore creates the data directory and loads the leaderboard.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		now:       time.Now,
		sess:      map[string]*Session{},
		roomIndex: map[string]string{},
		limits:    newRateLimiter(),
		// Generous enough that no hand can hit it, tight enough to stop a runaway
		// client: twelve a second with a burst of sixty.
		seatLimits: newRateLimiterWith(12, 60),
		// Cursors post at 30 Hz by design; the per-sender floor (40 ms) is what bounds
		// them, so this bucket only exists to stop a flood that ignores the floor.
		cursorLimits: newRateLimiterWith(120, 240),
		botPace:      defaultBotPace,
		botFill:      botFillAfter,
	}
	if err := s.loadBoard(); err != nil {
		return nil, err
	}
	// Drop session files and replays that are already past the idle TTL, so a restart
	// cannot resurrect a room (or a rejoin code) that the front page would not list.
	s.PruneOrphans(roomIdleTTL)
	s.reindexRooms()
	return s, nil
}

// ---- sessions -----------------------------------------------------------

// JokerPick is the Jester pair a browser asked the deal to show, and whether the
// player chose it by hand (a hand pick outranks the set's own rule). An empty
// Pair asks the server to choose for the set.
type JokerPick struct {
	Pair   []string
	Custom bool
}

// Create starts a new session and returns it. A seed may be supplied by the
// player (maker runs, leaderboard-ineligible) or generated server-side.
func (s *Store) Create(seed string, theme string, rules engine.RuleOptions) (*Session, error) {
	return s.CreateWith(seed, theme, rules, JokerPick{})
}

// CreateWith is Create with the browser's Jester request. The deal resolves it
// against the set, so a stale pair from another deck can never leak in.
func (s *Store) CreateWith(seed string, theme string, rules engine.RuleOptions, pick JokerPick) (*Session, error) {
	seedRandom := seed == ""
	if seedRandom {
		seed = engine.RandomSeed()
	}
	game := engine.NewGame(engine.SetupOptions{
		Seed:       seed,
		SeedRandom: seedRandom,
		Theme:      theme,
		Jokers:     dealJokers(theme, pick.Pair, pick.Custom, seed),
		Rules:      rules,
		NumPlayers: 1,
	}, engine.NewSeededRand(seed))

	sess := &Session{
		ID:        newID(),
		Game:      game,
		CreatedAt: s.now(),
		UpdatedAt: s.now(),
		cache:     map[string]cachedAction{},
	}
	s.startReplay(sess)
	s.mu.Lock()
	s.sess[sess.ID] = sess
	s.mu.Unlock()
	if err := s.persist(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Get returns a session, loading it from disk on a cache miss.
func (s *Store) Get(id string) (*Session, bool) {
	s.mu.RLock()
	sess, ok := s.sess[id]
	s.mu.RUnlock()
	if ok {
		return sess, true
	}
	if !validID(id) {
		return nil, false
	}
	loaded, err := s.load(id)
	if err != nil {
		return nil, false
	}
	// Two concurrent misses can both load the same file; the first one wins so
	// every caller shares a single Session (and its mutex) for a given id.
	s.mu.Lock()
	if existing, ok := s.sess[id]; ok {
		s.mu.Unlock()
		return existing, true
	}
	s.sess[id] = loaded
	s.mu.Unlock()
	return loaded, true
}

// Delete removes a session from memory and disk.
//
// The room code lives behind the session lock, and the store lock is never held
// while taking a session lock: read the code first, release, then take the store
// lock. Reading it under the store lock alone was the fourth face of F-3.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	sess, ok := s.sess[id]
	s.mu.Unlock()
	code := ""
	if ok && sess != nil {
		sess.mu.Lock()
		if sess.Room != nil {
			code = sess.Room.Code
		}
		sess.mu.Unlock()
	}
	s.mu.Lock()
	if code != "" {
		delete(s.roomIndex, code)
	}
	delete(s.sess, id)
	s.mu.Unlock()
	if ok && sess != nil {
		// Close the streams first: a deleted session's hub ticker kept running for
		// ever, one goroutine and one 100 ms ticker per session that ever streamed.
		s.CloseStreams(sess, "closed")
		// Finish anything already queued before removing the files it is writing to.
		// If the writer will not drain, its files are its own: the next GC sweep has
		// the rest of the session's life to try again, and a half-written log is
		// worse than a late delete.
		if !s.drainWrites(sess) {
			return
		}
	}
	s.deleteReplay(id)
	_ = os.Remove(s.sessionPath(id))
}

// Restart replaces the game inside an existing session, keeping the id so a
// client can stay on the same URL. The action cache is cleared.
func (s *Store) Restart(sess *Session, seed, theme string, rules engine.RuleOptions) error {
	return s.RestartWith(sess, seed, theme, rules, JokerPick{})
}

// RestartWith is Restart with the browser's Jester request, resolved the same way
// as a fresh deal.
func (s *Store) RestartWith(sess *Session, seed, theme string, rules engine.RuleOptions, pick JokerPick) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	seedRandom := seed == ""
	if seedRandom {
		seed = engine.RandomSeed()
	}
	sess.Game = engine.NewGame(engine.SetupOptions{
		Seed: seed, SeedRandom: seedRandom, Theme: theme,
		Jokers: dealJokers(theme, pick.Pair, pick.Custom, seed),
		Rules:  rules, NumPlayers: 1,
	}, engine.NewSeededRand(seed))
	sess.Seq++
	sess.UpdatedAt = s.now()
	sess.Claimed = false
	sess.cache = map[string]cachedAction{}
	s.startReplay(sess)
	return s.persistLocked(sess)
}

// Apply runs an action against a session. It is the only writer of game state.
// expectedSeq < 0 disables the seq check (used by tests).
func (s *Store) Apply(sess *Session, actionID string, expectedSeq int, act engine.Action) (int, []engine.Event, []byte, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if actionID != "" {
		if cached, ok := sess.cache[actionID]; ok {
			return cached.seq, cached.events, cached.body, nil
		}
	}
	if expectedSeq >= 0 && expectedSeq != sess.Seq {
		return sess.Seq, nil, nil, ErrStaleSeq
	}

	// The acting seat is captured before the transition: afterwards ActiveSeat may
	// already be the next seat, and the log would credit the wrong one (F-9).
	actor := sess.Game.ActiveSeat
	rng := engine.NewRandAt(sess.Game.Seed, sess.Game.RGNDraws)
	next, events, err := engine.Transition(sess.Game, act, rng)
	if err != nil {
		return sess.Seq, nil, nil, err
	}
	sess.Game = next
	sess.Seq++
	sess.UpdatedAt = s.now()

	body, err := actionBody(sess.Seq, events)
	if err != nil {
		return sess.Seq, nil, nil, err
	}
	if actionID != "" {
		sess.remember(actionID, cachedAction{seq: sess.Seq, events: events, body: body})
	}
	// The transition is committed in memory; the writes follow, in order, on the
	// session's own goroutine (F-29). Persist first, then the step: a log line must
	// never outlive the snapshot it belongs to (review finding, P1).
	s.enqueuePersistLocked(sess)
	s.enqueueStepLocked(sess, actor, act, sess.Game)
	return sess.Seq, events, body, nil
}

// View returns a session's sequence number and client projection under its lock.
// Handlers must use this instead of touching sess.Game, which Apply rewrites.
func (s *Store) View(sess *Session) (int, engine.ClientState) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Seq, engine.Snapshot(sess.Game)
}

// RuleSet returns the rules the current game was started with.
func (s *Store) RuleSet(sess *Session) engine.RuleOptions {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Game.Rules
}

// Sequence returns the session's current sequence number.
func (s *Store) Sequence(sess *Session) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Seq
}

// lastActive reads the session timestamp under its lock (GC holds the store lock
// when it calls this, so the order is store -> session everywhere).
func (s *Store) lastActive(sess *Session) time.Time {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.UpdatedAt
}

// ErrStaleSeq means the client acted on an out-of-date view.
var ErrStaleSeq = errors.New("stale sequence")

// ErrSeatPresent refuses a bot for a seat whose player is still at the table: a bot
// replaces an absent player, it does not take a turn from someone who is here.
var ErrSeatPresent = errors.New("seat_present")

// GC deletes sessions that have been idle for longer than ttl.
func (s *Store) GC(ttl time.Duration) int {
	cutoff := s.now().Add(-ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, sess := range s.sess {
		// A queued write that lands after the delete would recreate a headerless
		// replay file, which Verify then rejects. Wait for the next sweep instead.
		sess.mu.Lock()
		// "Pending" means queued OR being written right now: the writer takes the job
		// off the queue before it touches the disk, so an empty queue does not mean
		// the file is quiet.
		pending := (sess.outq != nil && len(sess.outq) > 0) || sess.writing
		sess.mu.Unlock()
		if pending {
			continue
		}
		if s.lastActive(sess).Before(cutoff) {
			if s.gcHook != nil {
				s.gcHook(sess)
			}
			// Drain outside the store lock so the writer can finish; if it will not,
			// the session stays for one more sweep.
			s.mu.Unlock()
			drained := s.drainWrites(sess)
			s.mu.Lock()
			if !drained {
				continue
			}
			if _, still := s.sess[id]; !still {
				continue
			}
			// The store lock was released for the drain, so the table could have woken
			// up and applied an action in that window. Deleting it then loses a live
			// game: the player's next poll 404s and the queued persist recreates a
			// headerless file. Re-check both the clock and the queue after the drain.
			sess.mu.Lock()
			woken := sess.UpdatedAt.After(cutoff) || sess.writing || (sess.outq != nil && len(sess.outq) > 0)
			sess.mu.Unlock()
			if woken {
				continue
			}
			delete(s.sess, id)
			if sess.Room != nil {
				delete(s.roomIndex, sess.Room.Code)
			}
			s.CloseStreams(sess, "expired")
			_ = os.Remove(s.sessionPath(id))
			// Every log this session ever wrote, including the rotated ones from
			// play-again, goes with it (F-7, F-18).
			s.deleteReplays(id)
			n++
		}
	}
	return n
}

// PruneOrphans removes session files older than ttl on disk (for restarts). It never
// touches a session that is live in memory: an idle table with a connected stream may
// not have written for an hour, and deleting its file (or its replay) would leave the
// next action recreating a headerless log. A replay is removed only when its session
// file is gone, not because the log itself is old.
func (s *Store) PruneOrphans(ttl time.Duration) int {
	cutoff := s.now().Add(-ttl)
	s.mu.RLock()
	live := make(map[string]bool, len(s.sess))
	for id := range s.sess {
		live[id] = true
	}
	s.mu.RUnlock()

	dir := filepath.Join(s.dir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if live[id] {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
		n++
	}
	// Re-read what is left: a session file that survived is not an orphan, whatever the
	// mtime of its replay.
	left, err := os.ReadDir(dir)
	if err != nil {
		return n
	}
	present := make(map[string]bool, len(left))
	for _, e := range left {
		present[strings.TrimSuffix(e.Name(), ".json")] = true
	}
	replays, err := os.ReadDir(filepath.Join(s.dir, "replays"))
	if err != nil {
		return n
	}
	for _, e := range replays {
		id := strings.SplitN(e.Name(), ".", 2)[0]
		if present[id] {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, "replays", e.Name()))
	}
	return n
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.dir, "sessions", id+".json")
}

// persist writes a session snapshot, taking the session lock itself.
func (s *Store) persist(sess *Session) error {
	sess.mu.Lock()
	payload, err := json.Marshal(sess)
	sess.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(s.sessionPath(sess.ID), payload)
}

// persistLocked writes a snapshot for callers that already hold the session lock.
func (s *Store) persistLocked(sess *Session) error {
	payload, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.sessionPath(sess.ID), payload)
}

func (s *Store) load(id string) (*Session, error) {
	raw, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	// A rules or shuffle change invalidates old sessions rather than replaying
	// them under different semantics.
	if sess.Game.Version != engine.Version {
		return nil, errors.New("session version mismatch")
	}
	sess.cache = map[string]cachedAction{}
	return &sess, nil
}

// writeFileAtomic writes via a temp file and rename so a crash never leaves a
// half-written JSON document.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A constant id is worse than a crash: session ids, watch ids and replay
		// tickets would all collide and be guessable. crypto/rand does not fail on a
		// working host, and if it does the server cannot be trusted to identify
		// anything.
		panic("regicide: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// ---- leaderboard --------------------------------------------------------

// Entry is one scored win.
type Entry struct {
	Name      string      `json:"name"`
	Tier      engine.Tier `json:"tier"`
	Theme     string      `json:"theme"`
	Jesters   int         `json:"jesters"`
	Turns     int         `json:"turns"`
	DurationS int         `json:"durationSeconds"`
	Seed      string      `json:"seed,omitempty"`
	// Chaos records that the run used the shuffled-royal variant. Chaos runs are
	// currently not accepted on the board; the field keeps old entries readable.
	Chaos bool      `json:"chaos,omitempty"`
	At    time.Time `json:"at"`
}

// Leaderboard is the persisted board file.
type Leaderboard struct {
	SchemaVersion int     `json:"schemaVersion"`
	Entries       []Entry `json:"entries"`
}

const boardSchemaVersion = 1

// maxBoardPerTier keeps the board bounded: the file is rewritten on every submit
// and rankOf reads the whole list. Two hundred rows per tier is far more than a
// friends-only leaderboard will ever show.
const maxBoardPerTier = 200

func (s *Store) loadBoard() error {
	raw, err := os.ReadFile(filepath.Join(s.dir, "leaderboard.json"))
	if errors.Is(err, os.ErrNotExist) {
		s.board = Leaderboard{SchemaVersion: boardSchemaVersion}
		return nil
	}
	if err != nil {
		return err
	}
	var board Leaderboard
	if err := json.Unmarshal(raw, &board); err != nil {
		// A corrupt board must not stop the server: keep a copy and start fresh.
		_ = writeFileAtomic(filepath.Join(s.dir, "leaderboard.corrupt.json"), raw)
		s.board = Leaderboard{SchemaVersion: boardSchemaVersion}
		return nil
	}
	s.board = board
	return nil
}

// Submit records a win. Eligibility is decided here, server-side: only wins from
// server-chosen seeds count, and only once per session.
func (s *Store) Submit(sess *Session, name string) (Entry, int, error) {
	if err := ValidateName(name); err != nil {
		return Entry{}, 0, err
	}

	sess.mu.Lock()
	if sess.Claimed {
		sess.mu.Unlock()
		return Entry{}, 0, ErrAlreadyClaimed
	}
	if sess.Game.Phase != engine.Won {
		sess.mu.Unlock()
		return Entry{}, 0, ErrNotEligible
	}
	if !sess.Game.SeedRandom {
		sess.mu.Unlock()
		return Entry{}, 0, ErrNotEligible
	}
	// Only the standard ruleset is comparable. An allow-list (rather than a list
	// of exclusions) means a future easier option cannot silently create a hole.
	if sess.Game.Rules != engine.DefaultRules() {
		sess.mu.Unlock()
		return Entry{}, 0, ErrNotEligible
	}
	entry := Entry{
		Name:      name,
		Tier:      engine.TierFor(sess.Game.JestersUsed),
		Theme:     sess.Game.Theme,
		Jesters:   sess.Game.JestersUsed,
		Turns:     sess.Game.TurnNo,
		Chaos:     sess.Game.Rules.Chaos,
		DurationS: int(sess.UpdatedAt.Sub(sess.CreatedAt).Seconds()),
		At:        s.now().UTC(),
	}
	sess.Claimed = true
	sess.mu.Unlock()

	// The lock covers the write as well as the mutation: releasing it first let two
	// concurrent submits persist out of order, and the older snapshot won on disk.
	s.boardMu.Lock()
	s.board.Entries = append(s.board.Entries, entry)
	trimBoard(&s.board, maxBoardPerTier)
	s.board.SchemaVersion = boardSchemaVersion
	payload, err := json.MarshalIndent(s.board, "", " ")
	if err == nil {
		err = writeFileAtomic(filepath.Join(s.dir, "leaderboard.json"), payload)
	}
	s.boardMu.Unlock()
	_ = s.persist(sess)

	rank := s.rankOf(entry)
	return entry, rank, err
}

// Errors for leaderboard submission.
var (
	ErrAlreadyClaimed = errors.New("this game was already submitted")
	ErrNotEligible    = errors.New("this game is not eligible for the leaderboard")
	ErrBadName        = errors.New("name must be 2-16 letters, numbers, spaces, - or _")
)

// trimBoard drops the worst entries so each tier holds at most per entries. It
// runs under boardMu, after the new entry is appended.
func trimBoard(board *Leaderboard, per int) {
	if len(board.Entries) <= per {
		return
	}
	byTier := map[engine.Tier][]Entry{}
	for _, e := range board.Entries {
		byTier[e.Tier] = append(byTier[e.Tier], e)
	}
	kept := make([]Entry, 0, len(board.Entries))
	for _, list := range byTier {
		sortEntries(list)
		if len(list) > per {
			list = list[:per]
		}
		kept = append(kept, list...)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].At.Before(kept[j].At) })
	board.Entries = kept
}

// sortEntries orders one tier: fewest turns, then fastest, then oldest.
func sortEntries(list []Entry) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Turns != list[j].Turns {
			return list[i].Turns < list[j].Turns
		}
		if list[i].DurationS != list[j].DurationS {
			return list[i].DurationS < list[j].DurationS
		}
		return list[i].At.Before(list[j].At)
	})
}

// Top returns the best entries per tier (fewest turns, then fastest).
func (s *Store) Top(limit int) map[engine.Tier][]Entry {
	if limit <= 0 {
		limit = 20
	}
	s.boardMu.Lock()
	entries := append([]Entry(nil), s.board.Entries...)
	s.boardMu.Unlock()

	byTier := map[engine.Tier][]Entry{}
	for _, e := range entries {
		byTier[e.Tier] = append(byTier[e.Tier], e)
	}
	for tier := range byTier {
		list := byTier[tier]
		sortEntries(list)
		if len(list) > limit {
			list = list[:limit]
		}
		byTier[tier] = list
	}
	return byTier
}

func (s *Store) rankOf(entry Entry) int {
	list := s.Top(1000)[entry.Tier]
	for i, e := range list {
		if e.At.Equal(entry.At) && e.Name == entry.Name {
			return i + 1
		}
	}
	return 0
}

// ValidateName enforces the nickname policy: 2-16 visible characters from a safe
// set, normalised whitespace, no obvious slurs.
func ValidateName(name string) error {
	trimmed := normaliseName(name)
	if n := len([]rune(trimmed)); n < 2 || n > 16 {
		return ErrBadName
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_':
		case r > 127:
			return ErrBadName
		default:
			return ErrBadName
		}
	}
	if hasProfanity(trimmed) {
		return ErrBadName
	}
	return nil
}

func normaliseName(name string) string {
	out := make([]rune, 0, len(name))
	lastSpace := false
	for _, r := range name {
		if r == '\t' || r == '\n' || r == '\r' {
			r = ' '
		}
		if r == ' ' {
			if lastSpace || len(out) == 0 {
				continue
			}
			lastSpace = true
		} else {
			lastSpace = false
		}
		out = append(out, r)
	}
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return string(out)
}

var blockedWords = []string{"fuck", "shit", "cunt", "nigger", "faggot", "rape"}

func hasProfanity(name string) bool {
	lower := make([]rune, 0, len(name))
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		lower = append(lower, r)
	}
	s := string(lower)
	for _, w := range blockedWords {
		if contains(s, w) {
			return true
		}
	}
	return false
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// ---- rate limiting ------------------------------------------------------

type bucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64
	burst     float64
	now       func() time.Time
	lastSweep time.Time
}

// idleBucketTTL is how long an untouched bucket is kept. A bucket that has been
// idle for longer has refilled to burst anyway, so dropping it changes nothing
// about the budget, only about memory.
const idleBucketTTL = 10 * time.Minute

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}, rate: 10, burst: 30, now: time.Now}
}

// newRateLimiterWith is the same bucket with an explicit rate and burst, for the
// per-seat action budget (T8.6).
func newRateLimiterWith(rate, burst float64) *rateLimiter {
	return &rateLimiter{buckets: map[string]*bucket{}, rate: rate, burst: burst, now: time.Now}
}

// sweep drops idle buckets. Callers hold l.mu.
func (l *rateLimiter) sweep(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < idleBucketTTL/2 {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > idleBucketTTL {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}

func (l *rateLimiter) bucketCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// allow consumes one token for key; false means the caller is over budget.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Periodic, not per-insert: a spoofed-IP flood must not pay an O(n) walk on
	// every request (that is quadratic), and idle buckets have refilled to burst
	// long before the TTL, so a coarser sweep costs nothing in accuracy.
	l.sweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// reindexRooms rebuilds the room-code index from the sessions on disk, so a
// restart keeps every table that is mid-game reachable by its code.
func (s *Store) reindexRooms() {
	entries, err := os.ReadDir(filepath.Join(s.dir, "sessions"))
	if err != nil {
		return
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(s.dir, "sessions", e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(raw, &sess); err != nil || sess.Room == nil {
			continue
		}
		if sess.Game.Version != engine.Version && !sess.Room.Lobby {
			continue // the loader would discard it anyway
		}
		s.roomIndex[sess.Room.Code] = sess.ID
	}
}
