package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"regicide/engine"
)

// maxBody bounds request bodies; every payload is tiny.
const maxBody = 8 << 10

// sessionResponse is the shape every session endpoint returns.
type sessionResponse struct {
	SessionID string             `json:"sessionId"`
	Seq       int                `json:"seq"`
	State     engine.ClientState `json:"state"`
	Events    []engine.Event     `json:"events,omitempty"`
}

type createRequest struct {
	Seed  string              `json:"seed"`
	Theme string              `json:"theme"`
	Rules *engine.RuleOptions `json:"rules"`
	// Jokers is the browser's Jester pair request. Custom says the player picked
	// it by hand, which outranks the set's generated pair.
	Jokers       []string `json:"jokers"`
	JokersCustom bool     `json:"jokersCustom"`
}

type actionRequest struct {
	ActionID    string          `json:"actionId"`
	ExpectedSeq *int            `json:"expectedSeq"`
	Action      json.RawMessage `json:"action"`
}

type submitRequest struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

// handleSessionsCreate handles POST /api/sessions.
func (s *Server) handleSessionsCreate(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow(s.clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		return
	}
	var req createRequest
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rules := engine.DefaultRules()
	if req.Rules != nil {
		rules = *req.Rules
	}
	seed := strings.TrimSpace(req.Seed)
	if seed != "" && !engine.ValidSeed(seed) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seed must be 1-32 letters or digits (hyphens and spaces are ignored)"})
		return
	}
	sess, err := s.store.CreateWith(seed, s.themeOrDefault(req.Theme), rules, JokerPick{Pair: req.Jokers, Custom: req.JokersCustom})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not start a game"})
		return
	}
	s.sessionsTotal.Add(1)
	seq, state := s.store.View(sess)
	writeJSON(w, http.StatusCreated, sessionResponse{SessionID: sess.ID, Seq: seq, State: state})
}

// handleSessionRoutes dispatches /api/sessions/{id} and its sub-paths.
func (s *Server) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	sess, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	// A co-op table has its own routes, and every guarantee it makes — seat tokens,
	// the turn gate, the withheld seed, the host-only replay — lives there. The solo
	// routes key on a static session id, so accepting a room here would bypass all of
	// them: the id is a capability, not a credential (F-30).
	if _, _, isRoom := s.store.roomFacts(sess); isRoom {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	switch {
	case len(parts) == 1:
		switch r.Method {
		case http.MethodGet:
			seq, state := s.store.View(sess)
			writeJSON(w, http.StatusOK, sessionResponse{SessionID: sess.ID, Seq: seq, State: state})
		case http.MethodDelete:
			s.store.Delete(id)
			w.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(w, "GET, DELETE")
		}
	case len(parts) == 2 && parts[1] == "actions":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		s.handleAction(w, r, sess)
	case len(parts) == 3 && parts[1] == "replay" && parts[2] == "ticket":
		// The browser viewer cannot send a session header either. A solo session id is
		// already the credential for its actions and its log, so it may mint the same
		// short-lived viewer ticket the co-op host does (D-47).
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		ticket, seconds := s.store.ReplayTicket(sess)
		writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "seconds": seconds})
	case len(parts) == 2 && parts[1] == "replay":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		raw, ok := s.store.replayFile(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no replay recorded"})
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="regicide-`+id[:8]+`.jsonl"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	case len(parts) == 2 && parts[1] == "restart":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req createRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rules := s.store.RuleSet(sess)
		if req.Rules != nil {
			rules = *req.Rules
		}
		seed := strings.TrimSpace(req.Seed)
		if seed != "" && !engine.ValidSeed(seed) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seed must be 1-32 letters or digits (hyphens and spaces are ignored)"})
			return
		}
		if err := s.store.RestartWith(sess, seed, s.themeOrDefault(req.Theme), rules, JokerPick{Pair: req.Jokers, Custom: req.JokersCustom}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not restart"})
			return
		}
		seq, state := s.store.View(sess)
		writeJSON(w, http.StatusOK, sessionResponse{SessionID: sess.ID, Seq: seq, State: state})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, sess *Session) {
	var req actionRequest
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	act, err := decodeAction(req.Action)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Without an explicit expectedSeq, fall back to the server's current seq
	// rather than disabling the check: a second tab must not slip a stale move in.
	expected := s.store.Sequence(sess)
	if req.ExpectedSeq != nil {
		expected = *req.ExpectedSeq
	}

	seq, events, _, err := s.store.Apply(sess, req.ActionID, expected, act)
	switch {
	case errors.Is(err, ErrStaleSeq):
		_, state := s.store.View(sess)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "stale",
			"seq":   seq,
			"state": state,
		})
		return
	case errors.Is(err, engine.ErrIllegal):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not apply the action"})
		return
	}

	_, state := s.store.View(sess)
	writeJSON(w, http.StatusOK, sessionResponse{
		SessionID: sess.ID,
		Seq:       seq,
		State:     state,
		Events:    events,
	})
}

