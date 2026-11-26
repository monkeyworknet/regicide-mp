package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- the presence stream (T1.1-T1.3) --------------------------------------

// streamReader is a live SSE connection from a test's point of view.
type streamReader struct {
	resp   *http.Response
	frames chan map[string]any
	raw    chan string
	cancel context.CancelFunc
}

// openStream connects and returns the first state frame plus a channel of the rest.
func (c *roomClient) openStream(headers map[string]string) (*streamReader, map[string]any, error) {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.srv.URL+"/api/rooms/"+c.code+"/stream", nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	sr := &streamReader{resp: resp, frames: make(chan map[string]any, 64), raw: make(chan string, 64), cancel: cancel}
	go func() {
		defer close(sr.frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var event string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var payload map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err == nil {
					payload["_event"] = event
					select {
					case sr.frames <- payload:
					default:
					}
				}
			case strings.HasPrefix(line, ": "):
				select {
				case sr.raw <- line:
				default:
				}
			}
		}
	}()
	first, ok := <-sr.frames
	if !ok {
		sr.cancel()
		return nil, nil, fmt.Errorf("stream closed before its first frame")
	}
	return sr, first, nil
}

func (sr *streamReader) next(t *testing.T, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame, ok := <-sr.frames:
			if !ok {
				t.Fatalf("stream closed while waiting for a %q frame", want)
			}
			if frame["_event"] == want {
				return frame
			}
		case <-deadline:
			t.Fatalf("no %q frame within %v", want, timeout)
		}
	}
}

func (c *roomClient) seatHeader() map[string]string {
	return map[string]string{"X-Regicide-Seat": fmt.Sprintf("%d:%s", c.seat, c.token)}
}

// startedRoom builds a two-seat room that has already dealt.
func startedRoom(t *testing.T) (host *roomClient, guest roomClient) {
	t.Helper()
	host = newRoomClient(t)
	host.createRoom("simple")
	guest = *host
	guest.join()
	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	return host, guest
}

func TestStreamSendsStateOnApply(t *testing.T) {
	host, guest := startedRoom(t)
	seat0, first, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer seat0.cancel()
	if first["_event"] != "state" || first["reason"] != "welcome" {
		t.Fatalf("first frame = %v", first["_event"])
	}
	if _, ok := first["state"].(map[string]any); !ok {
		t.Fatalf("the welcome frame carries no state")
	}

	// The guest's own stream may not be needed; the host streams their own turn.
	state := host.get("/api/rooms/" + host.code)["state"].(map[string]any)
	legal := state["legal"].([]any)
	choice := legal[0].(map[string]any)
	action := map[string]any{"type": choice["type"]}
	if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
		action["cardIds"] = ids
	}
	if resp, out := host.act(action); resp.StatusCode != http.StatusOK {
		t.Fatalf("action: %d %v", resp.StatusCode, out)
	}

	frame := seat0.next(t, "state", 3*time.Second)
	if frame["reason"] != "action" {
		t.Fatalf("state frame reason = %v, want action", frame["reason"])
	}
	if int(frame["seq"].(float64)) < 2 {
		t.Fatalf("state frame seq = %v", frame["seq"])
	}
	_ = guest
}

func TestStreamAuthSeatAndWatcher(t *testing.T) {
	host, _ := startedRoom(t)

	// A seat token opens a stream.
	seat, _, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("seat stream: %v", err)
	}
	seat.cancel()

	// A spectator id does too, with no seat.
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch: %d %v", resp.StatusCode, out)
	}
	watchID := out["watchId"].(string)
	w, first, err := host.openStream(map[string]string{"X-Regicide-Watch": watchID})
	if err != nil {
		t.Fatalf("watch stream: %v", err)
	}
	w.cancel()
	if s, ok := first["state"].(map[string]any); ok {
		if hand, ok := s["hand"].([]any); ok && len(hand) > 0 {
			t.Fatalf("a spectator stream carried a hand")
		}
	}
}

