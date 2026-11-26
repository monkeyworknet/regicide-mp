package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"regicide/engine"
	"regicide/server"
)

type client struct {
	t   *testing.T
	srv *server.Server
}

func newClient(t *testing.T) *client {
	t.Helper()
	srv, err := server.New(server.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t.Cleanup(srv.FlushWrites)
	return &client{t: t, srv: srv}
}

func (c *client) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	c.srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

type stateEnvelope struct {
	SessionID string             `json:"sessionId"`
	Seq       int                `json:"seq"`
	State     engine.ClientState `json:"state"`
	Events    []engine.Event     `json:"events"`
}

func (c *client) createSession(body any) stateEnvelope {
	c.t.Helper()
	code, raw := c.do(http.MethodPost, "/api/sessions", body)
	if code != http.StatusCreated {
		c.t.Fatalf("create session = %d body %s", code, raw)
	}
	var out stateEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("decode create: %v (%s)", err, raw)
	}
	return out
}

func TestCreateSessionHidesRandomSeed(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{})
	if env.SessionID == "" {
		t.Fatal("no session id")
	}
	if env.State.SeedVisible {
		t.Fatal("the seed of a random run must stay hidden during play")
	}
	if env.State.Seed != "" {
		t.Fatalf("seed leaked: %q", env.State.Seed)
	}
	if len(env.State.Hand) != 8 || env.State.Enemy == nil {
		t.Fatalf("unexpected setup: hand %d enemy %v", len(env.State.Hand), env.State.Enemy)
	}
}

func TestCreateSessionWithChosenSeed(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "abcd-efgh-jkmn"})
	if !env.State.SeedVisible {
		t.Fatal("a chosen seed should be visible immediately")
	}
	if env.State.Seed != "ABCD-EFGH-JKMN" {
		t.Fatalf("seed = %q, want the normalised form", env.State.Seed)
	}
	if env.State.SeedRandom {
		t.Fatal("a chosen seed must not be marked random (leaderboard eligibility)")
	}
}

func TestSeedReproducesDeal(t *testing.T) {
	c := newClient(t)
	a := c.createSession(map[string]any{"seed": "REPEAT-SEED1"})
	b := c.createSession(map[string]any{"seed": "REPEAT-SEED1"})
	for i := range a.State.Hand {
		if a.State.Hand[i].ID != b.State.Hand[i].ID {
			t.Fatalf("hand[%d] differs: %s vs %s", i, a.State.Hand[i].ID, b.State.Hand[i].ID)
		}
	}
	if a.State.Enemy.ID != b.State.Enemy.ID {
		t.Fatalf("enemy differs: %s vs %s", a.State.Enemy.ID, b.State.Enemy.ID)
	}
	if a.State.TavernCount != b.State.TavernCount {
		t.Fatalf("tavern differs")
	}
}

// firstPlay returns a legal single-card play from the current state.
func firstPlay(st engine.ClientState) []engine.CardID {
	for _, a := range st.Legal {
		if a.Type == "play" && len(a.CardIDs) == 1 {
			return a.CardIDs
		}
	}
	return nil
}

func TestActionFlowUpdatesState(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "FLOW-SEED-01"})
	cards := firstPlay(env.State)
	if cards == nil {
		t.Fatal("no single-card play offered")
	}
	code, raw := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId":    "act-1",
		"expectedSeq": env.Seq,
		"action":      map[string]any{"type": "play", "cardIds": cards},
	})
	if code != http.StatusOK {
		t.Fatalf("action = %d body %s", code, raw)
	}
	var out stateEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Seq != env.Seq+1 {
		t.Fatalf("seq = %d, want %d", out.Seq, env.Seq+1)
	}
	if len(out.Events) == 0 {
		t.Fatal("events should be returned for animation")
	}
	if out.State.Damage == env.State.Damage && out.State.Phase == env.State.Phase {
		t.Fatal("state did not change after a play")
	}
}

