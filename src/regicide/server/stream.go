package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// The presence transport (docs/MP-PLAN.md §4, D-34): one text/event-stream per seat
// or spectator, carrying state frames when the table changes, coalesced presence
// frames at 10 Hz, and a comment ping so a proxy does not close an idle stream.
// Actions and cursors travel up as POSTs; this file only pushes down.
//
// Two rules hold everywhere below:
//   - every state frame is built by ProjectFor/ProjectForWatcher, so the stream has no
//     projection of its own and cannot leak what the HTTP routes withhold (F-2, F-3);
//   - presence never touches app.state on the client and never triggers a render: it
//     moves nodes that already exist.

const (
	// subQueue bounds one subscriber's backlog. A client that cannot keep up loses
	// old frames, never the newest: at 10 Hz a 32-frame buffer is three seconds.
	subQueue = 32
	// presenceHz is how often the coalesced pointer frame goes out.
	// presenceEvery is the heartbeat for dots that are not moving. A moving cursor is
	// flushed immediately by setCursorNudge, so this only has to keep the frame warm.
	presenceEvery = 60 * time.Millisecond
	// streamPing keeps intermediaries from treating the stream as dead.
	streamPing = 15 * time.Second
	// streamSoftLife is how long one stream lives before the client reconnects. It
	// bounds the damage of a wedged connection and matches the 30 s WriteTimeout
	// story in MP-PLAN: the handler clears the deadline, but never forever.
	streamSoftLife = 10 * time.Minute
)

// subscriber is one live stream. The id is a counter, not a credential.
type subscriber struct {
	id    int64
	seat  int    // -1 for a spectator
	watch string // watch id, empty for a seat
	// as is the seat a spectator asked to look through ("view as"). It lives on the
	// subscriber so every streamed frame is projected the same way the ?as= poll is,
	// instead of resetting the choice to the hand-less table view on the next action.
	as      int // -1 for the table view
	ch      chan []byte
	closed  bool
	dropped atomic.Int64
}

// hub is a room's set of live streams plus the latest pointer positions. It is
// in-memory only: a restart is a reconnect, not a session.
type hub struct {
	mu        sync.Mutex
	subs      map[int64]*subscriber
	next      int64
	cursors   map[int]Cursor // seat -> last accepted pointer frame
	watchers  map[string]Cursor
	closed    bool
	lastFlush time.Time
	stop      chan struct{}
	once      sync.Once
}

// maxStreamsPerRoom bounds live streams per room. A LAN table needs a handful; the
// cap is what keeps one client from turning the marathon stream into a resource leak.
const maxStreamsPerRoom = 64

var errStreamFull = errors.New("stream_full")

func newHub() *hub {
	return &hub{
		subs:     map[int64]*subscriber{},
		cursors:  map[int]Cursor{},
		watchers: map[string]Cursor{},
		stop:     make(chan struct{}),
	}
}

// hubFor returns the session's hub, creating and starting it on first use.
// Caller holds sess.mu.
func (s *Store) hubForLocked(sess *Session) *hub {
	if sess.hub == nil {
		sess.hub = newHub()
		go s.runHub(sess, sess.hub)
	}
	return sess.hub
}

// subscribe registers one stream. A closed hub refuses, which is how a cancelled
// room stops handing out new ones.
func (h *hub) subscribe(seat int, watch string) (*subscriber, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("stream_closed")
	}
	// A stream lives for streamSoftLife and holds a goroutine. Eight watcher ids are
	// bounded, but nothing bounded the connections: one watch id could open thousands.
	if len(h.subs) >= maxStreamsPerRoom {
		return nil, errStreamFull
	}
	h.next++
	// as starts at -1 (the table view). Zero meant "look through seat 0", so a spectator
	// in an open-hands room received the host's hand on every frame after the welcome.
	sub := &subscriber{id: h.next, seat: seat, watch: watch, as: -1, ch: make(chan []byte, subQueue)}
	h.subs[sub.id] = sub
	return sub, nil
}

// snapshot copies the subscriber list under the hub lock. Every caller that walks the
// live set uses this: reading the map unlocked is a race with unsubscribe.
func (h *hub) snapshot() []*subscriber {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*subscriber, 0, len(h.subs))
	for _, sub := range h.subs {
		out = append(out, sub)
	}
	return out
}

