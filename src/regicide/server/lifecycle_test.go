package server

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

// ---- playtest-2: seats, chairs and the token ------------------------------
//
// The second co-op playtest asked for a lobby that can free a chair and hand it to
// somebody else. The rules that make that safe: the token is the credential, the seat
// number is a label; a lobby removal renumbers the chairs above it; a dealt table
// never shrinks.

// seatsFor opens a lobby with n seats and returns the host plus the guests, in join
// order. guests[i] sits on seat i+1.
func seatsFor(t *testing.T, n int) (*roomClient, []*roomClient) {
	t.Helper()
	host := newRoomClient(t)
	host.createRoom("simple")
	guests := make([]*roomClient, 0, n-1)
	for i := 1; i < n; i++ {
		g := *host
		g.join()
		guests = append(guests, &g)
	}
	return host, guests
}

// relabel returns a copy of the client that still sends an old seat number, the way a
// tab does between a compaction and its next GET.
func relabel(c *roomClient, seat int) *roomClient {
	return &roomClient{t: c.t, srv: c.srv, store: c.store, code: c.code, seat: seat, token: c.token}
}

// seatRows reads the seat array out of a room answer.
func seatRows(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["room"].(map[string]any)["seats"].([]any)
	if !ok {
		t.Fatalf("no seats in %v", body)
	}
	out := make([]map[string]any, len(raw))
	for i, row := range raw {
		out[i] = row.(map[string]any)
	}
	return out
}

// silenceSeat makes a seat look forgotten for d, the way a closed laptop does.
func silenceSeat(t *testing.T, c *roomClient, seat int, d time.Duration) {
	t.Helper()
	sess := mustSession(t, c)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.Room.Seats[seat].LastSeen = c.store.now().Add(-d)
}

// TestSeatTokenOutlivesTheSeatNumber: after a compaction the old number is stale and
// the token is still the credential. This is what stops a renumbered browser from
// being logged out of its own chair.
func TestSeatTokenOutlivesTheSeatNumber(t *testing.T) {
	host, guests := seatsFor(t, 3)
	third := guests[1]

	if resp, out := host.do(http.MethodDelete, "/api/rooms/"+host.code+"/seats/1", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("kick: %d %v", resp.StatusCode, out)
	}

	stale := relabel(third, 2) // the third player still thinks seat 2
	resp, out := stale.do(http.MethodGet, "/api/rooms/"+stale.code, nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a valid token with a stale label: %d %v", resp.StatusCode, out)
	}
	if seat := int(out["seat"].(float64)); seat != 1 {
		t.Errorf("answered seat = %d, want 1 (the server's truth)", seat)
	}
	if n := len(seatRows(t, out)); n != 2 {
		t.Errorf("seats = %d, want 2 after the kick", n)
	}
}

// TestLobbyLeaveRemovesTheChairAndRenumbers: a player leaving the lobby frees their
// chair, everybody above them moves down, and their token stops working.
func TestLobbyLeaveRemovesTheChairAndRenumbers(t *testing.T) {
	host, guests := seatsFor(t, 3)
	middle, last := guests[0], guests[1]
	resp, out := host.do(http.MethodGet, "/api/rooms/"+host.code, nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read: %d %v", resp.StatusCode, out)
	}
	wantName := seatRows(t, out)[2]["name"].(string)

	if resp, out := middle.do(http.MethodPost, "/api/rooms/"+middle.code+"/leave", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leave: %d %v", resp.StatusCode, out)
	}

	after := seatRows(t, host.get("/api/rooms/"+host.code))
	if len(after) != 2 {
		t.Fatalf("seats = %d, want 2", len(after))
	}
	if got := after[1]["name"].(string); got != wantName {
		t.Errorf("seat 1 is %q, want %q (the seat above moved down)", got, wantName)
	}
	if seat := int(after[1]["seat"].(float64)); seat != 1 {
		t.Errorf("seat label = %d, want 1", seat)
	}

	// The chair is gone, so the token that held it is gone too.
	if resp, _ := middle.do(http.MethodGet, "/api/rooms/"+middle.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the leaving token still reads the room: %d", resp.StatusCode)
	}
	// The player above kept working, with a fresh label.
	if resp, out := relabel(last, 2).do(http.MethodGet, "/api/rooms/"+last.code, nil, true); resp.StatusCode != http.StatusOK {
		t.Errorf("the renumbered seat lost its room: %d %v", resp.StatusCode, out)
	}
}