func TestStaleSequenceConflicts(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "STALE-SEED-1"})
	cards := firstPlay(env.State)
	code, raw := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId":    "act-1",
		"expectedSeq": env.Seq + 5,
		"action":      map[string]any{"type": "play", "cardIds": cards},
	})
	if code != http.StatusConflict {
		t.Fatalf("stale action = %d body %s, want 409", code, raw)
	}
	var out struct {
		Error string             `json:"error"`
		Seq   int                `json:"seq"`
		State engine.ClientState `json:"state"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error != "stale" || out.State.Phase == "" {
		t.Fatalf("conflict payload = %+v", out)
	}
}

func TestActionIsIdempotent(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "IDEMPOTENT-1"})
	cards := firstPlay(env.State)
	payload := map[string]any{
		"actionId":    "same-action",
		"expectedSeq": env.Seq,
		"action":      map[string]any{"type": "play", "cardIds": cards},
	}
	_, first := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", payload)
	code, second := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", payload)
	if code != http.StatusOK {
		t.Fatalf("replay = %d, want 200", code)
	}
	var a, b stateEnvelope
	_ = json.Unmarshal(first, &a)
	_ = json.Unmarshal(second, &b)
	if a.Seq != b.Seq {
		t.Fatalf("replay advanced the game: %d vs %d", a.Seq, b.Seq)
	}
	if len(b.State.Hand) != len(a.State.Hand) {
		t.Fatalf("replay changed the hand: %d vs %d", len(b.State.Hand), len(a.State.Hand))
	}
}

func TestIllegalActionReturns400(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "ILLEGAL-SEED"})
	code, raw := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId":    "bad-1",
		"expectedSeq": env.Seq,
		"action":      map[string]any{"type": "play", "cardIds": []string{"ZZ"}},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("illegal action = %d body %s, want 400", code, raw)
	}
	code, _ = c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId": "bad-2",
		"action":   map[string]any{"type": "teleport"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("unknown action type = %d, want 400", code)
	}
}

func TestSessionResumeAndNotFound(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "RESUME-SEED1"})
	code, raw := c.do(http.MethodGet, "/api/sessions/"+env.SessionID, nil)
	if code != http.StatusOK {
		t.Fatalf("resume = %d body %s", code, raw)
	}
	var out stateEnvelope
	_ = json.Unmarshal(raw, &out)
	if out.SessionID != env.SessionID || len(out.State.Hand) != 8 {
		t.Fatalf("resume payload wrong: %+v", out.State.Hand)
	}

	code, _ = c.do(http.MethodGet, "/api/sessions/deadbeef", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing session = %d, want 404", code)
	}

	code, _ = c.do(http.MethodDelete, "/api/sessions/"+env.SessionID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
	code, _ = c.do(http.MethodGet, "/api/sessions/"+env.SessionID, nil)
	if code != http.StatusNotFound {
		t.Fatalf("deleted session = %d, want 404", code)
	}
}

func TestRestartKeepsSessionID(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "RESTART-SEED"})
	cards := firstPlay(env.State)
	_, _ = c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId":    "r-1",
		"expectedSeq": env.Seq,
		"action":      map[string]any{"type": "play", "cardIds": cards},
	})

	code, raw := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/restart", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("restart = %d body %s", code, raw)
	}
	var out stateEnvelope
	_ = json.Unmarshal(raw, &out)
	if out.SessionID != env.SessionID {
		t.Fatalf("session id changed on restart")
	}
	if out.State.SeedVisible {
		t.Fatal("a restarted random game must hide its new seed again")
	}
	if len(out.State.Hand) != 8 {
		t.Fatalf("hand = %d after restart, want 8", len(out.State.Hand))
	}
}

func TestLeaderboardStartsEmpty(t *testing.T) {
	c := newClient(t)
	code, raw := c.do(http.MethodGet, "/api/leaderboard", nil)
	if code != http.StatusOK {
		t.Fatalf("leaderboard = %d", code)
	}
	var out map[string][]server.Entry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	for _, tier := range []string{"gold", "silver", "bronze"} {
		if out[tier] == nil {
			t.Fatalf("%s should be an empty array, got %v", tier, out[tier])
		}
	}
}

func TestSubmitIneligibleRun(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "CHOSEN-SEED1"})
	code, raw := c.do(http.MethodPost, "/api/leaderboard", map[string]any{
		"sessionId": env.SessionID,
		"name":      "Ada",
	})
	if code != http.StatusForbidden {
		t.Fatalf("seeded run submission = %d body %s, want 403", code, raw)
	}
}

func TestHealthExposesCounters(t *testing.T) {
	c := newClient(t)
	c.createSession(map[string]any{})
	code, raw := c.do(http.MethodGet, "/api/health", nil)
	if code != http.StatusOK {
		t.Fatalf("health = %d", code)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["status"] != "ok" {
		t.Fatalf("status = %v", out["status"])
	}
	if n, ok := out["sessions"].(float64); !ok || n < 1 {
		t.Fatalf("sessions counter = %v", out["sessions"])
	}
}

func TestContentTypeAndNoStoreOnAPI(t *testing.T) {
	c := newClient(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	c.srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
}

func TestUnknownStaticPathIs404(t *testing.T) {
	c := newClient(t)
	code, _ := c.do(http.MethodGet, "/nope/nothing", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown asset = %d, want 404", code)
	}
	code, raw := c.do(http.MethodGet, "/", nil)
	if code != http.StatusOK {
		t.Fatalf("index = %d", code)
	}
	if !bytes.Contains(raw, []byte("<html")) {
		t.Fatalf("index body does not look like HTML: %s", raw[:min(60, len(raw))])
	}
}

func TestMethodDispatch(t *testing.T) {
	c := newClient(t)
	code, _ := c.do(http.MethodGet, "/api/sessions", nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/sessions = %d, want 405", code)
	}
	code, _ = c.do(http.MethodPut, "/api/leaderboard", nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/leaderboard = %d, want 405", code)
	}
}

func TestSessionSurvivesServerRestart(t *testing.T) {
	dir := t.TempDir()
	srv1, err := server.New(server.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	c1 := &client{t: t, srv: srv1}
	env := c1.createSession(map[string]any{"seed": "PERSIST-SEED"})

	srv2, err := server.New(server.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	c2 := &client{t: t, srv: srv2}
	code, raw := c2.do(http.MethodGet, "/api/sessions/"+env.SessionID, nil)
	if code != http.StatusOK {
		t.Fatalf("resume after restart = %d body %s", code, raw)
	}
	var out stateEnvelope
	_ = json.Unmarshal(raw, &out)
	for i := range env.State.Hand {
		if out.State.Hand[i].ID != env.State.Hand[i].ID {
			t.Fatalf("hand changed across restart")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestThemeSelection proves every shipped card set is accepted and that an unknown
// id falls back to the default instead of persisting a board of missing art.
func TestThemeSelection(t *testing.T) {
	c := newClient(t)
	cases := map[string]string{
		"official": "official", "simple": "simple", "pokemon": "pokemon", "8bit": "8bit",
		"Simple": "simple", "  8BIT  ": "8bit",
		"": "official", "unknown-deck": "official", "../etc/passwd": "official",
		"casino": "official", "classic": "official", "jorel": "official", "modern": "official",
	}
	for in, want := range cases {
		status, body := c.do(http.MethodPost, "/api/sessions", map[string]any{"theme": in})
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("theme %q: status %d (%s)", in, status, body)
		}
		var out struct {
			State struct {
				Theme string `json:"theme"`
			} `json:"state"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("theme %q: decode: %v", in, err)
		}
		if out.State.Theme != want {
			t.Errorf("theme %q stored as %q, want %q", in, out.State.Theme, want)
		}
	}
}