// unsubscribe removes a stream and wakes nobody: the handler is leaving anyway.
func (h *hub) unsubscribe(sub *subscriber) {
	h.mu.Lock()
	delete(h.subs, sub.id)
	h.mu.Unlock()
}

// send delivers one frame, dropping the oldest for a subscriber that is behind
// rather than blocking the publisher, and it refuses once the hub or the subscriber
// is closed. It holds the hub lock for the whole send: that is what makes
// send-after-close impossible, because close() sets closed under the same lock and
// only then closes the channels. Every send in this file goes through here.
func (h *hub) send(sub *subscriber, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || sub.closed {
		return
	}
	select {
	case sub.ch <- frame:
		return
	default:
	}
	// Full: drop the oldest and take the newest. A live table that skips a pointer
	// frame is better than a table that blocks on one client.
	select {
	case <-sub.ch:
		sub.dropped.Add(1)
	default:
	}
	select {
	case sub.ch <- frame:
	default:
		sub.dropped.Add(1)
	}
}

// broadcast hands one frame to every subscriber.
func (h *hub) broadcast(frame []byte) {
	subs := h.snapshot()
	for _, sub := range subs {
		h.send(sub, frame)
	}
}

// close stops the hub: every subscriber gets a bye and its channel is closed.
func (h *hub) close(reason string) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	subs := make([]*subscriber, 0, len(h.subs))
	for _, sub := range h.subs {
		subs = append(subs, sub)
	}
	h.subs = map[int64]*subscriber{}
	h.mu.Unlock()

	frame := sseFrame("", "bye", map[string]string{"reason": reason})
	// Mark closed under the lock before any channel is closed, so a sender that
	// already holds the lock has finished and one that arrives later sees closed.
	h.mu.Lock()
	for _, sub := range subs {
		sub.closed = true
	}
	h.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.ch <- frame:
		default:
		}
		close(sub.ch)
	}
	h.once.Do(func() { close(h.stop) })
}

// dropSeat closes the streams of one seat without touching the rest of the table.
// A removed or kicked player must not keep receiving a board their token no longer
// owns.
func (h *hub) dropSeat(seat int, reason string) {
	if seat < 0 {
		return
	}
	h.mu.Lock()
	var victims []*subscriber
	for id, sub := range h.subs {
		if sub.seat == seat {
			delete(h.subs, id)
			victims = append(victims, sub)
		}
	}
	delete(h.cursors, seat)
	h.mu.Unlock()
	if len(victims) == 0 {
		return
	}
	frame := sseFrame("", "bye", map[string]string{"reason": reason})
	h.mu.Lock()
	for _, sub := range victims {
		sub.closed = true
	}
	h.mu.Unlock()
	for _, sub := range victims {
		select {
		case sub.ch <- frame:
		default:
		}
		close(sub.ch)
	}
}

// renumber follows a lobby compaction: every live seat stream and pointer moves to
// the seat number the server just gave that player.
func (h *hub) renumber(remap map[int]int) {
	if len(remap) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		if sub.seat < 0 {
			continue
		}
		if next, ok := remap[sub.seat]; ok {
			sub.seat = next
		}
	}
	moved := make(map[int]Cursor, len(h.cursors))
	for seat, c := range h.cursors {
		if next, ok := remap[seat]; ok {
			seat = next
		}
		moved[seat] = c
	}
	h.cursors = moved
}

// runHub owns the clock for one room: a coalesced presence frame every 100 ms while
// anyone is listening. One goroutine per room, not per subscriber, so a table of four
// costs one ticker.
func (s *Store) runHub(sess *Session, h *hub) {
	ticker := time.NewTicker(presenceEvery)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			subs := h.frames()
			if len(subs) == 0 {
				continue
			}
			// An open stream IS the heartbeat D-41 promises. Without this the only
			// thing that refreshed LastSeen was a GET or a /ping, and the default
			// client does neither: after 90 s every seat read as away, the banner said
			// "Waiting for ...", the host was offered "Take the seat" for a player who
			// was sitting right there — and a spectator's watch id expired after 60 s,
			// after which ProjectForWatcher refuses and the spectator's board freezes
			// for good, still showing a live table.
			if s.touchSubscribers(sess, subs) {
				// The crown moved while everyone was streaming: tell the table. Publish
				// takes sess.mu, so it must happen after touchSubscribers released it.
				s.Publish(sess, "host")
			}
			// The tick is the only regular clock a streaming table has, so it is where a
			// seat that went quiet gets its bot. Without this the 60-second recovery
			// only ran when somebody happened to poll (D-41, T4.1).
			if s.sweepBotFill(sess) {
				s.Publish(sess, "botfill")
			}
			h.broadcast(h.presenceFrame())
		}
	}
}