// TestKickIsHostOnly: only the host removes somebody, and the host cannot remove
// themselves with the kick route.
func TestKickIsHostOnly(t *testing.T) {
	host, guests := seatsFor(t, 3)
	guest, third := guests[0], guests[1]

	if resp, out := guest.do(http.MethodDelete, "/api/rooms/"+guest.code+"/seats/2", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest kick: %d %v, want 409 not_host", resp.StatusCode, out)
	}
	if resp, out := guest.do(http.MethodDelete, "/api/rooms/"+guest.code+"/seats/0", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest kicking the host: %d %v, want 409 not_host", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodDelete, "/api/rooms/"+host.code+"/seats/0", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_ready" {
		t.Errorf("host kicking themselves: %d %v, want 409 not_ready", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodDelete, "/api/rooms/"+host.code+"/seats/2", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("host kick: %d %v", resp.StatusCode, out)
	}
	if n := len(seatRows(t, host.get("/api/rooms/"+host.code))); n != 2 {
		t.Errorf("seats = %d, want 2 after the kick", n)
	}
	if resp, _ := third.do(http.MethodGet, "/api/rooms/"+third.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the kicked token still reads the room: %d", resp.StatusCode)
	}
}

// TestMidGameLeaveKeepsTheSeat: a dealt table never shrinks. The seat is marked left
// and, with bot fill on, played by a bot until its owner comes back.
func TestMidGameLeaveKeepsTheSeat(t *testing.T) {
	host, guest := startedRoom(t)
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/leave", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leave: %d %v", resp.StatusCode, out)
	}
	room := host.get("/api/rooms/" + host.code)
	seats := seatRows(t, room)
	if len(seats) != 2 {
		t.Fatalf("seats = %d, want the table to keep its chairs", len(seats))
	}
	if seats[1]["left"] != true {
		t.Errorf("seat 1 = %v, want left", seats[1])
	}
	if seats[1]["bot"] != true {
		t.Errorf("seat 1 = %v, want a bot with bot fill on", seats[1])
	}
	if seats[1]["away"] == true {
		t.Error("a left seat is not away: it says left")
	}

	// With bot fill off the chair simply waits for its owner. The option is a
	// lobby-only switch, so it is set before the deal.
	host2 := newRoomClient(t)
	host2.createRoom("simple")
	setBotFill(t, host2, false)
	guest2 := *host2
	guest2.join()
	if resp, out := host2.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	if resp, out := guest2.do(http.MethodPost, "/api/rooms/"+guest2.code+"/leave", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leave: %d %v", resp.StatusCode, out)
	}
	seats = seatRows(t, host2.get("/api/rooms/"+host2.code))
	if seats[1]["left"] != true || seats[1]["bot"] == true {
		t.Errorf("seat 1 with bot fill off = %v, want left and no bot", seats[1])
	}
}

// TestRoomRulesAreHostOnlyAndLobbyOnly: the table settings are the host's, and they
// are frozen once the cards are out.
func TestRoomRulesAreHostOnlyAndLobbyOnly(t *testing.T) {
	host, guests := seatsFor(t, 2)
	guest := guests[0]

	rules := map[string]any{"chaos": false, "passOnDefeat": true, "exactDamage": true}
	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/rules", map[string]any{"rules": rules}, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest rules: %d %v, want 409 not_host", resp.StatusCode, out)
	}

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/rules", map[string]any{"rules": rules}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host rules: %d %v", resp.StatusCode, out)
	}
	roomRules := out["room"].(map[string]any)["rules"].(map[string]any)
	if roomRules["passOnDefeat"] != true {
		t.Errorf("rules = %v, want passOnDefeat", roomRules)
	}

	if resp, out := host.start(); resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/rules", map[string]any{"rules": rules}, true); resp.StatusCode != http.StatusConflict || out["error"] != "game_started" {
		t.Errorf("rules after the deal: %d %v, want 409 game_started", resp.StatusCode, out)
	}
}