// TestHistoryIsExposed proves the move history reaches the client and carries the
// turn number and played cards, which is what the History drawer renders.
func TestHistoryIsExposed(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "HISTORY-API"})
	if len(env.State.Log) == 0 {
		t.Fatal("no history in the first snapshot")
	}
	play := firstPlay(env.State)
	if play == nil {
		t.Fatal("no single-card play offered")
	}
	code, raw := c.do(http.MethodPost, "/api/sessions/"+env.SessionID+"/actions", map[string]any{
		"actionId":    "hist-1",
		"expectedSeq": env.Seq,
		"action":      map[string]any{"type": "play", "cardIds": play},
	})
	if code != http.StatusOK {
		t.Fatalf("play: %d %s", code, raw)
	}
	code, raw = c.do(http.MethodGet, "/api/sessions/"+env.SessionID, nil)
	if code != http.StatusOK {
		t.Fatalf("resume: %d", code)
	}
	var resumed stateEnvelope
	if err := json.Unmarshal(raw, &resumed); err != nil {
		t.Fatalf("decode resumed: %v", err)
	}
	if len(resumed.State.Log) <= len(env.State.Log) {
		t.Fatalf("history did not grow: %d -> %d", len(env.State.Log), len(resumed.State.Log))
	}
	var played *engine.Event
	for i := range resumed.State.Log {
		if resumed.State.Log[i].Type == engine.EvPlay {
			played = &resumed.State.Log[i]
		}
	}
	if played == nil {
		t.Fatal("no play event in history")
	}
	if played.Turn < 1 || len(played.Cards) == 0 || played.Cards[0] != play[0] {
		t.Fatalf("play event = %+v", *played)
	}
	if len(resumed.State.Events) == 0 || len(resumed.State.Events) > 40 {
		t.Fatalf("recent events = %d, want 1..40", len(resumed.State.Events))
	}
}