// flushPresence broadcasts the table's pointer positions now, at most once per
// presenceFlush window. A moving dot is the one thing that must not queue behind a
// timer, but seven clients at 30 Hz must not become a flood either.
func (h *hub) flushPresence(now time.Time) {
	h.mu.Lock()
	if h.closed || now.Sub(h.lastFlush) < presenceFlush {
		h.mu.Unlock()
		return
	}
	h.lastFlush = now
	h.mu.Unlock()
	h.broadcast(h.presenceFrame())
}

// presenceFlush is the coalescing window for cursor-driven frames.
const presenceFlush = 25 * time.Millisecond

// presenceFrame snapshots every known pointer position into one frame. A new
// subscriber gets one immediately, so a client never waits up to a tick to learn
// where anyone is.
func (h *hub) presenceFrame() []byte {
	h.mu.Lock()
	seats := make([]presenceSeat, 0, len(h.cursors))
	for seat, c := range h.cursors {
		seats = append(seats, presenceSeat{Seat: seat, X: c.X, Y: c.Y, Mark: c.Mark, MX: c.MX, MY: c.MY, Hidden: c.Hidden})
	}
	watchers := make([]presenceSeat, 0, len(h.watchers))
	for id, c := range h.watchers {
		// The watch id is the spectator's whole credential: it authenticates their reads
		// and their DELETE. Presence only needs a stable handle to colour the dot, so
		// what leaves the server is a hash (X4).
		watchers = append(watchers, presenceSeat{X: c.X, Y: c.Y, Mark: c.Mark, MX: c.MX, MY: c.MY, Hidden: c.Hidden, Watch: watchSeatHandle(id)})
	}
	h.mu.Unlock()
	return sseFrame("", "presence", map[string]any{"seats": seats, "watchers": watchers})
}

// watchSeatHandle is the opaque per-spectator handle used in presence frames.
func watchSeatHandle(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("presence:" + id))
	return hex.EncodeToString(sum[:6])
}

// presenceSeat is one pointer position in a presence frame. Watch is set only for a
// spectator's own orb, which the server identifies so a client can colour it grey.
type presenceSeat struct {
	// No omitempty: seat 0 is the host, and a pointer without a seat cannot be drawn.
	Seat  int    `json:"seat"`
	Watch string `json:"watch,omitempty"`
	// X, Y are fractions of the board; Mark names the felt mark the pointer is over
	// (-1 for none) with MX, MY inside it. No omitempty on Mark: mark 0 is real.
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Mark   int     `json:"mark"`
	MX     float64 `json:"mx"`
	MY     float64 `json:"my"`
	Hidden bool    `json:"hidden,omitempty"`
}

// setCursor stores a seat's pointer frame. It returns applied=false inside the floor.
func (h *hub) setCursor(seat int, c Cursor, now time.Time) (applied, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prev, seen := h.cursors[seat]
	if seen && prev.At != 0 && now.UnixMilli()-prev.At < int64(cursorFloor/time.Millisecond) {
		return false, true
	}
	c.At = now.UnixMilli()
	h.cursors[seat] = c
	return true, true
}

// dropWatcher forgets a spectator's last pointer. Without it the map grew with every
// reload, a departed spectator kept a grey dot on every table for ever, and each presence
// frame carried every ghost (X3).
func (h *hub) dropWatcher(id string) {
	if h == nil || id == "" {
		return
	}
	h.mu.Lock()
	delete(h.watchers, id)
	h.mu.Unlock()
}

// setWatcherCursor is the spectator equivalent, keyed by watch id.
func (h *hub) setWatcherCursor(id string, c Cursor, now time.Time) (applied, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prev, seen := h.watchers[id]
	if seen && prev.At != 0 && now.UnixMilli()-prev.At < int64(cursorFloor/time.Millisecond) {
		return false, true
	}
	c.At = now.UnixMilli()
	h.watchers[id] = c
	return true, true
}

