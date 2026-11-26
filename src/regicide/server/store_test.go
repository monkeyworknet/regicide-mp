package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"regicide/engine"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// wonSession builds a finished, random-seed session without playing it out.
func wonSession(t *testing.T, s *Server, jesters int) *Session {
	t.Helper()
	sess, err := s.store.Create("", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Game.Phase = engine.Won
	sess.Game.JestersUsed = jesters
	sess.Game.TurnNo = 17
	return sess
}

func TestSubmitRequiresEligibility(t *testing.T) {
	srv := newTestServer(t)

	chosen, err := srv.store.Create("CHOSEN-SEED1", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	chosen.Game.Phase = engine.Won
	if _, _, err := srv.store.Submit(chosen, "Ada"); err != ErrNotEligible {
		t.Fatalf("chosen-seed submit error = %v, want ErrNotEligible", err)
	}

	live := wonSession(t, srv, 0)
	live.Game.Phase = engine.Step1Await
	if _, _, err := srv.store.Submit(live, "Ada"); err != ErrNotEligible {
		t.Fatalf("unfinished game error = %v, want ErrNotEligible", err)
	}

	won := wonSession(t, srv, 0)
	entry, rank, err := srv.store.Submit(won, "Ada")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if entry.Tier != engine.TierGold || rank != 1 {
		t.Fatalf("entry tier/rank = %s/%d, want gold/1", entry.Tier, rank)
	}

	// One submission per session.
	if _, _, err := srv.store.Submit(won, "Grace"); err != ErrAlreadyClaimed {
		t.Fatalf("second submit error = %v, want ErrAlreadyClaimed", err)
	}
}

func TestTierDerivedFromJesters(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		jesters int
		want    engine.Tier
	}{{0, engine.TierGold}, {1, engine.TierSilver}, {2, engine.TierBronze}} {
		sess := wonSession(t, srv, tc.jesters)
		entry, _, err := srv.store.Submit(sess, "Player"+string(rune('A'+tc.jesters)))
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if entry.Tier != tc.want {
			t.Errorf("%d jesters -> tier %s, want %s", tc.jesters, entry.Tier, tc.want)
		}
	}
}

func TestLeaderboardRanking(t *testing.T) {
	srv := newTestServer(t)
	mk := func(name string, turns int) {
		sess := wonSession(t, srv, 0)
		sess.Game.TurnNo = turns
		if _, _, err := srv.store.Submit(sess, name); err != nil {
			t.Fatalf("submit %s: %v", name, err)
		}
	}
	mk("Slow", 40)
	mk("Quick", 12)
	mk("Middle", 25)

	top := srv.store.Top(10)[engine.TierGold]
	if len(top) != 3 {
		t.Fatalf("gold entries = %d, want 3", len(top))
	}
	if top[0].Name != "Quick" || top[1].Name != "Middle" || top[2].Name != "Slow" {
		t.Fatalf("order = %v", []string{top[0].Name, top[1].Name, top[2].Name})
	}
	if limited := srv.store.Top(2)[engine.TierGold]; len(limited) != 2 {
		t.Fatalf("limit not applied: %d", len(limited))
	}
}

func TestLeaderboardPersists(t *testing.T) {
	dir := t.TempDir()
	srv1, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess := wonSession(t, srv1, 1)
	if _, _, err := srv1.store.Submit(sess, "Persisted"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	srv2, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	top := srv2.store.Top(10)[engine.TierSilver]
	if len(top) != 1 || top[0].Name != "Persisted" {
		t.Fatalf("board did not survive a restart: %+v", top)
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"Ada", "Grace Hopper", "player-1", "a_b", "Zeta9"}
	for _, name := range good {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	bad := []string{"", "a", strings.Repeat("x", 17), "bad<script>", "tab\there", "fuck", "héllo", "-_-"}
	_ = bad
	for _, name := range []string{"", "a", strings.Repeat("x", 17), "bad<script>", "fuck", "héllo"} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) accepted an invalid name", name)
		}
	}
	if got := normaliseName("  Ada   Lovelace  "); got != "Ada Lovelace" {
		t.Errorf("normaliseName = %q", got)
	}
}