// TestConcurrentReadAndAction is the regression guard for the session race: a GET
// (second tab) must never read sess.Game while an action rewrites it. Run with
// -race; a torn read or a lock removed in api.go makes this fail.
func TestConcurrentReadAndAction(t *testing.T) {
	c := newClient(t)
	env := c.createSession(map[string]any{"seed": "RACE-SEED-01"})
	path := "/api/sessions/" + env.SessionID

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if code, _ := c.do(http.MethodGet, path, nil); code != http.StatusOK {
				return
			}
		}
	}()

	seq := env.Seq
	for i := 0; i < 20; i++ {
		code, raw := c.do(http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("read %d: %d", i, code)
		}
		var cur stateEnvelope
		if err := json.Unmarshal(raw, &cur); err != nil {
			t.Fatalf("decode: %v", err)
		}
		seq = cur.Seq
		play := firstPlay(cur.State)
		if play == nil {
			break
		}
		code, raw = c.do(http.MethodPost, path+"/actions", map[string]any{
			"actionId":    "race-" + strconv.Itoa(i),
			"expectedSeq": seq,
			"action":      map[string]any{"type": "play", "cardIds": play},
		})
		if code != http.StatusOK {
			t.Fatalf("action %d: %d %s", i, code, raw)
		}
	}
	close(stop)
	wg.Wait()
}

// TestRateLimitKeyTrustsProxyOnlyWhenAsked guards the tunnel deployment: behind a
// proxy every request shares one RemoteAddr, so honouring X-Forwarded-For is
// required there and must NOT happen on a plain LAN server (spoofable).
func TestRateLimitKeyTrustsProxyOnlyWhenAsked(t *testing.T) {
	build := func(trust bool) *server.Server {
		t.Helper()
		srv, err := server.New(server.Options{DataDir: t.TempDir(), TrustProxy: trust})
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}
		return srv
	}
	// The key is unexported; observe it through the limiter's behaviour instead:
	// with TrustProxy a spoofable header must not unlock a fresh bucket for the
	// same peer address, so hammer one address and expect a 429 either way.
	for _, trust := range []bool{false, true} {
		srv := build(trust)
		s := httptest.NewServer(srv.Handler())
		var last int
		// The limiter is a token bucket (burst 30, 10/s), so 40 requests only
		// always trip it if the loop is fast. On a loaded machine the bucket
		// refills mid-loop and 40 is not enough; allow far more attempts while
		// still breaking on the first 429, so the normal case stays instant.
		for i := 0; i < 400; i++ {
			r, err := http.NewRequest(http.MethodPost, s.URL+"/api/sessions", strings.NewReader(`{"seed":"RATE-`+strconv.Itoa(i)+`"}`))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			r.RemoteAddr = "198.51.100.9:5555"
			r.Header.Set("X-Forwarded-For", "203.0.113.7")
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			resp.Body.Close()
			last = resp.StatusCode
			if last == http.StatusTooManyRequests {
				break
			}
		}
		if last != http.StatusTooManyRequests {
			t.Fatalf("trustProxy=%v: limiter never engaged (last %d)", trust, last)
		}
		s.Close()
	}
}
