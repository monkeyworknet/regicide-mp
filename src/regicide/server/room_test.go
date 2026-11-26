package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"regicide/bot"
	"regicide/engine"
	"regicide/names"
	"regicide/replay"
)

// ---- harness --------------------------------------------------------------

type roomClient struct {
	t     *testing.T
	srv   *httptest.Server
	store *Store
	token string
	seat  int
	code  string
}

func newRoomClient(t *testing.T) *roomClient {
	t.Helper()
	srv, err := New(Options{DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// Writes are queued; drain them before the temp directory goes away.
	t.Cleanup(srv.FlushWrites)
	return &roomClient{t: t, srv: ts, store: srv.store}
}

func (c *roomClient) do(method, path string, body any, withSeat bool) (*http.Response, map[string]any) {
	c.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.srv.URL+path, rdr)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withSeat && c.token != "" {
		req.Header.Set("X-Regicide-Seat", fmt.Sprintf("%d:%s", c.seat, c.token))
	}
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (c *roomClient) createRoom(theme string) map[string]any {
	c.t.Helper()
	resp, out := c.do(http.MethodPost, "/api/rooms", map[string]any{
		"theme": theme,
	}, false)
	if resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("create room: %d %v", resp.StatusCode, out)
	}
	c.code = out["code"].(string)
	c.seat = int(out["seat"].(float64))
	c.token = out["token"].(string)
	return out
}

func (c *roomClient) join() map[string]any {
	c.t.Helper()
	resp, out := c.do(http.MethodPost, "/api/rooms/"+c.code+"/join", nil, false)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("join: %d %v", resp.StatusCode, out)
	}
	c.seat = int(out["seat"].(float64))
	c.token = out["token"].(string)
	return out
}

// get fetches a room and returns the decoded body, failing on any non-200.
func (c *roomClient) get(path string) map[string]any {
	c.t.Helper()
	resp, out := c.do(http.MethodGet, path, nil, true)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s: %d %v", path, resp.StatusCode, out)
	}
	return out
}

// act posts one action with the sequence the client last saw. A room action must say
// which view it is based on (F-24), so the helper reads it from the room first.
func (c *roomClient) act(action map[string]any) (*http.Response, map[string]any) {
	c.t.Helper()
	seq := 0
	if body, ok := c.lastRoom(); ok {
		seq = int(body["seq"].(float64))
	}
	return c.do(http.MethodPost, "/api/rooms/"+c.code+"/actions", map[string]any{
		"actionId":    fmt.Sprintf("%d-%v-%d", c.seat, action["type"], time.Now().UnixNano()),
		"expectedSeq": seq,
		"action":      action,
	}, true)
}

// lastRoom reads the client's current room view, ignoring errors.
func (c *roomClient) lastRoom() (map[string]any, bool) {
	c.t.Helper()
	resp, out := c.do(http.MethodGet, "/api/rooms/"+c.code, nil, true)
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	return out, true
}

// start deals the room with the sequence this client last saw. The server requires
// one on /start (T5), exactly as it does on every other room write.
func (c *roomClient) start() (*http.Response, map[string]any) {
	c.t.Helper()
	seq := 0
	if body, ok := c.lastRoom(); ok {
		seq = int(body["seq"].(float64))
	}
	return c.do(http.MethodPost, "/api/rooms/"+c.code+"/start", map[string]any{"expectedSeq": seq}, true)
}

// again re-deals a finished room with the sequence this client last saw.
func (c *roomClient) again() (*http.Response, map[string]any) {
	c.t.Helper()
	seq := 0
	if body, ok := c.lastRoom(); ok {
		seq = int(body["seq"].(float64))
	}
	return c.do(http.MethodPost, "/api/rooms/"+c.code+"/again", map[string]any{"expectedSeq": seq}, true)
}

// ---- discovery and naming -----------------------------------------------

func TestRoomListShowsOpenTables(t *testing.T) {
	host := newRoomClient(t)
	created := host.createRoom("simple")
	if created["lobby"] != true {
		t.Error("a new room must be in the lobby")
	}
	room := created["room"].(map[string]any)
	if room["code"] != host.code {
		t.Errorf("room code = %v, want %s", room["code"], host.code)
	}
	seats := room["seats"].([]any)
	if len(seats) != 1 {
		t.Fatalf("host room has %d seats, want 1", len(seats))
	}
	if name := seats[0].(map[string]any)["name"].(string); name == "" {
		t.Error("the host must be given a name")
	}

	// Someone else opens the page: the table is listed, with its seat count.
	resp, out := host.do(http.MethodGet, "/api/rooms", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	rooms := out["rooms"].([]any)
	if len(rooms) != 1 {
		t.Fatalf("listed %d rooms, want 1", len(rooms))
	}
	row := rooms[0].(map[string]any)
	if row["code"] != host.code || int(row["seats"].(float64)) != 1 || int(row["capacity"].(float64)) != 4 {
		t.Errorf("listing row = %v", row)
	}
	if row["phase"] != "lobby" {
		t.Errorf("listing row = %v", row)
	}
}

func TestRoomNamesAreDistinctAndRoyal(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("8bit")
	assigned := []string{}
	_, first := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true)
	room := first["room"].(map[string]any)
	assigned = append(assigned, room["seats"].([]any)[0].(map[string]any)["name"].(string))

	for i := 0; i < 3; i++ {
		other := *host
		other.join()
		_, listing := other.do(http.MethodGet, "/api/rooms/"+other.code, nil, true)
		for _, raw := range listing["room"].(map[string]any)["seats"].([]any) {
			seat := raw.(map[string]any)
			if seat["you"] == true {
				assigned = append(assigned, seat["name"].(string))
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range assigned {
		if seen[name] {
			t.Errorf("%q was handed out twice", name)
		}
		seen[name] = true
		if !royalName(name) {
			t.Errorf("%q is not a royal name from the pool", name)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("%d distinct names for 4 seats", len(seen))
	}
}

// royalName checks the name really came from the pool, not from the socket.
func royalName(name string) bool { return names.Valid(name) }

// ---- co-op theme allow-list ---------------------------------------------

func TestRoomAcceptsPokemonAndRejectsUnregisteredSets(t *testing.T) {
	c := newRoomClient(t)
	// Pokemon is allowed at a table now; the lobby warns about the detail instead of
	// the server refusing it (D-86).
	if resp, out := c.do(http.MethodPost, "/api/rooms", map[string]any{"theme": "pokemon"}, false); resp.StatusCode != http.StatusCreated {
		t.Errorf("pokemon room: %d %v, want 201", resp.StatusCode, out)
	}
	// An empty theme still defaults; a set that is not in the registry is refused.
	for _, theme := range []string{"", "not-a-theme"} {
		resp, out := c.do(http.MethodPost, "/api/rooms", map[string]any{"theme": theme}, false)
		if theme == "" {
			if resp.StatusCode != http.StatusCreated {
				t.Errorf("empty theme should default, got %d %v", resp.StatusCode, out)
			}
			continue
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", theme, resp.StatusCode)
		}
		if out["error"] != "coop_theme" {
			t.Errorf("%s: error = %v", theme, out["error"])
		}
	}
	// Solo in the same binary is untouched.
	resp, out := c.do(http.MethodPost, "/api/sessions", map[string]any{"theme": "pokemon"}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("solo pokemon: %d %v", resp.StatusCode, out)
	}
}

// ---- seats and turns -----------------------------------------------------

func TestRoomWaitsForPlayersAndDealsForTheRealCount(t *testing.T) {
	host := newRoomClient(t)
	created := host.createRoom("simple")
	room := created["room"].(map[string]any)
	if int(room["capacity"].(float64)) != 4 {
		t.Errorf("capacity = %v, want 4 (a waiting room, not a promise)", room["capacity"])
	}
	if room["startable"] == true {
		t.Error("a room with one player reported startable")
	}

	// The setup table follows the seats actually taken, and two seats is already a
	// table.
	check := func(players, maxHand, jesters int, startable bool) {
		t.Helper()
		body := host.get("/api/rooms/" + host.code)
		room := body["room"].(map[string]any)
		next := room["nextRules"].(map[string]any)
		if int(room["players"].(float64)) != players {
			t.Fatalf("players = %v, want %d", room["players"], players)
		}
		if int(next["maxHand"].(float64)) != maxHand || int(next["jestersInTavern"].(float64)) != jesters {
			t.Fatalf("%d players: hand %v jesters %v, want %d/%d",
				players, next["maxHand"], next["jestersInTavern"], maxHand, jesters)
		}
		if room["startable"] != startable {
			t.Fatalf("%d players: startable = %v, want %v", players, room["startable"], startable)
		}
	}
	check(1, 8, 0, false)

	two := *host
	two.join()
	check(2, 7, 0, true)

	three := *host
	three.join()
	check(3, 6, 1, true)

	four := *host
	four.join()
	check(4, 5, 2, true)

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/join", nil, false)
	if resp.StatusCode != http.StatusConflict || out["error"] != "room_full" {
		t.Fatalf("fifth join: %d %v, want 409 room_full", resp.StatusCode, out)
	}

	// A seat that asks to hold the table blocks the start; letting go releases it.
	if resp, o := four.do(http.MethodPost, "/api/rooms/"+four.code+"/ready", map[string]any{"ready": false}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("hold: %d %v", resp.StatusCode, o)
	}
	check(4, 5, 2, false)
	if resp, out := host.start(); resp.StatusCode != http.StatusConflict {
		t.Fatalf("start while a seat holds: %d %v", resp.StatusCode, out)
	}
	four.do(http.MethodPost, "/api/rooms/"+four.code+"/ready", map[string]any{"ready": true}, true)

	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	state := host.get("/api/rooms/" + host.code)["state"].(map[string]any)
	if int(state["numPlayers"].(float64)) != 4 {
		t.Errorf("numPlayers = %v, want 4", state["numPlayers"])
	}
	if int(state["maxHand"].(float64)) != 5 {
		t.Errorf("maxHand = %v, want 5 for four seats", state["maxHand"])
	}
}

func TestRoomAwaySeatIsMarkedAndCanBeTakenOver(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	// Bot fill would hand the silent seat to a bot at 60 s, and a bot seat is free at
	// once: the away/takeover window only governs a seat that is still a human's.
	setBotFill(t, host, false)
	guest := *host
	guest.join()
	guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ready", map[string]any{"ready": true}, true)
	host.start()
	guestName := guest.get("/api/rooms/" + guest.code)["room"].(map[string]any)["seats"].([]any)[1].(map[string]any)["name"].(string)

	// Move the clock, not the network: a seat is away after 90s and takeable after 5m.
	base := time.Now()
	host.store.now = func() time.Time { return base.Add(2 * time.Minute) }
	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	awaySeat := room["seats"].([]any)[1].(map[string]any)
	if awaySeat["away"] != true {
		t.Error("a seat silent for two minutes should be away")
	}
	if awaySeat["takeable"] == true {
		t.Error("two minutes is too early to take the seat")
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/take", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "seat_not_away" {
		t.Fatalf("early takeover: %d %v, want 409 seat_not_away", resp.StatusCode, out)
	}

	host.store.now = func() time.Time { return base.Add(6 * time.Minute) }
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/take", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("takeover: %d %v", resp.StatusCode, out)
	}
	fresh := out["token"].(string)
	if fresh == "" || fresh == guest.token {
		t.Fatal("takeover must mint a new token")
	}

	// The old device is out, the new one is in, and the seat keeps its name and hand.
	if resp, _ := guest.do(http.MethodGet, "/api/rooms/"+guest.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token still works: %d", resp.StatusCode)
	}
	taken := &roomClient{t: t, srv: host.srv, store: host.store, code: host.code, seat: 1, token: fresh}
	body := taken.get("/api/rooms/" + taken.code)
	name := body["room"].(map[string]any)["seats"].([]any)[1].(map[string]any)["name"].(string)
	if name != guestName {
		t.Errorf("seat name changed during takeover: %s -> %s", guestName, name)
	}
	if hand := body["state"].(map[string]any)["hand"].([]any); len(hand) == 0 {
		t.Error("the taken seat lost its hand")
	}
}

func TestRoomRejectsUnknownAndBadTokens(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	if resp, _ := host.do(http.MethodGet, "/api/rooms/NOPE1", nil, false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown code: %d, want 404", resp.StatusCode)
	}
	good := host.token
	host.token = "deadbeef"
	if resp, out := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: %d %v, want 401", resp.StatusCode, out)
	}
	host.token = good
	if resp, _ := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true); resp.StatusCode != http.StatusOK {
		t.Errorf("good token: %d, want 200", resp.StatusCode)
	}
}

// TestRoomStartRequiresASequence: a deal is conditional on the lobby the host saw,
// like every other room write. Without a sequence a join racing the deal could land
// a seat the host's screen never showed (T5).
func TestRoomStartRequiresASequence(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/start", nil, true); resp.StatusCode != http.StatusBadRequest || out["error"] != "expected_seq_required" {
		t.Fatalf("start without a sequence: %d %v, want 400 expected_seq_required", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/start", map[string]any{"expectedSeq": -1}, true); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("start with a negative sequence: %d %v, want 400", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/start", map[string]any{"expectedSeq": 99}, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("start with a stale sequence: %d %v, want 409", resp.StatusCode, out)
	}
	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start with the current sequence: %d %v", resp.StatusCode, out)
	}
}