// subFrame is a subscriber with the fields a frame needs, read under the hub lock.
// hub.renumber rewrites sub.seat under that lock when a lobby compaction moves the
// chairs, so reading the seat afterwards (as Publish and the presence tick both did)
// was a data race against a leave or a kick.
type subFrame struct {
	sub   *subscriber
	seat  int
	watch string
	as    int
}

// frames snapshots the subscribers for one publish, with their seat numbers.
func (h *hub) frames() []subFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]subFrame, 0, len(h.subs))
	for _, sub := range h.subs {
		out = append(out, subFrame{sub: sub, seat: sub.seat, watch: sub.watch, as: sub.as})
	}
	return out
}

// statePayload is the frame body for one projection. The stream, the welcome frame and
// the poll path all speak this shape; building it in three places is how the fields
// drift apart.
func statePayload(p Projection, seat int, reason string) map[string]any {
	payload := map[string]any{
		"seq":   p.Seq,
		"seat":  seat,
		"lobby": p.Lobby,
		"room":  p.Room,
	}
	if p.HasGame {
		payload["state"] = p.State
	}
	if reason != "" {
		payload["reason"] = reason
	}
	return payload
}

// Publish sends one state frame to everyone in the room. It is called after the lock
// is released, and it projects per subscriber, so a seat and a spectator never share
// a frame.
func (s *Store) Publish(sess *Session, reason string) {
	sess.mu.Lock()
	h := sess.hub
	sess.mu.Unlock()
	if h == nil {
		return
	}
	frames := h.frames()
	if len(frames) == 0 {
		return
	}
	for _, f := range frames {
		var p Projection
		if f.watch != "" {
			// A spectator's chosen seat travels with the subscriber (D-45): without
			// this, "view as" lasted exactly until the next action frame.
			var ok bool
			p, ok = s.ProjectForWatcher(sess, f.watch, f.as)
			if !ok {
				continue
			}
		} else {
			p = s.ProjectFor(sess, f.seat)
		}
		h.send(f.sub, sseFrame(strconv.Itoa(p.Seq), "state", statePayload(p, f.seat, reason)))
	}
}

// CloseStreams ends every stream in a room with a reason. Cancel uses it, and so does
// server shutdown.
func (s *Store) CloseStreams(sess *Session, reason string) {
	sess.mu.Lock()
	h := sess.hub
	sess.hub = nil
	sess.mu.Unlock()
	if h != nil {
		h.close(reason)
	}
}

// CloseAllStreams is the shutdown path: every room says goodbye and every hub
// goroutine exits.
func (s *Store) CloseAllStreams(reason string) {
	for _, sess := range s.sessionList() {
		s.CloseStreams(sess, reason)
	}
}

// sseFrame builds one event. An empty id leaves the last one in place.
func sseFrame(id, event string, payload any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	out := make([]byte, 0, len(raw)+32)
	if id != "" {
		out = append(out, "id: "...)
		out = append(out, id...)
		out = append(out, '\n')
	}
	out = append(out, "event: "...)
	out = append(out, event...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, raw...)
	out = append(out, '\n', '\n')
	return out
}