func TestStreamRejectsForeignSeat(t *testing.T) {
	host, guest := startedRoom(t)
	// A token that does not belong to this room, and one that claims the wrong seat.
	for _, hdr := range []map[string]string{
		{"X-Regicide-Seat": "0:not-a-token"},
		{"X-Regicide-Seat": fmt.Sprintf("%d:%s", guest.seat, "nope")},
		{},
	} {
		resp, err := host.srv.Client().Do(mustRequest(t, host.srv.URL+"/api/rooms/"+host.code+"/stream", hdr))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("headers %v: status %d, want 401", hdr, resp.StatusCode)
		}
	}
}

// TestStreamNoForeignHands is the leak test for the new path: a seat's stream must
// carry that seat's hand and no other, exactly like its poll.
func TestStreamNoForeignHands(t *testing.T) {
	host, guest := startedRoom(t)
	seat1, first, err := (&guest).openStream((&guest).seatHeader())
	if err != nil {
		t.Fatalf("guest stream: %v", err)
	}
	defer seat1.cancel()

	state := first["state"].(map[string]any)
	if int(state["viewerSeat"].(float64)) != 1 {
		t.Fatalf("guest stream viewerSeat = %v, want 1", state["viewerSeat"])
	}
	players := state["players"].([]any)
	own := 0
	for _, raw := range players {
		p := raw.(map[string]any)
		hand, _ := p["hand"].([]any)
		if int(p["seat"].(float64)) == 1 {
			own = len(hand)
			continue
		}
		if len(hand) != 0 {
			t.Fatalf("seat %v's hand reached seat 1's stream (%d cards)", p["seat"], len(hand))
		}
	}
	if own == 0 {
		t.Fatalf("the guest's own hand is missing from its stream")
	}

	// And the host's stream, opened at the same time, must not see the guest's hand.
	seat0, hostFirst, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("host stream: %v", err)
	}
	defer seat0.cancel()
	for _, raw := range hostFirst["state"].(map[string]any)["players"].([]any) {
		p := raw.(map[string]any)
		hand, _ := p["hand"].([]any)
		if int(p["seat"].(float64)) == 1 && len(hand) != 0 {
			t.Fatalf("the guest's hand reached the host's stream (%d cards)", len(hand))
		}
	}
}

// TestStreamNoSeedUntilFinished: a seeded co-op table withholds the seed on the
// stream exactly as it does on the poll.
func TestStreamNoSeedUntilFinished(t *testing.T) {
	host := newRoomClient(t)
	resp, out := host.do(http.MethodPost, "/api/rooms", map[string]any{"theme": "simple", "seed": "STREAM-SEED"}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	host.code = out["code"].(string)
	host.seat = int(out["seat"].(float64))
	host.token = out["token"].(string)
	guest := *host
	guest.join()
	host.start()

	seat, first, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer seat.cancel()
	if seed, ok := first["state"].(map[string]any)["seed"].(string); ok && seed != "" {
		t.Fatalf("the stream published the seed %q mid-game", seed)
	}
	// A spectator stream must not carry it either, even with hands allowed.
	host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": true, "hands": true}, true)
	wresp, wout := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false)
	if wresp.StatusCode != http.StatusOK {
		t.Fatalf("watch: %d %v", wresp.StatusCode, wout)
	}
	w, wfirst, err := host.openStream(map[string]string{"X-Regicide-Watch": wout["watchId"].(string)})
	if err != nil {
		t.Fatalf("watch stream: %v", err)
	}
	defer w.cancel()
	if seed, ok := wfirst["state"].(map[string]any)["seed"].(string); ok && seed != "" {
		t.Fatalf("a spectator stream published the seed %q", seed)
	}
	_ = guest
}