func TestRoomStartNeedsTwoSeats(t *testing.T) {
	// One seat alone cannot start; two can, and once started the lobby closes.
	solo := newRoomClient(t)
	solo.createRoom("simple")
	if resp, out := solo.start(); resp.StatusCode != http.StatusConflict {
		t.Fatalf("a one-seat room started: %d %v", resp.StatusCode, out)
	}

	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start with two seats: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/join", nil, false); resp.StatusCode != http.StatusConflict {
		t.Errorf("join after start: %d %v", resp.StatusCode, out)
	}
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ready", map[string]any{"ready": false}, true); resp.StatusCode != http.StatusConflict {
		t.Errorf("ready after start: %d %v", resp.StatusCode, out)
	}
}

func TestRoomOnlyTheActiveSeatMayAct(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ready", map[string]any{"ready": true}, true)
	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}

	state := host.get("/api/rooms/" + host.code)["state"].(map[string]any)
	hand := state["hand"].([]any)
	if len(hand) == 0 {
		t.Fatal("the host has no cards after the deal")
	}
	cardID := hand[0].(map[string]any)["id"].(string)

	// The guest may not play out of turn, and the refusal carries their own state.
	resp, out := guest.act(map[string]any{"type": "play", "cardIds": []string{cardID}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("out-of-turn play: %d %v", resp.StatusCode, out)
	}
	if out["state"] == nil {
		t.Error("a 403 must carry the caller's state so the client can resync")
	}

	// The active seat can, and the guest sees it.
	if resp, out := host.act(map[string]any{"type": "play", "cardIds": []string{cardID}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("host play: %d %v", resp.StatusCode, out)
	}
	after := guest.get("/api/rooms/" + guest.code)["state"].(map[string]any)
	if after["phase"] != state["phase"] || after["seq"] == nil {
		t.Logf("guest phase %v (was %v)", after["phase"], state["phase"])
	}
	if rows := after["players"].([]any); len(rows) != 2 {
		t.Fatalf("guest seat rail has %d rows, want 2", len(rows))
	}
	// …and never the host's hand.
	for _, raw := range after["players"].([]any) {
		row := raw.(map[string]any)
		if row["seat"].(float64) == 0 {
			if row["hand"] != nil {
				t.Error("the guest can see the host's hand")
			}
		}
	}
}

func TestRoomStaleSeqAndIdempotentRetry(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ready", map[string]any{"ready": true}, true)
	host.start()

	state := host.get("/api/rooms/" + host.code)
	seq := int(state["seq"].(float64))
	cardID := state["state"].(map[string]any)["hand"].([]any)[0].(map[string]any)["id"].(string)

	// A stale expectedSeq is refused with the current state attached.
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", map[string]any{
		"actionId": "stale-1", "expectedSeq": seq - 5,
		"action": map[string]any{"type": "play", "cardIds": []string{cardID}},
	}, true)
	if resp.StatusCode != http.StatusConflict || out["state"] == nil {
		t.Fatalf("stale: %d %v", resp.StatusCode, out)
	}

	// A negative expectation must not quietly disable the check.
	resp, out = host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", map[string]any{
		"actionId": "negative-1", "expectedSeq": -9,
		"action": map[string]any{"type": "play", "cardIds": []string{cardID}},
	}, true)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("negative expectedSeq: %d %v, want 409", resp.StatusCode, out)
	}

	// A retry of the same action id returns the first answer, not a second action.
	first := map[string]any{"actionId": "retry-1", "expectedSeq": seq,
		"action": map[string]any{"type": "play", "cardIds": []string{cardID}}}
	r1, o1 := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", first, true)
	r2, o2 := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", first, true)
	if r1.StatusCode != http.StatusOK || r2.StatusCode != http.StatusOK {
		t.Fatalf("retry statuses: %d %d (%v %v)", r1.StatusCode, r2.StatusCode, o1, o2)
	}
	if o1["seq"] != o2["seq"] {
		t.Errorf("retry moved the sequence: %v then %v", o1["seq"], o2["seq"])
	}

	// A different action id with the same stale expectation is refused.
	again := map[string]any{"actionId": "fresh-1", "expectedSeq": seq,
		"action": map[string]any{"type": "play", "cardIds": []string{cardID}}}
	if resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", again, true); resp.StatusCode != http.StatusConflict {
		t.Errorf("second play on a spent seq: %d, want 409", resp.StatusCode)
	}
}

func TestRoomTokenIsNeverStored(t *testing.T) {
	host := newRoomClient(t)
	created := host.createRoom("simple")
	token := created["token"].(string)

	// Reading the room back must not return the token again, nor any hash of it.
	_, out := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true)
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), token) {
		t.Fatal("the raw seat token came back in a room response")
	}
	if strings.Contains(string(body), hashToken(token)) {
		t.Fatal("the token hash came back in a room response")
	}
	if strings.Contains(string(body), "tokenHash") {
		t.Fatal("a token hash field reached the client")
	}
}

func TestRoomCoopGameRunsAcrossSeats(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ready", map[string]any{"ready": true}, true)
	host.start()

	// Play whoever's turn it is until the game ends. This is the API-only acceptance
	// run: two seats, real actions, one authority. The old 40-action cap let the test
	// pass without ever reaching a terminal phase (F-23); the cap is now a generous
	// bound whose only acceptable outcome is won or lost.
	clients := []*roomClient{host, &guest}
	actions := 0
	for actions < 400 {
		var actor *roomClient
		var state map[string]any
		for _, c := range clients {
			s := c.get("/api/rooms/" + c.code)["state"].(map[string]any)
			if phase := s["phase"].(string); phase == "won" || phase == "lost" {
				// A win or a loss both mean the table ran: the point of this test is
				// that two seats drive one table through the real API. Before action
				// ids were unique this loop replayed cached answers and never moved,
				// so "reached a terminal phase" is the assertion worth making (F-23).
				if actions < 5 {
					t.Fatalf("the table ended after %d actions: %v", actions, s["lostReason"])
				}
				return
			}
			if legal, _ := s["legal"].([]any); len(legal) > 0 {
				actor, state = c, s
				break
			}
		}
		if actor == nil {
			t.Fatalf("no seat had a legal action at action %d", actions)
		}
		legal := state["legal"].([]any)
		choice := legal[0].(map[string]any)
		action := map[string]any{"type": choice["type"]}
		if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
			action["cardIds"] = ids
		}
		if seat, ok := choice["seat"]; ok {
			action["seat"] = seat
		}
		resp, out := actor.act(action)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("action %v by seat %d: %d %v", action, actor.seat, resp.StatusCode, out)
		}
		actions++
	}
	t.Fatalf("the table did not reach won/lost within %d actions", actions)
}

func TestRoomLeaderboardStaysSolo(t *testing.T) {
	c := newRoomClient(t)
	// A co-op session must never be submittable: it has no session url in the solo
	// sense, and the board rejects a multi-seat game explicitly.
	resp, out := c.do(http.MethodPost, "/api/leaderboard", map[string]any{
		"sessionId": "unknown", "name": "someone",
	}, false)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a submit with no solo session was accepted: %v", out)
	}
}

func TestHostCanOfferAReconnectLink(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	// Only the host may mint a link, and never for their own seat.
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/seats/1/invite", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Fatalf("guest minted a link: %d %v", resp.StatusCode, out)
	}
	if resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/0/invite", nil, true); resp.StatusCode != http.StatusConflict {
		t.Errorf("the host minted a link for their own seat: %d", resp.StatusCode)
	}

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/invite", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invite: %d %v", resp.StatusCode, out)
	}
	link := out["link"].(string)
	if !strings.Contains(link, "room="+host.code) || !strings.Contains(link, "seat=1") || !strings.Contains(link, "rejoin=") {
		t.Fatalf("link = %q", link)
	}
	key := link[strings.Index(link, "rejoin=")+len("rejoin="):]

	// The room view tells the host a link is live, and never repeats the key.
	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	seat1 := room["seats"].([]any)[1].(map[string]any)
	if seat1["rejoinLive"] != true {
		t.Errorf("rejoinLive = %v, want true", seat1["rejoinLive"])
	}
	if body, _ := json.Marshal(room); strings.Contains(string(body), key) {
		t.Fatal("the room view leaked the rejoin key")
	}

	// The link replaces the guest's token with a new one; the old token dies.
	oldToken := guest.token
	resp, out = host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/reclaim", map[string]any{"key": key}, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reclaim: %d %v", resp.StatusCode, out)
	}
	fresh := out["token"].(string)
	if fresh == "" || fresh == oldToken {
		t.Fatal("reclaim must mint a new token")
	}
	if resp, _ := guest.do(http.MethodGet, "/api/rooms/"+guest.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old token still works: %d", resp.StatusCode)
	}
	rejoined := &roomClient{t: t, srv: host.srv, store: host.store, code: host.code, seat: 1, token: fresh}
	if hand := rejoined.get("/api/rooms/" + rejoined.code)["state"].(map[string]any)["hand"].([]any); len(hand) == 0 {
		t.Error("the reclaimed seat lost its hand")
	}

	// A used key is spent: the same link cannot take the seat twice.
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/reclaim", map[string]any{"key": key}, true); resp.StatusCode != http.StatusConflict || out["error"] != "bad_rejoin_key" {
		t.Fatalf("replaying the key: %d %v", resp.StatusCode, out)
	}
}

func TestReconnectLinkExpires(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	_, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/invite", nil, true)
	key := out["link"].(string)
	key = key[strings.Index(key, "rejoin=")+len("rejoin="):]

	base := time.Now()
	host.store.now = func() time.Time { return base.Add(11 * time.Minute) }
	resp, body := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/reclaim", map[string]any{"key": key}, false)
	if resp.StatusCode != http.StatusConflict || body["error"] != "bad_rejoin_key" {
		t.Fatalf("expired key: %d %v, want 409 bad_rejoin_key", resp.StatusCode, body)
	}

	// A takeover also kills a live link: one owner at a time.
	host.store.now = func() time.Time { return base }
	_, out = host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/invite", nil, true)
	link := out["link"].(string)
	key = link[strings.Index(link, "rejoin=")+len("rejoin="):]
	host.store.now = func() time.Time { return base.Add(6 * time.Minute) }
	if resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/take", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("takeover: %d", resp.StatusCode)
	}
	if resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/reclaim", map[string]any{"key": key}, false); resp.StatusCode != http.StatusConflict {
		t.Fatalf("a link survived a takeover: %d", resp.StatusCode)
	}
}

func TestHostCanCancelTheTable(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/cancel", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Fatalf("guest cancelled the table: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}

	// Everyone can still read the room, and reads the reason.
	body := guest.get("/api/rooms/" + guest.code)
	if body["room"].(map[string]any)["cancelled"] != true {
		t.Error("the other seat did not see the table cancelled")
	}
	// No seat may act in a cancelled game.
	state := body["state"].(map[string]any)
	hand := state["hand"].([]any)
	if len(hand) > 0 {
		card := hand[0].(map[string]any)["id"].(string)
		resp, out := host.act(map[string]any{"type": "play", "cardIds": []string{card}})
		if resp.StatusCode != http.StatusConflict || out["error"] != "game_cancelled" {
			t.Fatalf("action in a cancelled game: %d %v", resp.StatusCode, out)
		}
	}
}