// TestBotFillIsHostOnly: the 60-second recovery is a room option, not a table vote.
func TestBotFillIsHostOnly(t *testing.T) {
	host, guests := seatsFor(t, 2)
	guest := guests[0]

	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/botfill", map[string]any{"on": false}, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest bot fill: %d %v, want 409 not_host", resp.StatusCode, out)
	}
	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/botfill", map[string]any{"on": false}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host bot fill: %d %v", resp.StatusCode, out)
	}
	if on := out["room"].(map[string]any)["botFill"]; on != false {
		t.Errorf("botFill = %v, want false", on)
	}
}

// TestHostAddsABotSeat: one human plus one bot is a valid table, and the deal uses
// both seats.
func TestHostAddsABotSeat(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/addbot", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add bot: %d %v", resp.StatusCode, out)
	}
	if seat := int(out["botSeat"].(float64)); seat != 1 {
		t.Errorf("botSeat = %d, want 1", seat)
	}
	seats := seatRows(t, out)
	if len(seats) != 2 || seats[1]["bot"] != true {
		t.Fatalf("seats = %v, want a bot on seat 1", seats)
	}

	startResp, startOut := host.start()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start with a bot: %d %v", startResp.StatusCode, startOut)
	}
	if n := int(startOut["state"].(map[string]any)["numPlayers"].(float64)); n != 2 {
		t.Errorf("numPlayers = %d, want 2", n)
	}

	// Only the host may add one.
	host2, guests := seatsFor(t, 2)
	if resp, out := guests[0].do(http.MethodPost, "/api/rooms/"+host2.code+"/addbot", nil, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest add bot: %d %v, want 409 not_host", resp.StatusCode, out)
	}
}

// TestCancelledRoomNotListed: a closed table waits for the GC out of sight.
func TestCancelledRoomNotListed(t *testing.T) {
	host, _ := startedRoom(t)
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/cancel", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d %v", resp.StatusCode, out)
	}
	resp, out := host.do(http.MethodGet, "/api/rooms", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if rooms, _ := out["rooms"].([]any); len(rooms) != 0 {
		t.Errorf("listed %d rooms, want the cancelled table hidden", len(rooms))
	}
}

// TestStreamFollowsSeatRenumber: a live stream is a seat's, so when the chair moves
// the stream moves with it, and the frame says so. The removed seat is told why.
func TestStreamFollowsSeatRenumber(t *testing.T) {
	host, guests := seatsFor(t, 3)
	middle, third := guests[0], guests[1]

	sr, first, err := third.openStream(third.seatHeader())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.cancel()
	if seat := int(first["seat"].(float64)); seat != 2 {
		t.Fatalf("welcome seat = %d, want 2", seat)
	}
	// The seat that is about to be removed has a stream too, so the test can see the
	// goodbye rather than only the survivors' renumbering.
	srMid, _, err := middle.openStream(middle.seatHeader())
	if err != nil {
		t.Fatalf("middle stream: %v", err)
	}
	defer srMid.cancel()

	if resp, out := host.do(http.MethodDelete, "/api/rooms/"+host.code+"/seats/1", nil, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("kick: %d %v", resp.StatusCode, out)
	}
	frame := sr.next(t, "state", 2*time.Second)
	if seat := int(frame["seat"].(float64)); seat != 1 {
		t.Errorf("frame seat = %d, want 1 after the compaction", seat)
	}
	bye := srMid.next(t, "bye", 2*time.Second)
	if bye["reason"] != "seat_removed" {
		t.Errorf("bye = %v, want seat_removed", bye)
	}
}