// handleStream serves GET /api/rooms/{code}/stream. A seat authenticates with its
// token, a spectator with its watch id; there is no anonymous stream, because a
// stream carries the same view a poll would.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	seat, watch, ok := s.streamIdentity(sess, r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "seat_or_watch_required"})
		return
	}

	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming_unsupported"})
		return
	}
	// A stream outlives the server's 30 s write timeout, so take the deadline off the
	// connection and manage it here. An unsupported controller cannot stream, and
	// saying so beats a handler that dies on the first frame.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Now().Add(streamSoftLife + time.Minute)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming_unsupported"})
		return
	}

	sess.mu.Lock()
	h := s.store.hubForLocked(sess)
	sess.mu.Unlock()

	// A room at its stream cap is the one error that can be told before the headers,
	// so it gets a 429 with Retry-After.
	sub, err := h.subscribe(seat, watch)
	if err != nil && errors.Is(err, errStreamFull) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_streams"})
		return
	}
	if err == nil {
		defer h.unsubscribe(sub)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err != nil {
		// The hub closed between the identity check and the subscribe. The contract the
		// client relies on is unchanged: a 200 stream whose first frame says goodbye,
		// which the tab shows as the closed table. A 503 here made it retry for ever.
		_, _ = w.Write(sseFrame("", "bye", map[string]string{"reason": "closed"}))
		flusher.Flush()
		return
	}

	// The first frame is the full view, so a client never has to poll on connect.
	// It is read back out of the subscriber's own projection, which keeps the stream
	// and the poll path from disagreeing.
	var first []byte
	if watch != "" {
		if p, ok := s.store.ProjectForWatcher(sess, watch, -1); ok {
			first = sseFrame(strconv.Itoa(p.Seq), "state", statePayload(p, -1, "welcome"))
		}
	} else {
		p := s.store.ProjectFor(sess, seat)
		// The welcome frame names this subscriber's seat, exactly like every later
		// frame: a compaction between the page load and the stream would otherwise
		// leave the tab on a label that no longer exists.
		first = sseFrame(strconv.Itoa(p.Seq), "state", statePayload(p, seat, "welcome"))
	}
	if first != nil {
		if _, err := w.Write(first); err != nil {
			return
		}
	}
	// Presence straight away: a client draws the orbs from the first paint rather than
	// a tick later, and a test can rely on it.
	if _, err := w.Write(h.presenceFrame()); err != nil {
		return
	}
	flusher.Flush()

	ping := time.NewTicker(streamPing)
	defer ping.Stop()
	life := time.NewTimer(streamSoftLife)
	defer life.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-life.C:
			_, _ = w.Write(sseFrame("", "bye", map[string]string{"reason": "reconnect"}))
			flusher.Flush()
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case frame, open := <-sub.ch:
			if !open {
				return
			}
			if frame == nil {
				continue
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// streamIdentity authenticates a stream request. A seat token wins over a watch id,
// because a browser that holds both is a player.
func (s *Server) streamIdentity(sess *Session, r *http.Request) (seat int, watch string, ok bool) {
	if raw := r.Header.Get("X-Regicide-Seat"); raw != "" {
		if n, authed := s.seatFromRequest(sess, r); authed {
			return n, "", true
		}
	}
	if id := r.Header.Get("X-Regicide-Watch"); id != "" {
		if s.store.WatcherAuth(sess, id) {
			return -1, id, true
		}
	}
	return 0, "", false
}

// cursorBody is the wire form of a pointer frame.
type cursorBody struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Mark   *int    `json:"mark"`
	MX     float64 `json:"mx"`
	MY     float64 `json:"my"`
	Hidden bool    `json:"hidden"`
}

// Cursor converts the wire form, defaulting Mark to "none" when the client omits it
// (an older tab, which only ever sent board fractions).
func (b cursorBody) Cursor() Cursor {
	mark := -1
	if b.Mark != nil {
		mark = *b.Mark
	}
	return Cursor{X: b.X, Y: b.Y, Mark: mark, MX: b.MX, MY: b.MY, Hidden: b.Hidden}
}

// handleCursor accepts a seat's pointer frame: POST /api/rooms/{code}/cursor.
func (s *Server) handleCursor(w http.ResponseWriter, r *http.Request, sess *Session, seat int) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if !s.store.cursorLimits.allow(s.clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		return
	}
	var body cursorBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sess.mu.Lock()
	h := s.store.hubForLocked(sess)
	sess.mu.Unlock()
	applied, ok := h.setCursor(seat, body.Cursor().Normalised(), s.store.Now())
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no_room"})
		return
	}
	if applied {
		h.flushPresence(s.store.Now())
	}
	w.WriteHeader(http.StatusNoContent)
}

// touchSubscribers refreshes presence for everyone whose stream is still open: a seat
// through TouchSeat's own path, a spectator through its watch id. It runs once per
// presence tick, so a table of four costs four timestamp writes per 100 ms.
func (s *Store) touchSubscribers(sess *Session, subs []subFrame) (hostChanged bool) {
	now := s.now()
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Room == nil {
		return false
	}
	for _, f := range subs {
		if f.watch != "" {
			if w, ok := sess.watchers[f.watch]; ok {
				w.LastSeen = now
			}
			continue
		}
		// A dealt game never renumbers, and a lobby compaction remaps the subscriber
		// in the same call that removes the chair, so the seat is a valid index here.
		if f.seat >= 0 && f.seat < len(sess.Room.Seats) {
			sess.Room.Seats[f.seat].LastSeen = now
		}
	}
	sess.UpdatedAt = now
	// Presence is the heartbeat, so it is also the only regular place where a host who
	// walked away can be noticed: the SSE client never polls, and an election that only
	// ran on a GET never ran at all (X1).
	return s.electHostLocked(sess, now)
}