// TestStreamRespectsWatchHands: switching spectator hands off must reach a live
// stream, not just the next poll (F-12, D-45).
func TestStreamRespectsWatchHands(t *testing.T) {
	host, _ := startedRoom(t)
	host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": true, "hands": true}, true)

	wresp, wout := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false)
	if wresp.StatusCode != http.StatusOK {
		t.Fatalf("watch: %d %v", wresp.StatusCode, wout)
	}
	watchID := wout["watchId"].(string)
	w, first, err := host.openStream(map[string]string{"X-Regicide-Watch": watchID})
	if err != nil {
		t.Fatalf("watch stream: %v", err)
	}
	defer w.cancel()
	// View-as a player: hands on, so the first frame is that seat's board.
	_ = first

	// Switch hands off. The stream re-projects and the hand disappears.
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": true, "hands": false}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("watching off: %d %v", resp.StatusCode, out)
	}
	frame := w.next(t, "state", 3*time.Second)
	state, _ := frame["state"].(map[string]any)
	hand, _ := state["hand"].([]any)
	if len(hand) != 0 {
		t.Fatalf("a spectator stream still carries %d cards after hands were switched off", len(hand))
	}
}

func TestStreamSlowConsumerDropsOldest(t *testing.T) {
	h := newHub()
	sub, err := h.subscribe(0, "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Fill the queue and keep publishing: the newest frame must survive, and the
	// publisher must never block.
	for i := 0; i < subQueue+20; i++ {
		h.broadcast([]byte(fmt.Sprintf("frame-%d", i)))
	}
	if len(sub.ch) != subQueue {
		t.Fatalf("queue length %d, want the cap %d", len(sub.ch), subQueue)
	}
	last := ""
	for len(sub.ch) > 0 {
		last = string(<-sub.ch)
	}
	if last != fmt.Sprintf("frame-%d", subQueue+19) {
		t.Fatalf("newest frame = %q, want the last one published", last)
	}
	if got := sub.dropped.Load(); got < 20 {
		t.Fatalf("dropped %d frames, want at least 20", got)
	}
}

// TestStreamDrainsOnShutdown: a live stream returns when the server says goodbye.
func TestStreamDrainsOnShutdown(t *testing.T) {
	host, _ := startedRoom(t)
	sr, _, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	host.store.CloseAllStreams("shutdown")
	// Presence frames are coalesced and may still be in flight; the stream ends with
	// a bye and then closes.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case frame, ok := <-sr.frames:
			if !ok {
				sr.cancel()
				return
			}
			if frame["_event"] == "bye" {
				if frame["reason"] != "shutdown" {
					t.Fatalf("bye reason = %v, want shutdown", frame["reason"])
				}
				sr.cancel()
				return
			}
		case <-deadline:
			t.Fatal("the stream did not end on shutdown")
		}
	}
}

// TestNoGoroutineLeakAfterCycles measures the leak rather than eyeballing it: twenty
// connect/disconnect cycles may leave at most five goroutines behind.
func TestNoGoroutineLeakAfterCycles(t *testing.T) {
	host, _ := startedRoom(t)
	// One warm-up stream so lazily started goroutines (the hub ticker) already exist.
	warm, _, err := host.openStream(host.seatHeader())
	if err != nil {
		t.Fatalf("warm-up stream: %v", err)
	}
	warm.cancel()
	time.Sleep(150 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		sr, _, err := host.openStream(host.seatHeader())
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		sr.cancel()
		sr.resp.Body.Close()
	}
	// Give the handlers a moment to notice their context is done.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > baseline+5 {
		t.Fatalf("goroutines %d -> %d after 20 stream cycles (allowed +5)", baseline, after)
	}
}

func TestCursorRequiresSeat(t *testing.T) {
	host, _ := startedRoom(t)
	for _, path := range []string{"/api/rooms/" + host.code + "/cursor"} {
		req := mustRequest(t, host.srv.URL+path, map[string]string{"Content-Type": "application/json"})
		req.Method = http.MethodPost
		req.Body = io.NopCloser(strings.NewReader(`{"x":0.5,"y":0.5}`))
		resp, err := host.srv.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a seat: %d, want 401", path, resp.StatusCode)
		}
	}
	// With the seat it is accepted and rate-limited like the watcher relay.
	resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cursor", map[string]any{"x": 0.5, "y": 0.5}, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("seat cursor: %d", resp.StatusCode)
	}
}