// TestSilentSeatGetsBotAfter60s: bot fill is the table's answer to a dead connection,
// and it never touches the host's chair.
func TestSilentSeatGetsBotAfter60s(t *testing.T) {
	host, _ := startedRoom(t)
	for seat := 0; seat < 2; seat++ {
		silenceSeat(t, host, seat, botFillAfter+time.Second)
	}
	sess := mustSession(t, host)
	if !host.store.sweepBotFill(sess) {
		t.Fatal("sweep found nothing to fill")
	}
	seats := seatRows(t, host.get("/api/rooms/"+host.code))
	if seats[0]["bot"] == true {
		t.Error("the host seat got a bot: a bot cannot hold the menu")
	}
	if seats[1]["bot"] != true {
		t.Errorf("seat 1 = %v, want a bot after 60 s of silence", seats[1])
	}
	sess.mu.Lock()
	running := sess.botRunning
	sess.mu.Unlock()
	if !running {
		t.Error("no bot loop is running")
	}
}

// TestBotFilledSeatKeepsItsOwnersToken: a bot fill borrows a silent seat, it does not
// take it. A tab that comes back after the 60 s fill reads the room with the token it
// kept and gets its own chair and hand back, which is the guarantee the client's
// lost-seat screen depends on: only a join can make a returning token 401.
func TestBotFilledSeatKeepsItsOwnersToken(t *testing.T) {
	host, guest := startedRoom(t)
	before := guest.get("/api/rooms/" + guest.code)
	wantHand := handIDs(before)

	silenceSeat(t, host, 1, botFillAfter+time.Second)
	if !host.store.sweepBotFill(mustSession(t, host)) {
		t.Fatal("sweep found nothing to fill")
	}
	if seats := seatRows(t, host.get("/api/rooms/"+host.code)); seats[1]["bot"] != true {
		t.Fatalf("seat 1 = %v, want a bot before the owner returns", seats[1])
	}

	// The owner's next read is the reclaim: same seat, same hand, human again.
	after := guest.get("/api/rooms/" + guest.code)
	if seat := int(after["seat"].(float64)); seat != 1 {
		t.Errorf("returning seat = %d, want 1", seat)
	}
	if got := handIDs(after); !reflect.DeepEqual(got, wantHand) {
		t.Errorf("returning hand = %v, want the same cards %v", got, wantHand)
	}
	if seats := seatRows(t, host.get("/api/rooms/"+host.code)); seats[1]["bot"] == true {
		t.Errorf("seat 1 = %v, want the owner back in it", seats[1])
	}
}

// handIDs reads a projection's hand as card ids, so a same-sized hand from another
// seat cannot pass as the owner's.
func handIDs(state map[string]any) []string {
	ids := []string{}
	for _, raw := range state["state"].(map[string]any)["hand"].([]any) {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

// TestReturningHumanStopsTheBot: a touch from the owner takes the chair back, and the
// loop stops when the last bot leaves.
func TestReturningHumanStopsTheBot(t *testing.T) {
	host, guest := startedRoom(t)
	silenceSeat(t, host, 1, botFillAfter+time.Second)
	if !host.store.sweepBotFill(mustSession(t, host)) {
		t.Fatal("sweep found nothing to fill")
	}

	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/ping", nil, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("ping: %d %v", resp.StatusCode, out)
	}
	seats := seatRows(t, guest.get("/api/rooms/"+guest.code))
	if seats[1]["bot"] == true {
		t.Errorf("seat 1 = %v, want the owner back", seats[1])
	}
	sess := mustSession(t, host)
	sess.mu.Lock()
	running, stop := sess.botRunning, sess.botStop
	sess.mu.Unlock()
	if running {
		t.Error("the bot loop kept running after the last bot left")
	}
	if stop != nil {
		t.Error("the bot stop channel was left open")
	}
}

// TestJoinReplacesBotMidGame: the room code is the invite, and a dealt table hands a
// joiner the lowest bot seat instead of a fifth chair.
func TestJoinReplacesBotMidGame(t *testing.T) {
	host, guest := startedRoom(t)
	silenceSeat(t, host, 1, botFillAfter+time.Second)
	if !host.store.sweepBotFill(mustSession(t, host)) {
		t.Fatal("sweep found nothing to fill")
	}

	newcomer := &roomClient{t: t, srv: host.srv, store: host.store, code: host.code}
	resp, out := newcomer.do(http.MethodPost, "/api/rooms/"+host.code+"/join", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join mid-game: %d %v", resp.StatusCode, out)
	}
	newcomer.seat = int(out["seat"].(float64))
	newcomer.token = out["token"].(string)
	if newcomer.seat != 1 {
		t.Errorf("joiner seat = %d, want 1", newcomer.seat)
	}
	if newcomer.token == guest.token {
		t.Error("the joiner got the old device's token")
	}
	seats := seatRows(t, host.get("/api/rooms/"+host.code))
	if len(seats) != 2 {
		t.Errorf("seats = %d, want the table to stay at 2", len(seats))
	}
	if seats[1]["bot"] == true {
		t.Errorf("seat 1 = %v, want the human to hold it", seats[1])
	}
	if resp, _ := guest.do(http.MethodGet, "/api/rooms/"+guest.code, nil, true); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the replaced device still reads the room: %d", resp.StatusCode)
	}
}