// TestWatcherExpires closes F-4/F-23: a spectator slot is a budget, so a silent tab
// must lose it. The clock is injected, so this is deterministic and instant.
func TestWatcherExpires(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	host.store.now = func() time.Time { return now }

	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	id, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	if !host.store.WatcherAuth(sess, id) {
		t.Fatal("a fresh watcher is not live")
	}
	// Past the TTL the slot is gone, and the count says so.
	now = now.Add(61 * time.Second)
	if host.store.WatcherAuth(sess, id) {
		t.Fatal("a watcher outlived the TTL")
	}
	if n := host.store.WatcherCount(sess); n != 0 {
		t.Fatalf("watcher count = %d after expiry, want 0", n)
	}
	// And the slot is reusable: an expired watcher does not keep the eighth seat.
	again, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("the slot was not freed: %v", err)
	}
	if !host.store.WatcherAuth(sess, again) {
		t.Fatal("the new watcher is not live")
	}
}

// TestRoomConcurrentJoinSameSeat closes F-23's missing concurrency test: eight joins at
// once must hand out eight different seats or be refused, never the same seat twice.
func TestRoomConcurrentJoinSameSeat(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")

	type result struct {
		seat  int
		token string
		err   error
	}
	const attempts = 8
	results := make(chan result, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			seat, token, err := host.store.JoinRoom(mustSession(t, host))
			results <- result{seat: seat, token: token, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	seats := map[int]int{}
	tokens := map[string]bool{}
	joined, refused := 0, 0
	for r := range results {
		if r.err != nil {
			refused++
			continue
		}
		joined++
		seats[r.seat]++
		if tokens[r.token] {
			t.Fatalf("the same token was handed out twice")
		}
		tokens[r.token] = true
	}
	for seat, n := range seats {
		if n > 1 {
			t.Fatalf("seat %d was handed out %d times", seat, n)
		}
	}
	if joined != roomCapacity-1 {
		t.Fatalf("%d joins succeeded, want %d (host + capacity)", joined, roomCapacity-1)
	}
	if refused != attempts-joined {
		t.Fatalf("%d refused, want %d", refused, attempts-joined)
	}
}

func mustSession(t *testing.T, c *roomClient) *Session {
	t.Helper()
	sess, ok := c.store.ByCode(c.code)
	if !ok {
		t.Fatalf("room %s missing", c.code)
	}
	return sess
}

func TestWaitingForNamesTheAwaySeat(t *testing.T) {
	host, _ := startedRoom(t)
	// The deal starts on the host, and the host's own GET refreshes the host's seat. The
	// check under test is about the seat that must act, so make the guest that seat
	// instead of depending on whose turn the deal happened to give (F-23).
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.Game.ActiveSeat = 1
	sess.mu.Unlock()

	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	if name := room["waitingFor"]; name != nil && name != "" {
		t.Fatalf("waitingFor = %v before anyone went quiet", name)
	}
	guestName := room["seats"].([]any)[1].(map[string]any)["name"].(string)

	// The seat whose turn it is goes quiet: only then is the table stopped, by name.
	base := time.Now()
	host.store.now = func() time.Time { return base.Add(2 * time.Minute) }
	room = host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	if room["waitingFor"] != guestName {
		t.Fatalf("waitingFor = %v, want %s", room["waitingFor"], guestName)
	}
	// waitingLeft is omitted when false.
	if left, ok := room["waitingLeft"].(bool); ok && left {
		t.Errorf("waitingLeft = %v for a seat that only went quiet", room["waitingLeft"])
	}

	// A seat that chose to leave is named with the other label.
	sess.mu.Lock()
	sess.Room.Seats[1].Left = true
	sess.Room.Seats[1].Bot = false
	sess.mu.Unlock()
	room = host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	if room["waitingFor"] != guestName || room["waitingLeft"] != true {
		t.Errorf("waitingFor = %v/%v, want %s/true for a left seat", room["waitingFor"], room["waitingLeft"], guestName)
	}
}

// ---- spectators ----------------------------------------------------------

type watcher struct {
	id string
}

func (c *roomClient) watch() (map[string]any, *watcher) {
	c.t.Helper()
	resp, out := c.do(http.MethodPost, "/api/rooms/"+c.code+"/watch", nil, false)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("watch: %d %v", resp.StatusCode, out)
	}
	return out, &watcher{id: out["watchId"].(string)}
}

func (c *roomClient) watchGet(w *watcher) (*http.Response, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.srv.URL+"/api/rooms/"+c.code+"/watch", nil)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-Regicide-Watch", w.id)
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestSpectatorSeesTheTableAndNoHands(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	joined, w := host.watch()
	room := joined["room"].(map[string]any)
	if room["allowWatch"] != true {
		t.Error("watching should be on by default")
	}
	if room["watchLink"] != "/?room="+host.code+"&watch=1" {
		t.Errorf("watchLink = %v", room["watchLink"])
	}
	if int(room["watchers"].(float64)) != 1 {
		t.Errorf("watchers = %v, want 1", room["watchers"])
	}

	state := joined["state"].(map[string]any)
	if hand := state["hand"].([]any); len(hand) != 0 {
		t.Fatalf("a spectator got a hand of %d cards", len(hand))
	}
	if legal := state["legal"].([]any); len(legal) != 0 {
		t.Errorf("a spectator got %d legal actions", len(legal))
	}
	if int(state["viewerSeat"].(float64)) != -1 {
		t.Errorf("viewerSeat = %v, want -1", state["viewerSeat"])
	}
	// The seat rail is public: counts and the limit are exactly what a spectator may
	// use to follow the game.
	players := state["players"].([]any)
	if len(players) != 2 {
		t.Fatalf("spectator sees %d seats, want 2", len(players))
	}
	for _, raw := range players {
		p := raw.(map[string]any)
		if p["hand"] != nil {
			t.Errorf("seat %v's hand reached a spectator", p["seat"])
		}
		if int(p["handCount"].(float64)) == 0 {
			t.Errorf("seat %v shows an empty hand after the deal", p["seat"])
		}
		if p["name"] == "" || p["name"] == nil {
			t.Errorf("seat %v has no name", p["seat"])
		}
	}

	// No card id from any hand may appear anywhere in the spectator body.
	raw, _ := json.Marshal(joined)
	for _, seat := range []int{0, 1} {
		c := &roomClient{t: t, srv: host.srv, store: host.store, code: host.code, seat: seat}
		if seat == 0 {
			c.token = host.token
		} else {
			c.token = guest.token
		}
		own := c.get("/api/rooms/" + c.code)["state"].(map[string]any)["hand"].([]any)
		for _, card := range own {
			id := card.(map[string]any)["id"].(string)
			if strings.Contains(string(raw), `"`+id+`"`) {
				t.Fatalf("spectator body leaks %s", id)
			}
		}
	}

	// The watch id reads the room, and stops working when dropped.
	if resp, out := host.watchGet(w); resp.StatusCode != http.StatusOK {
		t.Fatalf("watch get: %d %v", resp.StatusCode, out)
	}
	if resp, _ := host.do(http.MethodGet, "/api/rooms/"+host.code+"/watch", nil, false); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("watching without an id: %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, host.srv.URL+"/api/rooms/"+host.code+"/watch", nil)
	req.Header.Set("X-Regicide-Watch", w.id)
	if resp, err := host.srv.Client().Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("drop watcher: %v %v", err, resp)
	}
	if resp, _ := host.watchGet(w); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("dropped watcher still reads: %d", resp.StatusCode)
	}
}