// mustRequest builds a GET with headers, for the cases the client helper cannot express.
func mustRequest(t *testing.T, url string, headers map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// ---- the hub's send discipline (F-35) -------------------------------------

// TestPublishNeverBlocksOnADeadStream: a subscriber that stops reading must not be
// able to wedge a publisher. Before this, Publish did a plain channel send, so one
// stuck socket filled its 32-slot queue and every later action for that room hung.
func TestPublishNeverBlocksOnADeadStream(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	h := host.store.hubForLocked(sess)
	sess.mu.Unlock()

	sub, err := h.subscribe(0, "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Nobody ever drains this queue.
	for i := 0; i < subQueue; i++ {
		sub.ch <- []byte("fill")
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			host.store.Publish(sess, "")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

// TestPublishAfterCloseDoesNotPanic: close used to close every subscriber channel
// while Publish could be mid-send, which is a send-on-closed-channel panic in the hub
// goroutine — that kills the process, and the frozen solo game with it.
func TestPublishAfterCloseDoesNotPanic(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	h := host.store.hubForLocked(sess)
	sess.mu.Unlock()
	if _, err := h.subscribe(0, ""); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Publish and close racing, repeatedly: the assertion is that nothing panics.
	for i := 0; i < 50; i++ {
		if i%2 == 0 {
			go host.store.Publish(sess, "")
		} else {
			host.store.CloseStreams(sess, "cancelled")
			sess.mu.Lock()
			h = host.store.hubForLocked(sess) // a fresh hub for the next round
			sess.mu.Unlock()
			if _, err := h.subscribe(0, ""); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
		}
	}
	host.store.Publish(sess, "")
}

// TestStreamIsTheHeartbeat: an open stream keeps its seat present and keeps a
// spectator's watch id alive. This is D-41's promise, and without it a client that
// never polls is marked away after 90 s, the host is offered its seat, and a
// spectator's board freezes once its watch id expires.
//
// The assertion is on the running hub's ticker, not on the helper: the tick is what
// has to call it.
func TestStreamIsTheHeartbeat(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	// The clock is atomic because the hub goroutine reads it while the test advances
	// it: assigning host.store.now twice is itself a data race.
	var nowNs atomic.Int64
	nowNs.Store(time.Unix(1_700_000_000, 0).UnixNano())
	host.store.now = func() time.Time { return time.Unix(0, nowNs.Load()) }
	base := time.Unix(1_700_000_000, 0)

	sess.mu.Lock()
	h := host.store.hubForLocked(sess)
	sess.mu.Unlock()
	if _, err := h.subscribe(0, ""); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	watch := "watch-heartbeat"
	sess.mu.Lock()
	if sess.watchers == nil {
		sess.watchers = map[string]*Watcher{}
	}
	sess.watchers[watch] = &Watcher{ID: watch, JoinedAt: base, LastSeen: base}
	sess.mu.Unlock()
	if _, err := h.subscribe(-1, watch); err != nil {
		t.Fatalf("subscribe watcher: %v", err)
	}

	// Ten minutes pass on the server's clock while both streams stay open. The hub
	// ticks every 100 ms, so two ticks are enough to prove it is refreshing.
	nowNs.Store(base.Add(10 * time.Minute).UnixNano())
	time.Sleep(250 * time.Millisecond)

	if _, away, _ := host.store.IdleFor(sess, 0); away {
		t.Error("a seat with an open stream was reported away")
	}
	if !host.store.WatcherAuth(sess, watch) {
		t.Error("a spectator with an open stream lost its watch id to the TTL")
	}
}

// TestWatcherStreamNeverLeaksAHand: a spectator's subscriber used to default to
// "look through seat 0", so in an open-hands room the welcome frame showed the table and
// every frame after it showed the HOST'S HAND. The subscriber's view must start at the
// table (-1) and only move when it is asked to.
func TestWatcherStreamNeverLeaksAHand(t *testing.T) {
	host, _ := startedRoom(t)
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": true, "hands": true}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("open hands: %d %v", resp.StatusCode, out)
	}
	watch, err := host.store.AddWatcher(mustSession(t, host))
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	sr, _, err := host.openStream(map[string]string{"X-Regicide-Watch": watch})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.cancel()

	// Any action produces a published frame; the spectator must not see a hand in it.
	state := host.get("/api/rooms/" + host.code)
	legal := state["state"].(map[string]any)["legal"].([]any)
	choice := legal[0].(map[string]any)
	action := map[string]any{"type": choice["type"]}
	if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
		action["cardIds"] = ids
	}
	if resp, out := host.act(action); resp.StatusCode != http.StatusOK {
		t.Fatalf("action: %d %v", resp.StatusCode, out)
	}

	frame := sr.next(t, "state", 4*time.Second)
	st, _ := frame["state"].(map[string]any)
	if st == nil {
		t.Fatalf("frame had no state: %v", frame)
	}
	if hand, ok := st["hand"].([]any); ok && len(hand) > 0 {
		t.Fatalf("a spectator received %d cards: %v", len(hand), hand)
	}
	if seat, _ := st["viewerSeat"].(float64); seat != -1 {
		t.Fatalf("spectator frame projected for seat %v, want the table (-1)", seat)
	}
}

// TestHubCapsStreamsPerRoom pins the resource bound: watcher ids are capped, but the
// connections were not, so one id could hold thousands of ten-minute streams.
func TestHubCapsStreamsPerRoom(t *testing.T) {
	h := newHub()
	for i := 0; i < maxStreamsPerRoom; i++ {
		if _, err := h.subscribe(0, ""); err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
	}
	if _, err := h.subscribe(0, ""); !errors.Is(err, errStreamFull) {
		t.Fatalf("subscribe past the cap = %v, want errStreamFull", err)
	}
}

// TestStreamRefusesPastTheRoomCap drives the real route: the per-room stream budget
// is a resource bound, and the caller must learn it before any event bytes arrive.
func TestStreamRefusesPastTheRoomCap(t *testing.T) {
	host, _ := startedRoom(t)
	url := host.srv.URL + "/api/rooms/" + host.code + "/stream"
	header := host.seatHeader()
	client := &http.Client{Timeout: 10 * time.Second}
	open := func() *http.Response {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("stream request: %v", err)
		}
		return res
	}
	bodies := make([]io.ReadCloser, 0, maxStreamsPerRoom)
	defer func() {
		for _, b := range bodies {
			b.Close()
		}
	}()
	for i := 0; i < maxStreamsPerRoom; i++ {
		res := open()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %d", i, res.StatusCode)
		}
		bodies = append(bodies, res.Body)
	}
	res := open()
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("stream %d = %d, want 429", maxStreamsPerRoom+1, res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Fatalf("429 without Retry-After")
	}
}

// TestStreamOnAClosedHubSaysGoodbye pins the cancelled-room contract: the tab gets a
// 200 stream whose first frame says goodbye, which it shows as the closed table. A
// bare error status made the client retry for ever.
func TestStreamOnAClosedHubSaysGoodbye(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	h := host.store.hubForLocked(sess)
	sess.mu.Unlock()
	// Leave sess.hub pointing at the closed hub: the window a parallel cancel opens.
	h.close("shutdown")

	req, err := http.NewRequest(http.MethodGet, host.srv.URL+"/api/rooms/"+host.code+"/stream", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range host.seatHeader() {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("closed hub answered %d, want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	if !strings.Contains(string(body), "event: bye") {
		t.Fatalf("first frame was not a goodbye: %q", string(body))
	}
}