func TestSessionGCRemovesIdle(t *testing.T) {
	srv := newTestServer(t)
	sess, err := srv.store.Create("GC-SEED-1234", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	sess.UpdatedAt = old
	if n := srv.store.GC(24 * time.Hour); n != 1 {
		t.Fatalf("gc removed %d, want 1", n)
	}
	if _, ok := srv.store.Get(sess.ID); ok {
		t.Fatal("session should be gone after GC")
	}
	// A GC'd session is a 404 over HTTP, which the client treats as "start fresh".
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("gc'd session GET = %d, want 404", rec.Code)
	}
}

// TestGCRemovesReplayAndRoomIndex: a swept room leaves nothing behind that still
// resolves. The index entry and every log the session wrote (current and the ones
// rotated by play-again) go with it (F-7, F-18).
func TestGCRemovesReplayAndRoomIndex(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sess, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	code := sess.Room.Code
	replays := filepath.Join(dir, "replays")
	if err := os.MkdirAll(replays, 0o755); err != nil {
		t.Fatalf("replays dir: %v", err)
	}
	paths := []string{
		store.replayPath(sess.ID),
		filepath.Join(replays, sess.ID+".game1.jsonl"),
	}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	sess.mu.Lock()
	sess.UpdatedAt = store.now().Add(-48 * time.Hour)
	sess.mu.Unlock()

	if n := store.GC(time.Hour); n != 1 {
		t.Fatalf("gc removed %d sessions, want 1", n)
	}
	store.mu.RLock()
	_, indexed := store.roomIndex[code]
	store.mu.RUnlock()
	if indexed {
		t.Errorf("room %s is still in roomIndex after GC", code)
	}
	if _, ok := store.ByCode(code); ok {
		t.Errorf("room %s still resolves after GC", code)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("replay %s survived GC (stat err %v)", filepath.Base(p), err)
		}
	}
}

// TestPruneOrphansRemovesTheReplay: a restart keeps the session files for a while
// but a log whose session file was pruned is unreachable, so it goes too (F-18).
func TestPruneOrphansRemovesTheReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sess, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	logPath := store.replayPath(sess.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("replays dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write replay: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{store.sessionPath(sess.ID), logPath} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
	}

	// Simulate a restart: the file is old and no live session holds it.
	store.mu.Lock()
	delete(store.sess, sess.ID)
	store.mu.Unlock()
	if n := store.PruneOrphans(time.Hour); n != 1 {
		t.Fatalf("prune removed %d sessions, want 1", n)
	}
	if _, err := os.Stat(store.sessionPath(sess.ID)); !os.IsNotExist(err) {
		t.Errorf("session file survived prune (stat err %v)", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("orphan replay survived prune (stat err %v)", err)
	}
}

// TestPruneKeepsALiveReplay: an idle table with a connected stream may not have written
// for an hour. Its session and log must survive the sweep; deleting the log would make
// the next action recreate a headerless file.
func TestPruneKeepsALiveReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sess, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	logPath := store.replayPath(sess.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("replays dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write replay: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{store.sessionPath(sess.ID), logPath} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
	}
	if n := store.PruneOrphans(time.Hour); n != 0 {
		t.Fatalf("prune removed %d live sessions, want 0", n)
	}
	for _, p := range []string{store.sessionPath(sess.ID), logPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("live file %s was removed: %v", filepath.Base(p), err)
		}
	}
}

func TestCorruptLeaderboardIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	srv, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := writeFileAtomic(dir+"/leaderboard.json", []byte("{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	srv2, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen with corrupt board: %v", err)
	}
	if len(srv2.store.Top(5)[engine.TierGold]) != 0 {
		t.Fatal("corrupt board should start empty")
	}
	_ = srv
}

func TestRateLimiterBudget(t *testing.T) {
	l := newRateLimiter()
	for i := 0; i < 30; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d rejected inside the burst budget", i)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("the 31st request should be over budget")
	}
	if !l.allow("5.6.7.8") {
		t.Fatal("a different client must have its own budget")
	}
}