func TestSpectatorCannotAct(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()
	_, w := host.watch()

	// A watch id is not a seat token: presenting it as one must fail.
	req, _ := http.NewRequest(http.MethodPost, host.srv.URL+"/api/rooms/"+host.code+"/actions",
		bytes.NewReader([]byte(`{"action":{"type":"yield"}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Regicide-Seat", "0:"+w.id)
	resp, err := host.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a watch id acted as seat 0: %d", resp.StatusCode)
	}
}

func TestHostCanSwitchSpectatorsOff(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()
	_, w := host.watch()

	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/watching", map[string]any{"allowed": false}, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Fatalf("guest switched watching off: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": false}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("switch off: %d %v", resp.StatusCode, out)
	}

	// The open watcher is dropped and cannot rejoin; the room says why.
	if resp, _ := host.watchGet(w); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a watcher survived the switch: %d", resp.StatusCode)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false); resp.StatusCode != http.StatusForbidden || out["error"] != "watching_disabled" {
		t.Fatalf("watching after the switch: %d %v", resp.StatusCode, out)
	}
	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	if room["allowWatch"] != false || room["watchLink"] != nil {
		t.Errorf("room still offers watching: allowWatch=%v link=%v", room["allowWatch"], room["watchLink"])
	}
}

func TestSpectatorsAreCapped(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	for i := 0; i < maxWatchers; i++ {
		if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false); resp.StatusCode != http.StatusOK {
			t.Fatalf("watcher %d: %d %v", i, resp.StatusCode, out)
		}
	}
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watch", nil, false)
	if resp.StatusCode != http.StatusConflict || out["error"] != "watchers_full" {
		t.Fatalf("watcher %d: %d %v, want 409 watchers_full", maxWatchers+1, resp.StatusCode, out)
	}
}

func TestSpectatorCursorIsClamped(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()
	_, w := host.watch()

	post := func(body map[string]any) *http.Response {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, host.srv.URL+"/api/rooms/"+host.code+"/watch/cursor", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Regicide-Watch", w.id)
		resp, err := host.srv.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		resp.Body.Close()
		return resp
	}
	if resp := post(map[string]any{"x": 1.7, "y": -0.4}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cursor: %d", resp.StatusCode)
	}
	host.store.mu.RLock()
	sess, _ := host.store.ByCode(host.code)
	host.store.mu.RUnlock()
	sess.mu.Lock()
	got := sess.watchers[w.id].Cursor
	sess.mu.Unlock()
	if got.X != 1 || got.Y != 0 {
		t.Errorf("cursor = {%v,%v}, want clamped to 1,0", got.X, got.Y)
	}
}

func TestSpectatorNeverSeesHandsEvenWithOpenHands(t *testing.T) {
	host := newRoomClient(t)
	resp, out := host.do(http.MethodPost, "/api/rooms", map[string]any{"theme": "simple", "openHands": true}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	host.code = out["code"].(string)
	host.token = out["token"].(string)
	guest := *host
	guest.join()
	host.start()

	// A seated player in an open-hands room does see every hand.
	seated := host.get("/api/rooms/" + host.code)["state"].(map[string]any)
	sawHand := false
	for _, raw := range seated["players"].([]any) {
		if len(raw.(map[string]any)["hand"].([]any)) > 0 {
			sawHand = true
		}
	}
	if !sawHand {
		t.Fatal("open hands did not open for a seated player")
	}

	// A spectator does not, ever.
	joined, _ := host.watch()
	for _, raw := range joined["state"].(map[string]any)["players"].([]any) {
		if raw.(map[string]any)["hand"] != nil {
			t.Fatalf("open hands leaked to a spectator")
		}
	}
}

func TestSpectatorCanViewAsAPlayerWhenHostAllows(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	// Default: hands are private, and asking for a seat changes nothing.
	joined, w := host.watch()
	if joined["room"].(map[string]any)["watchHands"] != false {
		t.Error("watchHands should default to off")
	}
	resp, as0 := host.watchGetURL("/api/rooms/"+host.code+"/watch?as=0", w)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch as 0: %d", resp.StatusCode)
	}
	if int(as0["watchingAs"].(float64)) != -1 {
		t.Errorf("watchingAs = %v without permission, want -1", as0["watchingAs"])
	}
	if hand := as0["state"].(map[string]any)["hand"].([]any); len(hand) != 0 {
		t.Fatalf("a spectator got a hand without permission: %d cards", len(hand))
	}

	// Only the host may open hands.
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/watching", map[string]any{"allowed": true, "hands": true}, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("guest opened hands: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": true, "hands": true}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("host opened hands: %d %v", resp.StatusCode, out)
	}

	// The spectator now sees seat 0's board: the same cards that seat sees.
	seat0 := host.get("/api/rooms/" + host.code)["state"].(map[string]any)["hand"].([]any)
	_, asView := host.watchGetURL("/api/rooms/"+host.code+"/watch?as=0", w)
	if int(asView["watchingAs"].(float64)) != 0 {
		t.Fatalf("watchingAs = %v, want 0", asView["watchingAs"])
	}
	if asView["spectator"] != true {
		t.Error("the response does not mark itself a spectator")
	}
	state := asView["state"].(map[string]any)
	seen := state["hand"].([]any)
	if len(seen) != len(seat0) {
		t.Fatalf("spectator sees %d cards, seat 0 has %d", len(seen), len(seat0))
	}
	for i := range seen {
		if seen[i].(map[string]any)["id"] != seat0[i].(map[string]any)["id"] {
			t.Fatalf("card %d differs: %v vs %v", i, seen[i].(map[string]any)["id"], seat0[i].(map[string]any)["id"])
		}
	}
	// Viewing as a player still never grants an action.
	if legal := state["legal"].([]any); len(legal) != 0 {
		t.Errorf("spectator got %d legal actions while viewing as a player", len(legal))
	}

	// Another seat's board is another hand.
	seat1 := guest.get("/api/rooms/" + guest.code)["state"].(map[string]any)["hand"].([]any)
	_, as1 := host.watchGetURL("/api/rooms/"+host.code+"/watch?as=1", w)
	if len(as1["state"].(map[string]any)["hand"].([]any)) != len(seat1) {
		t.Error("viewing as seat 1 did not show seat 1's hand")
	}

	// An impossible seat falls back to the table view.
	_, bad := host.watchGetURL("/api/rooms/"+host.code+"/watch?as=99", w)
	if int(bad["watchingAs"].(float64)) != -1 || len(bad["state"].(map[string]any)["hand"].([]any)) != 0 {
		t.Errorf("as=99 did not fall back: watchingAs=%v", bad["watchingAs"])
	}

	// Switching spectators off closes hands with it.
	host.do(http.MethodPost, "/api/rooms/"+host.code+"/watching", map[string]any{"allowed": false, "hands": true}, true)
	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	if room["watchHands"] != false {
		t.Error("hands stayed open after watching was disabled")
	}
}

// watchGetURL fetches a watch URL with the watcher header.
func (c *roomClient) watchGetURL(path string, w *watcher) (*http.Response, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.srv.URL+path, nil)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-Regicide-Watch", w.id)
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestRoomRecordsAVerifiableReplay(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	// A few real actions, then take the log. Each seat is asked for its own state:
	// pairing one seat's legal list with another seat's token posts out of turn (it
	// used to "work" only because a repeated action id returned a cached answer).
	for i := 0; i < 6; i++ {
		var actor *roomClient
		var st map[string]any
		for _, c := range []*roomClient{host, &guest} {
			s := c.get("/api/rooms/" + c.code)["state"].(map[string]any)
			if legal, _ := s["legal"].([]any); len(legal) > 0 {
				actor, st = c, s
				break
			}
		}
		if actor == nil {
			break
		}
		legal, _ := st["legal"].([]any)
		if len(legal) == 0 {
			break
		}
		choice := legal[0].(map[string]any)
		action := map[string]any{"type": choice["type"]}
		if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
			action["cardIds"] = ids
		}
		if seat, ok := choice["seat"]; ok {
			action["seat"] = seat
		}
		resp, out := actor.act(action)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("action: %d %v", resp.StatusCode, out)
		}
		// Nothing to carry forward: the next iteration asks each seat again.
	}

	// Only the host may download it, because it contains every hand.
	if resp, _ := guest.do(http.MethodGet, "/api/rooms/"+guest.code+"/replay", nil, true); resp.StatusCode != http.StatusConflict {
		t.Errorf("a guest downloaded the replay: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, host.srv.URL+"/api/rooms/"+host.code+"/replay", nil)
	req.Header.Set("X-Regicide-Seat", fmt.Sprintf("%d:%s", host.seat, host.token))
	resp, err := host.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("replay request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("content-type %q", ct)
	}

	// And it must actually replay: every state hash recomputed and compared.
	rep, err := replay.Read(resp.Body)
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	game, err := rep.Verify()
	if err != nil {
		t.Fatalf("recorded replay does not verify: %v", err)
	}
	if len(rep.Steps) == 0 {
		t.Fatal("the replay recorded no actions")
	}
	if rep.Header.Room != host.code || rep.Header.NumPlayers != 2 || rep.Header.Mode != "coop" {
		t.Errorf("header = %+v", rep.Header)
	}
	if rep.Header.Players[0].Name == "" || rep.Header.Players[1].Name == "" {
		t.Error("the replay lost the seat names")
	}
	_ = game
}

func TestSoloSessionRecordsAReplay(t *testing.T) {
	c := newRoomClient(t)
	resp, out := c.do(http.MethodPost, "/api/sessions", map[string]any{"theme": "simple"}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: %d %v", resp.StatusCode, out)
	}
	id := out["sessionId"].(string)
	state := out["state"].(map[string]any)
	legal := state["legal"].([]any)
	choice := legal[0].(map[string]any)
	action := map[string]any{"type": choice["type"]}
	if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
		action["cardIds"] = ids
	}
	if r, o := c.do(http.MethodPost, "/api/sessions/"+id+"/actions", map[string]any{"action": action}, false); r.StatusCode != http.StatusOK {
		t.Fatalf("action: %d %v", r.StatusCode, o)
	}

	req, _ := http.NewRequest(http.MethodGet, c.srv.URL+"/api/sessions/"+id+"/replay", nil)
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay status %d", resp.StatusCode)
	}
	rep, err := replay.Read(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := rep.Verify(); err != nil {
		t.Fatalf("solo replay does not verify: %v", err)
	}
	if rep.Header.Mode != "solo" || rep.Header.NumPlayers != 1 {
		t.Errorf("header = %+v", rep.Header)
	}

	// The end screen can open the same viewer the co-op host gets: the session id is
	// the credential, and the ticket is what the ticket-only /api/replay route accepts.
	resp, out = c.do(http.MethodPost, "/api/sessions/"+id+"/replay/ticket", map[string]any{}, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("solo replay ticket: %d %v", resp.StatusCode, out)
	}
	ticket, _ := out["ticket"].(string)
	if ticket == "" {
		t.Fatal("solo replay ticket is empty")
	}
	req, _ = http.NewRequest(http.MethodGet, c.srv.URL+"/api/replay/"+ticket+"?step=0", nil)
	resp, err = c.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("viewer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer status %d", resp.StatusCode)
	}
	var view map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("viewer json: %v", err)
	}
	if _, ok := view["steps"]; !ok {
		t.Errorf("viewer response has no steps: %v", view)
	}
}

// TestF3ConcurrentJoinPollsAndTakes is the acceptance test for the projection race:
// join, take, watch, poll and act all run against one room at the same time, and the
// test asserts the *outcomes* rather than only staying quiet under -race.
//
// Before ProjectFor existed, a handler read the seat book under one lock and the room
// view under another; between them a take could rewrite a token hash, a join could
// append a seat, or a watcher could be switched off, and the response described a
// table that never existed.
func TestF3ConcurrentJoinPollsAndTakes(t *testing.T) {
	host := newRoomClient(t)
	created := host.createRoom("simple")
	code := created["code"].(string)

	// One server, several clients: the store is the shared thing under test.
	guest := *host
	guest.join()
	third := *host
	third.join()

	// Make the third seat takeable: an away seat can be handed on (D-41).
	sess, ok := host.store.ByCode(code)
	if !ok {
		t.Fatalf("room %s missing", code)
	}
	sess.mu.Lock()
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat == 2 {
			sess.Room.Seats[i].LastSeen = time.Now().Add(-10 * time.Minute)
		}
	}
	sess.mu.Unlock()

	type anomaly struct {
		where  string
		detail string
	}
	var mu sync.Mutex
	var anomalies []anomaly
	note := func(where, format string, args ...any) {
		mu.Lock()
		anomalies = append(anomalies, anomaly{where: where, detail: fmt.Sprintf(format, args...)})
		mu.Unlock()
	}

	// A response must describe exactly one instant: the code at the top, the code in
	// the room view and the seat's own index agree, and the body is well-formed.
	checkBody := func(where string, body map[string]any) int {
		if body == nil {
			note(where, "empty body")
			return -1
		}
		if got, _ := body["code"].(string); got != code {
			note(where, "code = %q, want %q", got, code)
		}
		room, ok := body["room"].(map[string]any)
		if !ok {
			note(where, "no room view")
			return -1
		}
		if got, _ := room["code"].(string); got != code {
			note(where, "room.code = %q, want %q", got, code)
		}
		seats, ok := room["seats"].([]any)
		if !ok {
			note(where, "room.seats is not a list")
			return -1
		}
		for _, raw := range seats {
			rs, ok := raw.(map[string]any)
			if !ok {
				note(where, "seat entry is not an object")
				continue
			}
			if name, _ := rs["name"].(string); name == "" {
				note(where, "a seat in the view has no name")
			}
		}
		seq, _ := body["seq"].(float64)
		return int(seq)
	}

	maxSeq := map[string]int{}
	observe := func(viewer string, seq int) {
		if seq < 0 {
			return
		}
		mu.Lock()
		if prev, ok := maxSeq[viewer]; ok && seq < prev {
			anomalies = append(anomalies, anomaly{where: viewer, detail: fmt.Sprintf("seq went backwards: %d after %d", seq, prev)})
		}
		if seq > maxSeq[viewer] {
			maxSeq[viewer] = seq
		}
		mu.Unlock()
	}

	// Any refusal a busy table may legitimately give. A 500 or a body that contradicts
	// itself is the failure this test is for.
	acceptable := map[int]bool{200: true, 201: true, 204: true, 400: true, 401: true, 403: true, 404: true, 409: true, 429: true}

	do := func(c *roomClient, method, path string, body any) (int, map[string]any) {
		resp, out := c.do(method, path, body, true)
		if !acceptable[resp.StatusCode] {
			note(path, "status %d: %v", resp.StatusCode, out)
		}
		return resp.StatusCode, out
	}

	const rounds = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	for lane := 0; lane < 8; lane++ {
		wg.Add(1)
		go func(lane int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				switch lane {
				case 0, 1: // host polls
					status, out := do(host, http.MethodGet, "/api/rooms/"+code, nil)
					if status == 200 {
						observe("host", checkBody("host poll", out))
					}
				case 2: // guest polls
					status, out := do(&guest, http.MethodGet, "/api/rooms/"+code, nil)
					if status == 200 {
						observe("guest", checkBody("guest poll", out))
					}
				case 3: // a seat joins late (capacity 4 stops it eventually)
					late := *host
					late.code = code
					resp, _ := late.do(http.MethodPost, "/api/rooms/"+code+"/join", nil, false)
					if !acceptable[resp.StatusCode] {
						note("join", "status %d", resp.StatusCode)
					}
				case 4: // watchers come and go
					w := *host
					w.code = code
					resp, body := w.do(http.MethodPost, "/api/rooms/"+code+"/watch", nil, false)
					if resp.StatusCode == 200 {
						id, _ := body["watchId"].(string)
						if id == "" {
							note("watch", "200 with no watchId")
							continue
						}
						checkBody("watch", body)
						req, _ := http.NewRequest(http.MethodGet, w.srv.URL+"/api/rooms/"+code+"/watch", nil)
						req.Header.Set("X-Regicide-Watch", id)
						gresp, gerr := w.srv.Client().Do(req)
						if gerr != nil {
							note("watch get", "%v", gerr)
						} else {
							gresp.Body.Close()
							if gresp.StatusCode != 200 && gresp.StatusCode != 401 {
								note("watch get", "status %d", gresp.StatusCode)
							}
						}
					} else if !acceptable[resp.StatusCode] {
						note("watch", "status %d", resp.StatusCode)
					}
				case 5: // the host takes the away seat (succeeds once, then not away)
					status, out := do(host, http.MethodPost, "/api/rooms/"+code+"/seats/2/take", nil)
					if status == 200 {
						if tok, _ := out["token"].(string); tok == "" {
							note("take", "200 without a token")
						}
					}
				case 6: // someone reclaims with a bad key (must never succeed)
					bad := *host
					bad.code = code
					resp, _ := bad.do(http.MethodPost, "/api/rooms/"+code+"/seats/1/reclaim", map[string]any{"key": "not-a-key"}, false)
					if resp.StatusCode == 200 {
						note("reclaim", "a bad key was accepted")
					}
				case 7: // the host tries to act while everyone else reads
					do(host, http.MethodPost, "/api/rooms/"+code+"/ping", nil)
				}
			}
		}(lane)
	}
	close(start)
	wg.Wait()

	for _, a := range anomalies {
		t.Errorf("%s: %s", a.where, a.detail)
	}

	// After the storm the table is still coherent: the seats that exist are distinct,
	// named and inside the capacity, and the room is findable by code.
	sess.mu.Lock()
	seats := len(sess.Room.Seats)
	seenSeat := map[int]bool{}
	seenName := map[string]bool{}
	for _, rs := range sess.Room.Seats {
		if seenSeat[rs.Seat] {
			t.Errorf("seat %d appears twice", rs.Seat)
		}
		if rs.Name == "" || seenName[rs.Name] {
			t.Errorf("seat %d has a missing or duplicate name %q", rs.Seat, rs.Name)
		}
		seenSeat[rs.Seat] = true
		seenName[rs.Name] = true
	}
	sess.mu.Unlock()
	if seats > roomCapacity {
		t.Errorf("room holds %d seats, capacity is %d", seats, roomCapacity)
	}
	if _, ok := host.store.ByCode(code); !ok {
		t.Errorf("room %s is no longer reachable by code", code)
	}
}

// ---- P0 findings: seed, rejoin metadata, cursor floor, expectedSeq, take view ----

// F-2: a co-op table must not publish its seed while it is being played. A known
// seed fixes the shuffle, so every hand would be derivable from it.
func TestF2CoopSnapshotNoSeed(t *testing.T) {
	host := newRoomClient(t)
	resp, out := host.do(http.MethodPost, "/api/rooms", map[string]any{"theme": "simple", "seed": "SEEDED-TABLE"}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	host.code = out["code"].(string)
	host.seat = int(out["seat"].(float64))
	host.token = out["token"].(string)
	guest := *host
	guest.join()
	host.start()

	for _, c := range []*roomClient{host, &guest} {
		state := c.get("/api/rooms/" + c.code)["state"].(map[string]any)
		if seed, ok := state["seed"].(string); ok && seed != "" {
			t.Fatalf("seat %d received the seed %q mid-game", c.seat, seed)
		}
		if vis, _ := state["seedVisible"].(bool); vis {
			t.Fatalf("seat %d was told the seed is visible mid-game", c.seat)
		}
	}

	// When the table finishes, the seed stops being a secret.
	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	sess.mu.Lock()
	sess.Game.Phase = engine.Won
	sess.mu.Unlock()
	p := host.store.ProjectFor(sess, 0)
	if p.State.Seed == "" || !p.State.SeedVisible {
		t.Fatalf("a finished table still withholds its seed: %q", p.State.Seed)
	}
}

// F-2 (solo): the practice seed is the solo player's own, and stays.
func TestF2SoloSeedStillShown(t *testing.T) {
	srv, err := New(Options{DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	sess, err := srv.store.Create("SOLO-SEED", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, state := srv.store.View(sess)
	if state.Seed == "" || !state.SeedVisible {
		t.Fatalf("solo lost its seed: %q", state.Seed)
	}
}

// F-11: whether a seat has a live reconnect link, and whether it can be taken over,
// are the host's business. A guest or spectator learns nothing about it.
func TestF11RejoinMetadataHostOnly(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/invite", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("invite: %d %v", resp.StatusCode, out)
	}

	raw, _ := json.Marshal(host.get("/api/rooms/" + host.code))
	if !strings.Contains(string(raw), `"rejoinLive":true`) {
		t.Fatalf("the host cannot see the live link: %s", raw)
	}
	raw, _ = json.Marshal(guest.get("/api/rooms/" + guest.code))
	if strings.Contains(string(raw), `"rejoinLive":true`) || strings.Contains(string(raw), `"rejoinSeconds":`) {
		t.Fatalf("a guest sees host-only rejoin state: %s", raw)
	}
	if strings.Contains(string(raw), `"takeable":true`) {
		t.Fatalf("a guest sees takeable: %s", raw)
	}
}

// F-5: one client cannot flood the pointer relay. Frames inside the floor are
// dropped, and the answer is still 204 so a fast client learns nothing.
func TestF5CursorFloor(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	id, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	applied := 0
	for i := 0; i < 100; i++ {
		okFrame, live := host.store.SetWatcherCursor(sess, id, Cursor{X: 0.5, Y: 0.5})
		if !live {
			t.Fatal("the watcher stopped being live")
		}
		if okFrame {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("%d of 100 frames inside one frame-time were applied, want 1", applied)
	}
	// After the floor has passed, the next frame lands.
	sess.mu.Lock()
	sess.watchers[id].LastCursor = time.Now().Add(-time.Second)
	sess.mu.Unlock()
	if okFrame, _ := host.store.SetWatcherCursor(sess, id, Cursor{X: 0.2, Y: 0.3}); !okFrame {
		t.Fatal("a frame after the floor was dropped")
	}
}

// F-24: an action that does not say which sequence it is based on is refused, so
// "omitted" can never mean "unchecked".
func TestF24MissingExpectedSeqRejected(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()
	room := host.get("/api/rooms/" + host.code)
	state := room["state"].(map[string]any)
	legal := state["legal"].([]any)
	if len(legal) == 0 {
		t.Fatal("the host has no legal action")
	}
	choice := legal[0].(map[string]any)
	action := map[string]any{"type": choice["type"]}
	if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
		action["cardIds"] = ids
	}
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", map[string]any{
		"actionId": "no-seq-1",
		"action":   action,
	}, true)
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "expected_seq_required" {
		t.Fatalf("action without expectedSeq: %d %v", resp.StatusCode, out)
	}
	// The same action with the sequence the client was shown is accepted.
	seq := int(room["seq"].(float64))
	resp, out = host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", map[string]any{
		"actionId": "with-seq-1", "expectedSeq": seq, "action": action,
	}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("action with expectedSeq: %d %v", resp.StatusCode, out)
	}
}

// F-28: a takeover answers with the *target's* view and the target's new token, so
// the host's browser can hand the seat over.
func TestF28TakeSeatReturnsTargetView(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	sess.mu.Lock()
	for i := range sess.Room.Seats {
		if sess.Room.Seats[i].Seat == 1 {
			sess.Room.Seats[i].LastSeen = time.Now().Add(-10 * time.Minute)
		}
	}
	sess.mu.Unlock()

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/take", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("take: %d %v", resp.StatusCode, out)
	}
	if got := int(out["seat"].(float64)); got != 1 {
		t.Fatalf("take answered for seat %d, want the target seat 1", got)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("take answered without a token")
	}
	state, _ := out["state"].(map[string]any)
	if state == nil || int(state["viewerSeat"].(float64)) != 1 {
		t.Fatalf("take answered with the host's view, not the target's: %v", state)
	}
	// The token authenticates as the taken seat.
	taken := *host
	taken.seat, taken.token = 1, token
	if got := taken.get("/api/rooms/" + host.code); int(got["seat"].(float64)) != 1 {
		t.Fatalf("the new token did not authenticate as seat 1: %v", got["seat"])
	}
}

// ---- P0: the write queue (F-29, F-8, F-9) ---------------------------------

// TestF29WritesOrderedAfterUnlock is the acceptance test for moving disk I/O out of
// the session lock. It proves two things that "it still works" cannot: no write ever
// happens while sess.mu is held, and a session snapshot is written before the replay
// step for the same sequence.
func TestF29WritesOrderedAfterUnlock(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	type event struct {
		kind string
		seq  int
	}
	var mu sync.Mutex
	var events []event
	// The writer parks inside a real write on request, so the test can ask the table
	// a question while that write is in progress. If the write held the lock, the
	// question would wait for it; that is the whole finding (F-29).
	inWrite := make(chan struct{}, 1)
	release := make(chan struct{})
	parkNext := int32(1)
	host.store.writeHook = func(sess *Session, job writeJob) {
		kind := "session"
		if job.kind == writeReplayStep {
			kind = "replay"
		}
		mu.Lock()
		events = append(events, event{kind: kind, seq: job.seq})
		mu.Unlock()
		if atomic.CompareAndSwapInt32(&parkNext, 1, 0) {
			close(inWrite)
			<-release
		}
	}

	// Drive 30 real actions across both seats. The first action parks the writer, so
	// this loop reaches the responsiveness check before it continues.
	clients := []*roomClient{host, &guest}
	for n := 0; n < 30; n++ {
		var actor *roomClient
		var legal []any
		for _, c := range clients {
			state := c.get("/api/rooms/" + c.code)["state"].(map[string]any)
			if ls, _ := state["legal"].([]any); len(ls) > 0 {
				actor, legal = c, ls
				break
			}
		}
		if actor == nil {
			break // the table ended
		}
		choice := legal[0].(map[string]any)
		action := map[string]any{"type": choice["type"]}
		if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
			action["cardIds"] = ids
		}
		if seat, ok := choice["seat"]; ok {
			action["seat"] = seat
		}
		if resp, out := actor.act(action); resp.StatusCode != http.StatusOK {
			t.Fatalf("action %d: %d %v", n, resp.StatusCode, out)
		}
		if n == 0 {
			select {
			case <-inWrite:
			case <-time.After(2 * time.Second):
				t.Fatal("the writer never reached a write")
			}
			// The writer is now parked mid-write. Another seat must still be served:
			// its read takes the session lock, which a write must not be holding.
			start := time.Now()
			guest.get("/api/rooms/" + guest.code)
			took := time.Since(start)
			close(release)
			if took > 500*time.Millisecond {
				t.Fatalf("a read waited %v behind a write", took)
			}
		}
	}

	// Drain, then look at what the writer actually did.
	host.store.FlushWrites()
	mu.Lock()
	got := append([]event(nil), events...)
	mu.Unlock()

	persisted := map[int]bool{}
	steps := 0
	for _, e := range got {
		switch e.kind {
		case "session":
			persisted[e.seq] = true
		case "replay":
			steps++
			if !persisted[e.seq] {
				t.Fatalf("replay step for seq %d was written before its session snapshot", e.seq)
			}
		}
	}
	if steps == 0 {
		t.Fatal("no replay step was written through the queue")
	}

	// The log on disk is the game that was played: every hash recomputes.
	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	raw, ok := host.store.replayFile(sess.ID)
	if !ok {
		t.Fatal("no replay file")
	}
	rep, err := replay.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	if _, err := rep.Verify(); err != nil {
		t.Fatalf("queued replay does not verify: %v", err)
	}
	if len(rep.Steps) != steps {
		t.Fatalf("replay has %d steps, the writer processed %d", len(rep.Steps), steps)
	}
}

// TestF29QueueDrainsOnShutdown: an acknowledged action is on disk after FlushWrites,
// even if nothing else has touched the session since.
func TestF29QueueDrainsOnShutdown(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

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
	host.store.FlushWrites()
	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	p := host.store.ProjectFor(sess, 0)
	if p.Seq < 1 {
		t.Fatalf("seq = %d, want at least 1", p.Seq)
	}
	// The persisted snapshot carries the same sequence as memory.
	id := sess.ID
	payload, err := os.ReadFile(host.store.sessionPath(id))
	if err != nil {
		t.Fatalf("session file: %v", err)
	}
	var onDisk struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(payload, &onDisk); err != nil {
		t.Fatalf("session file: %v", err)
	}
	if onDisk.Seq != p.Seq {
		t.Fatalf("disk seq %d, memory seq %d", onDisk.Seq, p.Seq)
	}
}

// F-9: the replay step records the seat that acted, not the seat whose turn it is
// after the transition (the two differ once a turn ends).
func TestF9SoloStepRecordsTheActor(t *testing.T) {
	srv := newTestServer(t)
	sess, err := srv.store.Create("STEP-SEAT", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, state := srv.store.View(sess)
	var play engine.ActionView
	for _, a := range state.Legal {
		if a.Type == "play" && len(a.CardIDs) > 0 {
			play = a
			break
		}
	}
	if len(play.CardIDs) == 0 {
		t.Fatalf("no play offered: %v", state.Legal)
	}
	if _, _, _, err := srv.store.Apply(sess, "f9-1", -1, engine.Play{CardIDs: play.CardIDs}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	srv.store.FlushWrites()

	raw, ok := srv.store.replayFile(sess.ID)
	if !ok {
		t.Fatal("no replay")
	}
	rep, err := replay.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rep.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(rep.Steps))
	}
	if rep.Steps[0].Seat != 0 {
		t.Fatalf("step seat = %d, want the actor 0", rep.Steps[0].Seat)
	}
}

// F-30: the solo session routes must refuse a co-op session. Every guarantee a room
// makes (seat tokens, the turn gate, the withheld seed, the host-only replay) lives on
// the room routes, and a session id is a capability, not a credential.
func TestF30SoloRoutesRejectCoopSession(t *testing.T) {
	host := newRoomClient(t)
	resp, out := host.do(http.MethodPost, "/api/rooms", map[string]any{"theme": "simple", "seed": "COOP-SEED"}, false)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	host.code = out["code"].(string)
	host.seat = int(out["seat"].(float64))
	host.token = out["token"].(string)
	guest := *host
	guest.join()
	host.start()

	sess, ok := host.store.ByCode(host.code)
	if !ok {
		t.Fatal("room missing")
	}
	id := sess.ID

	// Read: no state, and therefore no seed and no hand.
	if resp, out := host.do(http.MethodGet, "/api/sessions/"+id, nil, false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET a coop session: %d %v", resp.StatusCode, out)
	}
	// Act: no bypass of the turn gate or of seat identity.
	respAct, outAct := host.do(http.MethodPost, "/api/sessions/"+id+"/actions", map[string]any{
		"actionId": "coop-1", "action": map[string]any{"type": "flip_jester"},
	}, false)
	if respAct.StatusCode != http.StatusNotFound {
		t.Errorf("POST an action to a coop session: %d %v", respAct.StatusCode, outAct)
	}
	// The replay is the host's, and it contains every hand.
	if resp, _ := host.do(http.MethodGet, "/api/sessions/"+id+"/replay", nil, false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("replay of a coop session: %d", resp.StatusCode)
	}
	// Restart would overwrite a live table.
	if resp, _ := host.do(http.MethodPost, "/api/sessions/"+id+"/restart", map[string]any{"theme": "simple"}, false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("restart of a coop session: %d", resp.StatusCode)
	}
	// And the table is still there afterwards: refusing did not delete it.
	if resp, _ := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true); resp.StatusCode != http.StatusOK {
		t.Errorf("the room did not survive: %d", resp.StatusCode)
	}
	// The leaderboard has no co-op entries (D-36), including through this door.
	if resp, _ := host.do(http.MethodPost, "/api/leaderboard", map[string]any{"sessionId": id, "name": "sneaky"}, false); resp.StatusCode != http.StatusForbidden {
		t.Errorf("leaderboard submit for a coop session: %d", resp.StatusCode)
	}
	// DELETE is the host's cancel, not a solo id delete.
	if resp, _ := host.do(http.MethodDelete, "/api/sessions/"+id, nil, false); resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE a coop session via the solo route: %d", resp.StatusCode)
	}
	if _, ok := host.store.ByCode(host.code); !ok {
		t.Error("the room is gone after a refused DELETE")
	}
}

// F-33: a deal is based on a view the host actually saw. A join that landed between
// their poll and their click is refused rather than silently adding a seat.
func TestF33StartRejectsAStaleView(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	third := *host
	third.join()

	stale := host.get("/api/rooms/" + host.code)["seq"].(float64)
	third.do(http.MethodPost, "/api/rooms/"+host.code+"/ready", map[string]any{"ready": true}, true)
	// The third seat joining bumped the sequence the host is now behind.
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/start", map[string]any{"expectedSeq": int(stale)}, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("start from a stale view: %d %v", resp.StatusCode, out)
	}
	fresh := int(host.get("/api/rooms/" + host.code)["seq"].(float64))
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/start", map[string]any{"expectedSeq": fresh}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("start from a fresh view: %d %v", resp.StatusCode, out)
	}
}

// endGameForTest finishes the game on a session, which is the only state from which
// play-again and the end sheet are reachable.
func endGameForTest(t *testing.T, sess *Session) {
	t.Helper()
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.Game.Phase = engine.Lost
	sess.Game.LostReason = engine.LostCannotAct
	sess.Game.LostSeat = 0
}

// TestPlayAgainRefusesALiveGame: POST /again during a live game rotated the log and
// re-dealt the table out from under the other seats.
func TestPlayAgainRefusesALiveGame(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/again", nil, true); resp.StatusCode != http.StatusBadRequest || out["error"] != "expected_seq_required" {
		t.Fatalf("play again without a sequence: %d %v, want 400 expected_seq_required", resp.StatusCode, out)
	}
	if resp, out := host.again(); resp.StatusCode != http.StatusConflict {
		t.Fatalf("play again during a live game: %d %v, want 409", resp.StatusCode, out)
	}
	// And the log is still the game's own, not a rotated one.
	sess, _ := host.store.ByCode(host.code)
	if sess == nil {
		t.Fatal("session disappeared")
	}
	sess.mu.Lock()
	phase := sess.Game.Phase
	sess.mu.Unlock()
	if phase == engine.Won || phase == engine.Lost {
		t.Error("the live game was ended by the refused request")
	}
}

// Play again re-deals the same seats and keeps the finished log under a numbered name.
func TestPlayAgainKeepsSeatsAndRotatesTheLog(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

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
	host.store.FlushWrites()

	sess, _ := host.store.ByCode(host.code)
	before := len(mustRead(t, host.store.replayPath(sess.ID)))
	if before == 0 {
		t.Fatal("no replay before play-again")
	}

	// Play again is only offered after a finished game: end this one first, because
	// re-dealing from under three live players was one POST away (F-39).
	endGameForTest(t, sess)

	if resp, out := host.again(); resp.StatusCode != http.StatusOK {
		t.Fatalf("play again: %d %v", resp.StatusCode, out)
	}
	host.store.FlushWrites()

	// Same seats, a new deal, and a log that starts again.
	after := host.get("/api/rooms/" + host.code)
	if got := int(after["room"].(map[string]any)["players"].(float64)); got != 2 {
		t.Fatalf("seats after play again = %d, want 2", got)
	}
	if int(after["seq"].(float64)) <= int(state["turnNo"].(float64)) {
		t.Fatalf("seq did not advance")
	}
	// The finished game is still on disk under its numbered name.
	rotated := filepath.Join(host.store.dir, "replays", sess.ID+".game1.jsonl")
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("the old log was not kept: %v", err)
	}

	// Only the host may re-deal.
	if resp, _ := guest.again(); resp.StatusCode != http.StatusConflict {
		t.Errorf("a guest re-dealt the table: %d", resp.StatusCode)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// F-16: a watch id is not a key to somebody else's view. Deleting a spectator is
// authenticated like reading one.
func TestF16WatcherDropRequiresAuth(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	sess, _ := host.store.ByCode(host.code)
	id, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}

	// No header, and a made-up id: both refused, and the real watcher survives.
	for _, hdr := range []string{"", "not-a-watch-id"} {
		req, _ := http.NewRequest(http.MethodDelete, host.srv.URL+"/api/rooms/"+host.code+"/watch", nil)
		if hdr != "" {
			req.Header.Set("X-Regicide-Watch", hdr)
		}
		resp, err := host.srv.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("delete with %q: %d, want 401", hdr, resp.StatusCode)
		}
	}
	if !host.store.WatcherAuth(sess, id) {
		t.Fatal("an unauthenticated delete removed a live watcher")
	}

	req, _ := http.NewRequest(http.MethodDelete, host.srv.URL+"/api/rooms/"+host.code+"/watch", nil)
	req.Header.Set("X-Regicide-Watch", id)
	resp, err := host.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete with the real id: %d", resp.StatusCode)
	}
	if host.store.WatcherAuth(sess, id) {
		t.Fatal("the watcher was not dropped")
	}
}

// F-17: after the host closes the table a spectator must not keep a board.
func TestF17WatcherLosesTheBoardOnCancel(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	sess, _ := host.store.ByCode(host.code)
	id, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	if p, ok := host.store.ProjectForWatcher(sess, id, -1); !ok || !p.HasGame {
		t.Fatalf("a live table gave a spectator no game")
	}

	if err := host.store.Cancel(sess, 0); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	p, ok := host.store.ProjectForWatcher(sess, id, -1)
	if !ok {
		t.Fatal("the watcher lost its identity on cancel")
	}
	if p.HasGame {
		t.Fatal("a cancelled table still projects a board to a spectator")
	}
	if p.Room == nil || !p.Room.Cancelled {
		t.Fatal("the spectator was not told the table was cancelled")
	}
}

// ---- P6: the viewer's ticket and the log's fate -------------------------------

// TestReplayTicketIsShortLived: the browser viewer cannot send a seat header, so the
// host mints a ticket. It expires, and only the host can mint one.
func TestReplayTicketIsShortLived(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

	if resp, _ := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/replay/ticket", nil, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("a guest minted a ticket: %d", resp.StatusCode)
	}
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/replay/ticket", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host ticket: %d %v", resp.StatusCode, out)
	}
	ticket := out["ticket"].(string)
	if ticket == "" {
		t.Fatal("no ticket")
	}
	if _, ok := host.store.SessionByReplayTicket(ticket); !ok {
		t.Fatal("a fresh ticket did not resolve")
	}
	// Two minutes later it is nobody's.
	host.store.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	if _, ok := host.store.SessionByReplayTicket(ticket); ok {
		t.Fatal("an expired ticket still resolved")
	}
}

// TestReplayViewRebuildsToAStep: the viewer asks for a step and gets the position, with
// the seat's own hand only.
func TestReplayViewRebuildsToAStep(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()

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
	host.store.FlushWrites()

	_, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/replay/ticket", nil, true)
	ticket := out["ticket"].(string)

	req, _ := http.NewRequest(http.MethodGet, host.srv.URL+"/api/replay/"+ticket+"?step=1&as=1", nil)
	resp, err := host.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("view: %d", resp.StatusCode)
	}
	var body struct {
		Step   int              `json:"step"`
		Steps  int              `json:"steps"`
		State  map[string]any   `json:"state"`
		Action map[string]any   `json:"action"`
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Step != 1 || body.Steps < 1 {
		t.Fatalf("step %d of %d", body.Step, body.Steps)
	}
	if int(body.State["viewerSeat"].(float64)) != 1 {
		t.Fatalf("viewerSeat = %v, want the seat asked for", body.State["viewerSeat"])
	}
	// The viewer explains the step: who acted, what it was, and the events it caused.
	if body.Action == nil || body.Action["seat"] == nil {
		t.Fatalf("action = %v, want the step's move", body.Action)
	}
	if _, ok := body.Action["type"]; !ok {
		t.Fatalf("action = %v, want a type", body.Action)
	}
	if len(body.Events) == 0 {
		t.Fatal("events = none, want the step's events")
	}
	// The events must be this step's: a play event's cards are the action's cards.
	if cards, ok := body.Action["cards"].([]any); ok && len(cards) > 0 {
		found := false
		for _, ev := range body.Events {
			if ev["type"] != "play" {
				continue
			}
			if got, _ := ev["cards"].([]any); len(got) == len(cards) {
				found = true
			}
		}
		if !found {
			t.Fatalf("events %v do not carry the move's cards %v", body.Events, cards)
		}
	}

	// Step 0 is the opening position: no move, no events.
	req0, _ := http.NewRequest(http.MethodGet, host.srv.URL+"/api/replay/"+ticket+"?step=0", nil)
	resp0, err := host.srv.Client().Do(req0)
	if err != nil {
		t.Fatalf("view step 0: %v", err)
	}
	defer resp0.Body.Close()
	var open struct {
		Action map[string]any   `json:"action"`
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(resp0.Body).Decode(&open); err != nil {
		t.Fatalf("decode step 0: %v", err)
	}
	if open.Action != nil || len(open.Events) != 0 {
		t.Fatalf("step 0 = action %v, events %v; want the opening position", open.Action, open.Events)
	}
	// Seat 1's view carries its own hand and no other.
	for _, raw := range body.State["players"].([]any) {
		p := raw.(map[string]any)
		hand, _ := p["hand"].([]any)
		if int(p["seat"].(float64)) != 1 && len(hand) != 0 {
			t.Fatalf("the viewer was shown seat %v's hand", p["seat"])
		}
	}
	if legal, _ := body.State["legal"].([]any); len(legal) != 0 {
		t.Fatal("a replay view offered legal actions")
	}
}

// TestDeleteAtATableKeepsTheLog (F-25): DELETE during a game cancels; the log survives.
func TestDeleteAtATableKeepsTheLog(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	host.start()
	sess, _ := host.store.ByCode(host.code)
	host.store.FlushWrites()
	if _, ok := host.store.replayFile(sess.ID); !ok {
		t.Fatal("no log to protect")
	}

	if resp, _ := host.do(http.MethodDelete, "/api/rooms/"+host.code, nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, ok := host.store.ByCode(host.code); !ok {
		t.Fatal("the table was destroyed instead of cancelled")
	}
	if _, ok := host.store.replayFile(sess.ID); !ok {
		t.Fatal("the log was destroyed with the delete (F-25)")
	}
	// The other seat reads the reason, not a 404.
	body := guest.get("/api/rooms/" + guest.code)
	if body["room"].(map[string]any)["cancelled"] != true {
		t.Error("the other seat did not learn the table was cancelled")
	}
}

// TestRoomCodeCollisionIsResolved: a forced collision, not a probabilistic one. The
// generator hands out the same code twice and then a free one; two rooms must both
// exist and both resolve by their own code. Before the fix the code was chosen, the
// lock released, and the room registered later: the second insert overwrote roomIndex
// and orphaned the first room by code (F-26) — which 32^5 codes never show at random.
func TestRoomCodeCollisionIsResolved(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	var calls int
	prev := roomCodeFn
	roomCodeFn = func() (string, error) {
		calls++
		if calls <= 2 {
			return "SAME1", nil
		}
		return newRoomCode()
	}
	defer func() { roomCodeFn = prev }()

	first, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("first room: %v", err)
	}
	second, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("second room: %v", err)
	}
	if first.Room.Code == second.Room.Code {
		t.Fatalf("both rooms were handed %s", first.Room.Code)
	}
	for _, sess := range []*Session{first, second} {
		got, ok := store.ByCode(sess.Room.Code)
		if !ok || got != sess {
			t.Fatalf("code %s does not resolve to its room", sess.Room.Code)
		}
	}
}

// TestRoomCodeIsReservedUnderTheLock: F-26's window was the gap between choosing the
// code and registering the room. The invariant is that there is no gap — the store lock
// is still held at that point — and sync.Mutex.TryLock can prove it: a lock that is
// already held refuses a second owner.
func TestRoomCodeIsReservedUnderTheLock(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	heldInside := false
	prev := testInCodeWindow
	testInCodeWindow = func() {
		// Still inside CreateRoom's critical section: taking the lock must fail.
		if store.mu.TryLock() {
			store.mu.Unlock()
			return
		}
		heldInside = true
	}
	defer func() { testInCodeWindow = prev }()

	if _, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !heldInside {
		t.Fatal("the code window is outside the store lock: two creators can pick the same code (F-26)")
	}
}

// TestRoomCodeUniquenessUnderConcurrency: many creators at once must never share a
// code, and every code must resolve to the room that was given it.
func TestRoomCodeUniquenessUnderConcurrency(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	const n = 24
	type created struct {
		code string
		sess *Session
	}
	out := make([]created, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
			errs[i] = err
			if err == nil {
				out[i] = created{code: sess.Room.Code, sess: sess}
			}
		}(i)
	}
	wg.Wait()

	codes := map[string]*Session{}
	for i, row := range out {
		if errs[i] != nil {
			t.Fatalf("create %d: %v", i, errs[i])
		}
		if codes[row.code] != nil && codes[row.code] != row.sess {
			t.Fatalf("code %s was handed to two different rooms", row.code)
		}
		codes[row.code] = row.sess
	}
	for code, want := range codes {
		got, ok := store.ByCode(code)
		if !ok || got != want {
			t.Fatalf("code %s does not resolve to the room that was given it", code)
		}
	}
}

// TestWatcherNamesMatchTheCount: the menu list must name each spectator once and the
// length must agree with the count the banner reads (T2).
func TestWatcherNamesMatchTheCount(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	first, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("first watcher: %v", err)
	}
	second, err := host.store.AddWatcher(sess)
	if err != nil {
		t.Fatalf("second watcher: %v", err)
	}
	// Force a known arrival order: AddWatcher can stamp both in the same instant.
	sess.mu.Lock()
	sess.watchers[first].JoinedAt = host.store.now().Add(-2 * time.Second)
	sess.watchers[second].JoinedAt = host.store.now().Add(-time.Second)
	sess.mu.Unlock()

	room := host.get("/api/rooms/" + host.code)["room"].(map[string]any)
	names, _ := room["watchNames"].([]any)
	if got := int(room["watchers"].(float64)); got != len(names) {
		t.Fatalf("watchers = %d, watchNames = %d: the count and the list disagree", got, len(names))
	}
	if len(names) != 2 || names[0] != "Spectator 1" || names[1] != "Spectator 2" {
		t.Fatalf("watchNames = %v, want arrival order Spectator 1, Spectator 2", names)
	}
}

// TestCachedRetryAfterCancelIsRefused: the idempotency cache used to answer before the
// cancelled/lobby gate, so a retried action on a cancelled table was replayed as a
// success (F-10).
func TestCachedRetryAfterCancelIsRefused(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	state := host.get("/api/rooms/" + host.code)
	seq := int(state["seq"].(float64))
	legal := state["state"].(map[string]any)["legal"].([]any)
	choice := legal[0].(map[string]any)
	action := map[string]any{"type": choice["type"]}
	if ids, ok := choice["cardIds"].([]any); ok && len(ids) > 0 {
		action["cardIds"] = ids
	}
	// The envelope carries the id, and it is the same on the retry: that is the whole
	// point. host.act() mints a fresh id per call, which is why the first version of
	// this test passed with the bug still in place.
	const fixedID = "retry-same-id"
	envelope := func() map[string]any {
		return map[string]any{"actionId": fixedID, "expectedSeq": seq, "action": action}
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", envelope(), true); resp.StatusCode != http.StatusOK {
		t.Fatalf("first action: %d %v", resp.StatusCode, out)
	}

	// The host closes the table, then the same action id is retried.
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}
	sess.mu.Lock()
	cancelled := sess.Room.Cancelled
	sess.mu.Unlock()
	if !cancelled {
		t.Fatal("the room was not cancelled")
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", envelope(), true); resp.StatusCode == http.StatusOK {
		t.Fatalf("a cached action was replayed on a cancelled table: %d %v", resp.StatusCode, out)
	}
}

// TestActionRateLimit: one seat's client cannot spend the table's budget (T8.6). The
// 429 comes with Retry-After, and the next seat is unaffected — the budget is per seat,
// not per room.
func TestActionRateLimit(t *testing.T) {
	host, _ := startedRoom(t)
	var got429 int
	for i := 0; i < 160; i++ {
		state := host.get("/api/rooms/" + host.code)
		seq := int(state["seq"].(float64))
		// A yield is the cheapest legal action to spam; the answer does not matter,
		// only the status, and a refused one still costs a token.
		body := map[string]any{
			"actionId":    fmt.Sprintf("spam-%d", i),
			"expectedSeq": seq,
			"action":      map[string]any{"type": "yield"},
		}
		resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/actions", body, true)
		if resp.StatusCode == http.StatusTooManyRequests {
			got429++
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	if got429 == 0 {
		t.Fatal("160 rapid actions were all accepted: the per-seat budget is not enforced")
	}
}

// ---- the bot seat (T7.5) -------------------------------------------------

// TestBotSeatRequiresHost: only the host hands a seat to the bot, and only when the
// player is actually gone. A bot never takes a turn from someone who is at the table.
func TestBotSeatRequiresHost(t *testing.T) {
	host, guest := startedRoom(t)
	body := map[string]any{"seat": 1, "on": true}

	// Not the host.
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+host.code+"/bot", body, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("guest set a bot: %d %v, want 409", resp.StatusCode, out)
	}
	// The seat's player is present (the guest just acted).
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/bot", body, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("bot for a present seat: %d %v, want 409", resp.StatusCode, out)
	}

	// Now the seat goes quiet: the clock rolls past the away threshold.
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.Room.Seats[1].LastSeen = host.store.now().Add(-10 * time.Minute)
	sess.mu.Unlock()
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/bot", body, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("bot for an away seat: %d %v", resp.StatusCode, out)
	}
	view := host.get("/api/rooms/" + host.code)
	seats := view["room"].(map[string]any)["seats"].([]any)
	if bot, _ := seats[1].(map[string]any)["bot"].(bool); !bot {
		t.Fatal("the seat is not labelled as held by a bot")
	}
	// And the bot actually plays: make seat 1 the active seat, then wait for a move.
	// It is a test-only shortcut into the turn, which the rules would otherwise reach
	// after the host's turn.
	sess.mu.Lock()
	sess.Game.ActiveSeat = 1
	before := sess.Seq
	sess.mu.Unlock()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		seq := sess.Seq
		sess.mu.Unlock()
		if seq > before {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the bot seat never acted")
}

// TestBotSeatObservesOnlyOwnHand: the bot decides from the seat's own projection. This
// asserts the wiring, not the policy: the observation handed to the policy must be
// built from the projection, which cannot contain another seat's cards.
func TestBotSeatObservesOnlyOwnHand(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	seat := sess.Game.ActiveSeat
	sess.mu.Unlock()
	// ProjectFor takes the session lock itself: reading the seat inside it deadlocks.
	p := host.store.ProjectFor(sess, seat)

	obs, ok := bot.FromView(p.State)
	if !ok {
		t.Fatal("a seat projection must produce an observation")
	}
	if obs.Seat != seat {
		t.Fatalf("observation for seat %d, want %d", obs.Seat, seat)
	}
	own := map[engine.CardID]bool{}
	for _, c := range obs.Hand {
		own[c.ID] = true
	}
	for _, c := range p.State.Hand {
		if !own[c.ID] {
			t.Fatalf("projection card %s missing from the observation", c.ID)
		}
	}
	// The projection is the boundary: it carries this seat's hand and nobody else's.
	if len(obs.Hand) != len(p.State.Hand) {
		t.Fatalf("observation hand %d, projection hand %d", len(obs.Hand), len(p.State.Hand))
	}
}

// ---- host succession (D-60) ----------------------------------------------

// setBotFill flips the room's 60-second disconnect recovery off in the lobby, the way
// the host's table-settings block does.
func setBotFill(t *testing.T, c *roomClient, on bool) {
	t.Helper()
	sess := mustSession(t, c)
	if err := c.store.SetBotFill(sess, hostSeatOf(t, c), on); err != nil {
		t.Fatalf("set bot fill %v: %v", on, err)
	}
}

// idleSeat makes a seat look silent for d, the way a closed tab would.
func idleSeat(t *testing.T, host *roomClient, seat int, d time.Duration) {
	t.Helper()
	sess := mustSession(t, host)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.Room.Seats[seat].LastSeen = host.store.now().Add(-d)
}

func hostSeatOf(t *testing.T, host *roomClient) int {
	t.Helper()
	sess := mustSession(t, host)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.Room.HostSeat
}

// TestHostSuccessionElectsLowestPresentSeat: when the host has been silent for
// takeAfter, the lowest-numbered present human seat inherits — the table must never be
// stranded by the one seat that holds every administrative action.
func TestHostSuccessionElectsLowestPresentSeat(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	for i := 0; i < 2; i++ {
		guest := *host
		guest.join()
	}
	idleSeat(t, host, 0, takeAfter+time.Minute)

	// Succession is elected under the session lock by whoever notices first.
	store := host.store
	sess := mustSession(t, host)
	sess.mu.Lock()
	moved := store.electHostLocked(sess, store.now())
	sess.mu.Unlock()
	if !moved {
		t.Fatal("no succession happened with a present seat available")
	}
	if got := hostSeatOf(t, host); got != 1 {
		t.Fatalf("host seat = %d, want 1 (lowest present)", got)
	}
}

// TestHostSuccessionSkipsAwayAndBotSeats: an away seat and a bot seat are not candidates.
func TestHostSuccessionSkipsAwayAndBotSeats(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	for i := 0; i < 2; i++ {
		guest := *host
		guest.join()
	}
	idleSeat(t, host, 0, takeAfter+time.Minute)
	idleSeat(t, host, 1, awayAfter+time.Minute) // away, so not present
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.Room.Seats[1].Bot = true
	moved := host.store.electHostLocked(sess, host.store.now())
	sess.mu.Unlock()
	if !moved {
		t.Fatal("no succession with seat 2 present")
	}
	if got := hostSeatOf(t, host); got != 2 {
		t.Fatalf("host seat = %d, want 2 (1 is away)", got)
	}
}

// TestHostSuccessionKeepsTheHostWhenNobodyIsPresent: a stranded table keeps its host
// rather than inventing one; the binding is the invariant.
func TestHostSuccessionKeepsTheHostWhenNobodyIsPresent(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	for seat := 0; seat < 2; seat++ {
		idleSeat(t, host, seat, takeAfter+time.Minute)
	}
	sess := mustSession(t, host)
	sess.mu.Lock()
	moved := host.store.electHostLocked(sess, host.store.now())
	sess.mu.Unlock()
	if moved {
		t.Fatal("a host was elected while every seat was away")
	}
	if got := hostSeatOf(t, host); got != 0 {
		t.Fatalf("host seat = %d, want 0 kept", got)
	}
}

// TestHandOverHostRequiresHostAndAPresentTarget, and the demoted host loses authority.
func TestHandOverHost(t *testing.T) {
	host, guest := startedRoom(t)
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+host.code+"/host", map[string]any{"seat": 0}, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("guest handed over the crown: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/host", map[string]any{"seat": 1}, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("handover: %d %v", resp.StatusCode, out)
	}
	if got := hostSeatOf(t, host); got != 1 {
		t.Fatalf("host seat = %d, want 1", got)
	}
	// The new host can cancel; the demoted one cannot.
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("demoted host cancelled: %d %v", resp.StatusCode, out)
	}
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("new host could not cancel: %d %v", resp.StatusCode, out)
	}
}

// TestHostSuccessionSkipsALeftSeat: a seat that chose to leave has no device to hold the
// menu. It is stamped present at the moment it leaves, so the presence test alone lets
// it inherit; the election must skip Left explicitly (P2-authority Q5).
func TestHostSuccessionSkipsALeftSeat(t *testing.T) {
	host, guest := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.Room.BotFill = false
	sess.mu.Unlock()
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/leave", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leave: %d %v", resp.StatusCode, out)
	}
	idleSeat(t, host, 0, takeAfter+time.Minute)

	sess.mu.Lock()
	moved := host.store.electHostLocked(sess, host.store.now())
	sess.mu.Unlock()
	if moved {
		t.Fatal("the crown was handed to a seat that left")
	}
	if got := hostSeatOf(t, host); got != 0 {
		t.Fatalf("host seat = %d, want 0 kept (seat 1 left)", got)
	}
}

// TestHandOverHostRefusesALeftSeat: the host's menu must not be able to crown a seat whose
// token is already gone, or every host-only control becomes unreachable (P2-authority Q5).
func TestHandOverHostRefusesALeftSeat(t *testing.T) {
	host, guest := startedRoom(t)
	sess := mustSession(t, host)
	sess.mu.Lock()
	sess.Room.BotFill = false
	sess.mu.Unlock()
	guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/leave", nil, true)
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/host", map[string]any{"seat": 1}, true); resp.StatusCode != http.StatusConflict {
		t.Fatalf("handover to a left seat: %d %v, want 409", resp.StatusCode, out)
	}
	if got := hostSeatOf(t, host); got != 0 {
		t.Fatalf("host seat = %d, want 0 kept", got)
	}
}

// TestTakeSeatRefusesACancelledRoom: every other lifecycle route refuses a closed table;
// this one used to mint a fresh token for a room nobody can play (P2-authority Q5).
func TestTakeSeatRefusesACancelledRoom(t *testing.T) {
	host, _ := startedRoom(t)
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/seats/1/take", nil, true)
	if resp.StatusCode != http.StatusConflict || out["error"] != "game_cancelled" {
		t.Fatalf("take on a cancelled room: %d %v, want 409 game_cancelled", resp.StatusCode, out)
	}
}

// TestSuccessionUnblocksTheLobby: the note that started this — a lobby whose host walked
// away could never be dealt.
func TestSuccessionUnblocksTheLobby(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	guest.do(http.MethodPost, "/api/rooms/"+host.code+"/ready", map[string]any{"ready": true}, true)
	idleSeat(t, host, 0, takeAfter+time.Minute)

	sess := mustSession(t, host)
	sess.mu.Lock()
	moved := host.store.electHostLocked(sess, host.store.now())
	sess.mu.Unlock()
	if !moved {
		t.Fatal("the lobby kept a silent host")
	}
	if resp, out := guest.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("the new host could not deal: %d %v", resp.StatusCode, out)
	}
}

// TestSuccessionFiresFromTheStreamTick: the election used to run only from TouchSeat, which
// the default (stream) transport never calls — so the five-minute rule was unreachable and
// the table was still stranded. The tick is the heartbeat, so the tick is the trigger.
func TestSuccessionFiresFromTheStreamTick(t *testing.T) {
	host, guest := startedRoom(t)
	sr, _, err := guest.openStream(guest.seatHeader())
	if err != nil {
		t.Fatalf("guest stream: %v", err)
	}
	// An open stream blocks the test server's shutdown: close it like every other stream
	// test does, or the whole package hangs at the end of this test.
	defer sr.cancel()
	sess := mustSession(t, host)
	sess.mu.Lock()
	h := host.store.hubForLocked(sess)
	sess.Room.Seats[0].LastSeen = host.store.now().Add(-10 * time.Minute)
	sess.mu.Unlock()
	// Snapshot under the hub lock: an open stream can unsubscribe at any moment, and a
	// compaction can rewrite a subscriber's seat.
	subs := h.frames()
	if len(subs) == 0 {
		t.Fatal("no subscriber registered")
	}
	changed := host.store.touchSubscribers(sess, subs)
	if !changed {
		t.Fatal("the tick did not elect a successor")
	}
	if got := hostSeatOf(t, host); got != 1 {
		t.Fatalf("host seat = %d, want the present guest (1)", got)
	}
}

// TestBotCannotHoldTheCrown: handing the host's own chair to the bot left the crown on a
// seat the election never revisits, so cancel/again/watching were dead for five minutes.
func TestBotCannotHoldTheCrown(t *testing.T) {
	host, _ := startedRoom(t)
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/bot", map[string]any{"seat": 0, "on": true}, true)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("the host handed its own chair to a bot: %v", out)
	}
	if got := hostSeatOf(t, host); got != 0 {
		t.Fatalf("host seat = %d", got)
	}
}

// TestCursorFramesDoNotShareTheJoinBucket: the client posts pointers at 30 Hz against a
// 10/s IP bucket, so most of a moving pointer was answered 429.
func TestCursorFramesDoNotShareTheJoinBucket(t *testing.T) {
	host, _ := startedRoom(t)
	rejected := 0
	for i := 0; i < 60; i++ {
		body := map[string]any{"x": float64(i) / 60, "y": 0.5, "mark": -1, "mx": 0, "my": 0}
		resp, _ := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cursor", body, true)
		if resp.StatusCode == http.StatusTooManyRequests {
			rejected++
		}
	}
	if rejected > 2 {
		t.Fatalf("%d of 60 pointer frames were rate-limited", rejected)
	}
}

// TestMarkOutOfRangeIsRejected: the guard allowed mark == feltMarks, one past the last.
func TestMarkOutOfRangeIsRejected(t *testing.T) {
	c := Cursor{X: 0.5, Y: 0.5, Mark: feltMarks}.Normalised()
	if c.Mark != -1 {
		t.Fatalf("mark %d survived normalisation, want -1", c.Mark)
	}
}

// TestBotIntervalIsWatchable pins playtest 3 note 3A: a bot move must be slow enough to
// read next to its seat bubble, and the loop must take the interval from the Store so a
// future test can shorten it without sleeping through a real table's rhythm.
func TestBotIntervalIsWatchable(t *testing.T) {
	// The floor is the reveal (1400 hold + 420 fly) plus the half-second beat: a pace
	// below that lets the next bot action overtake the defence reveal (D-73).
	if defaultBotPace < 1900*time.Millisecond || defaultBotPace > 3*time.Second {
		t.Fatalf("default bot pace = %v, want between 1.9s and 3s", defaultBotPace)
	}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := store.botInterval(); got != defaultBotPace {
		t.Errorf("new store interval = %v, want %v", got, defaultBotPace)
	}
	store.botPace = 40 * time.Millisecond
	if got := store.botInterval(); got != 40*time.Millisecond {
		t.Errorf("injected interval = %v, want 40ms", got)
	}
	store.botPace = 0
	if got := store.botInterval(); got != defaultBotPace {
		t.Errorf("zero interval = %v, want the default", got)
	}
}

// TestRoomsHidesFinishedAndStale: the join screen is a list of tables you can still
// join or watch. A finished game leaves it at once, and a room with no write for the
// idle hour leaves it even before the GC deletes the session.
func TestRoomsHidesFinishedAndStale(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := store.Create("", "official", engine.DefaultRules()); err != nil {
		t.Fatalf("solo session: %v", err)
	}
	fresh, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("fresh room: %v", err)
	}
	if got := len(store.Rooms()); got != 1 {
		t.Fatalf("fresh lobby listed %d rooms, want 1", got)
	}

	finished, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("finished room: %v", err)
	}
	finished.mu.Lock()
	finished.Room.Lobby = false
	finished.Game.Phase = engine.Won
	// Give a newcomer a chair to take: without the phase gate this join would succeed.
	finished.Room.Seats[0].Bot = true
	finished.mu.Unlock()
	if got := len(store.Rooms()); got != 1 {
		t.Fatalf("finished room listed %d rooms, want only the fresh one", got)
	}
	if _, _, err := store.JoinRoom(finished); err == nil {
		t.Fatal("a finished room accepted a join through a free bot chair")
	}
	if _, err := store.AddWatcher(finished); err == nil {
		t.Fatal("a finished room accepted a watcher")
	}

	stale, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("stale room: %v", err)
	}
	stale.mu.Lock()
	stale.Room.Lobby = false
	stale.UpdatedAt = store.now().Add(-roomIdleTTL - time.Minute)
	stale.mu.Unlock()
	out := store.Rooms()
	if len(out) != 1 || out[0].Code != fresh.Room.Code {
		t.Fatalf("rooms = %v, want only %s", out, fresh.Room.Code)
	}
	if _, ok := store.ByCode(stale.Room.Code); ok {
		t.Fatal("a stale room still resolves by code: a rejoin would race the GC")
	}
	if _, ok := store.ByCode(finished.Room.Code); !ok {
		t.Fatal("a finished room must stay readable for its end sheet and replay")
	}
}

// TestSetBotSeatBumpsSeqAndPersists pins the seat-book contract: a bot toggle is a
// room write like any other. It used to change the seat and never bump the sequence or
// persist, so the next frame looked unchanged and the toggle vanished on restart.
func TestSetBotSeatBumpsSeqAndPersists(t *testing.T) {
	host, _ := startedRoom(t)
	sess := mustSession(t, host)
	silenceSeat(t, host, 1, awayAfter+time.Second)

	sess.mu.Lock()
	before := sess.Seq
	sess.mu.Unlock()
	if err := host.store.SetBotSeat(sess, 0, 1, true); err != nil {
		t.Fatalf("SetBotSeat: %v", err)
	}
	sess.mu.Lock()
	after := sess.Seq
	sess.mu.Unlock()
	if after <= before {
		t.Fatalf("seq did not advance: %d -> %d", before, after)
	}

	fresh, err := NewStore(host.store.dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	reloaded, ok := fresh.ByCode(host.code)
	if !ok {
		t.Fatalf("room %s did not survive the reload", host.code)
	}
	reloaded.mu.Lock()
	isBot := len(reloaded.Room.Seats) > 1 && reloaded.Room.Seats[1].Bot
	reloaded.mu.Unlock()
	if !isBot {
		t.Fatalf("seat 1 was not persisted as a bot")
	}
}