// decodeAction translates the wire action into an engine action.
// decodeAction accepts the client's wire form by handing it to the engine's own codec.
// It used to restate every field here, so the HTTP surface and the replay reader were
// two independent parsers of the same JSON: an action could be accepted over the wire
// and be unreadable in its own log, or read from a log and be refused by the server.
func decodeAction(raw json.RawMessage) (engine.Action, error) {
	return engine.UnmarshalAction(raw)
}

// handleLeaderboard serves GET (top lists) and POST (submit a win).
func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}
		top := s.store.Top(limit)
		out := map[string]any{
			"gold":   orEmpty(top[engine.TierGold]),
			"silver": orEmpty(top[engine.TierSilver]),
			"bronze": orEmpty(top[engine.TierBronze]),
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		if !s.limits.allow(s.clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
			return
		}
		var req submitRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		sess, ok := s.store.Get(req.SessionID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		// A co-op table has no leaderboard by rule, not by accident (D-36), and the
		// solo route must not become the way around that either (F-30).
		if _, _, isRoom := s.store.roomFacts(sess); isRoom {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": ErrNotEligible.Error()})
			return
		}
		entry, rank, err := s.store.Submit(sess, req.Name)
		switch {
		case errors.Is(err, ErrBadName):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrBadName.Error()})
		case errors.Is(err, ErrAlreadyClaimed):
			writeJSON(w, http.StatusConflict, map[string]string{"error": ErrAlreadyClaimed.Error()})
		case errors.Is(err, ErrNotEligible):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": ErrNotEligible.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the score"})
		default:
			writeJSON(w, http.StatusCreated, map[string]any{"entry": entry, "rank": rank})
		}
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func orEmpty(e []Entry) []Entry {
	if e == nil {
		return []Entry{}
	}
	return e
}

// The theme whitelist is read from the embedded registry, not hand-maintained: a
// set that appears in themes.json is valid by definition (the registry is built
// from the art on disk), so adding a set needs no Go change and the two cannot
// drift apart.
var (
	themeIDsOnce sync.Once
	themeIDs     []string
)

// registryThemeIDs reads the registry that this server actually serves: the
// on-disk file when -web is set (a dev run may regenerate it), the embedded copy
// otherwise. The caller gets a copy, so no handler can mutate the cache.
func (s *Server) registryThemeIDs() []string {
	if s.opts.WebDir != "" {
		if raw, err := os.ReadFile(filepath.Join(s.opts.WebDir, "data", "themes.json")); err == nil {
			if ids, ok := parseThemeIDs(raw); ok {
				return ids
			}
		}
		return s.embeddedThemeIDs()
	}
	return s.embeddedThemeIDs()
}

func (s *Server) embeddedThemeIDs() []string {
	themeIDsOnce.Do(func() {
		themeIDs = []string{"official"}
		raw, err := fs.ReadFile(webFS, "web/data/themes.json")
		if err != nil {
			return
		}
		if ids, ok := parseThemeIDs(raw); ok {
			themeIDs = ids
		}
	})
	return append([]string(nil), themeIDs...)
}

// parseThemeIDs pulls the ids out of a themes.json document.
func parseThemeIDs(raw []byte) ([]string, bool) {
	var doc struct {
		Themes []struct {
			ID string `json:"id"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(doc.Themes))
	for _, t := range doc.Themes {
		if t.ID != "" {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

func (s *Server) themeOrDefault(theme string) string {
	theme = strings.ToLower(strings.TrimSpace(theme))
	for _, id := range s.registryThemeIDs() {
		if id == theme {
			return theme
		}
	}
	return "official"
}

func decodeBody(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	// One value, and nothing after it: `{} {}` used to be accepted as `{}`.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid JSON body")
	}
	return nil
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

// clientIP extracts the rate-limiting key. Forwarded headers are only honoured
// when the operator says a trusted proxy sits in front (Options.TrustProxy);
// otherwise a LAN client could spoof X-Forwarded-For and dodge the limiter.
func (s *Server) clientIP(r *http.Request) string {
	if s.opts.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first := strings.TrimSpace(strings.Split(fwd, ",")[0])
			if first != "" {
				return first
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