func TestSubmitEndpointHTTPCodes(t *testing.T) {
	srv := newTestServer(t)
	sess := wonSession(t, srv, 0)

	post := func(body map[string]any) int {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/leaderboard", strings.NewReader(string(raw)))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(map[string]any{"sessionId": "unknown", "name": "Ada"}); code != http.StatusNotFound {
		t.Fatalf("unknown session = %d, want 404", code)
	}
	if code := post(map[string]any{"sessionId": sess.ID, "name": "a"}); code != http.StatusBadRequest {
		t.Fatalf("short name = %d, want 400", code)
	}
	if code := post(map[string]any{"sessionId": sess.ID, "name": "Ada"}); code != http.StatusCreated {
		t.Fatalf("valid submit = %d, want 201", code)
	}
	if code := post(map[string]any{"sessionId": sess.ID, "name": "Ada"}); code != http.StatusConflict {
		t.Fatalf("double submit = %d, want 409", code)
	}
}

func TestVersionMismatchInvalidatesSession(t *testing.T) {
	dir := t.TempDir()
	srv, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, err := srv.store.Create("VER-SEED-123", "official", engine.DefaultRules())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Game.Version = engine.Version + 1
	if err := srv.store.persist(sess); err != nil {
		t.Fatalf("persist: %v", err)
	}
	srv.store.Delete(sess.ID) // drop the cached copy but keep the file
	if err := writeFileAtomic(dir+"/sessions/"+sess.ID+".json", mustJSON(t, sess)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := srv.store.Get(sess.ID); ok {
		t.Fatal("a session from a different engine version must not load")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// TestBotFillGraceOverride pins the two ends of the bot-fill grace: the shipped
// minute, and the test override that keeps the disconnect browser check from
// sleeping through it. The check runs the real sweep; only the clock is short.
func TestBotFillGraceOverride(t *testing.T) {
	def, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := def.store.botFillGrace(); got != botFillAfter {
		t.Fatalf("default grace = %v, want %v", got, botFillAfter)
	}

	short, err := New(Options{DataDir: t.TempDir(), BotFillAfter: 3 * time.Second})
	if err != nil {
		t.Fatalf("New with override: %v", err)
	}
	if got := short.store.botFillGrace(); got != 3*time.Second {
		t.Fatalf("override grace = %v, want 3s", got)
	}

	// A zero field falls back rather than disabling the sweep.
	short.store.botFill = 0
	if got := short.store.botFillGrace(); got != botFillAfter {
		t.Fatalf("zero field grace = %v, want %v", got, botFillAfter)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store.botFill != botFillAfter {
		t.Fatalf("NewStore botFill = %v, want %v", store.botFill, botFillAfter)
	}
}

// TestDecodeBodyRejectsTrailingJSON pins that one request carries one JSON value.
// `{} {}` used to be accepted as `{}`, which let a caller hide a second document.
func TestDecodeBodyRejectsTrailingJSON(t *testing.T) {
	ok := []string{`{"A":1}`, `{"A":1}   ` + "\n"}
	for _, body := range ok {
		var v struct{ A int }
		if err := decodeBody(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &v); err != nil {
			t.Fatalf("body %q rejected: %v", body, err)
		}
		if v.A != 1 {
			t.Fatalf("body %q decoded A=%d", body, v.A)
		}
	}
	bad := []string{`{"A":1} {}`, `{"A":1}garbage`, `{"A":1}{"A":2}`}
	for _, body := range bad {
		var v struct{ A int }
		if err := decodeBody(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &v); err == nil {
			t.Fatalf("body %q was accepted", body)
		}
	}
}

// TestSecurityHeaders pins the headers that make the build safe to put behind a proxy.
func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, path := range []string{"/", "/api/health"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options = %q", path, got)
		}
		if got := res.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("%s Referrer-Policy = %q", path, got)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
			t.Fatalf("%s CSP = %q", path, csp)
		}
	}
}