// TestJoinNeverExpandsTheTable: once the deal is made there is no fifth chair.
func TestJoinNeverExpandsTheTable(t *testing.T) {
	host, _ := startedRoom(t)
	late := &roomClient{t: t, srv: host.srv, store: host.store, code: host.code}
	resp, out := late.do(http.MethodPost, "/api/rooms/"+host.code+"/join", nil, false)
	if resp.StatusCode != http.StatusConflict || out["error"] != "game_started" {
		t.Errorf("join with no free seat: %d %v, want 409 game_started", resp.StatusCode, out)
	}
	if n := len(seatRows(t, host.get("/api/rooms/"+host.code))); n != 2 {
		t.Errorf("seats = %d, want 2", n)
	}
}

// TestRoomThemeIsHostOnlyAndLobbyOnly: the deck is a lobby choice, the host's, and it
// is frozen at the deal — the same rule as the switches beside it.
func TestRoomThemeIsHostOnlyAndLobbyOnly(t *testing.T) {
	host, guests := seatsFor(t, 2)
	guest := guests[0]

	if resp, out := guest.do(http.MethodPost, "/api/rooms/"+guest.code+"/theme", map[string]any{"theme": "official"}, true); resp.StatusCode != http.StatusConflict || out["error"] != "not_host" {
		t.Errorf("guest theme: %d %v, want 409 not_host", resp.StatusCode, out)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/theme", map[string]any{"theme": "not-a-theme"}, true); resp.StatusCode != http.StatusBadRequest || out["error"] != "bad_theme" {
		t.Errorf("unregistered set in a room: %d %v, want 400 bad_theme", resp.StatusCode, out)
	}
	// Pokemon is a table set now; the lobby warns, the server allows (D-86).
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/theme", map[string]any{"theme": "pokemon"}, true); resp.StatusCode != http.StatusOK {
		t.Errorf("pokemon theme: %d %v, want 200", resp.StatusCode, out)
	}

	resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/theme", map[string]any{"theme": "official"}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host theme: %d %v", resp.StatusCode, out)
	}
	if theme := out["room"].(map[string]any)["theme"]; theme != "official" {
		t.Errorf("room theme = %v, want official", theme)
	}
	themes, _ := out["room"].(map[string]any)["themes"].([]any)
	if len(themes) != 4 || themes[0] != "official" {
		t.Errorf("themes = %v, want official, simple, 8bit, pokemon", themes)
	}

	startResp, startOut := host.start()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %v", startResp.StatusCode, startOut)
	}
	if got := startOut["state"].(map[string]any)["theme"]; got != "official" {
		t.Errorf("dealt theme = %v, want official", got)
	}
	if resp, out := host.do(http.MethodPost, "/api/rooms/"+host.code+"/theme", map[string]any{"theme": "simple"}, true); resp.StatusCode != http.StatusConflict || out["error"] != "game_started" {
		t.Errorf("theme after the deal: %d %v, want 409 game_started", resp.StatusCode, out)
	}
}
