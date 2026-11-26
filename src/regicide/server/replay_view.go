package server

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"regicide/engine"
	"regicide/replay"
)

// The browser viewer asks for one step at a time. Rebuilding from the seed is the only
// honest way to render a past position without shipping the engine to JavaScript, and a
// game is a few hundred steps: rebuilding to step N costs microseconds.
//
// Everything here is behind a ticket the host minted, and the ticket is the only
// credential in the URL (D-47).

// handleReplayView serves GET /api/replay/{ticket}?step=N[&as=SEAT].
func (s *Server) handleReplayView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	ticket := strings.TrimPrefix(r.URL.Path, "/api/replay/")
	if ticket == "" || strings.Contains(ticket, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	sess, ok := s.store.SessionByReplayTicket(ticket)
	if !ok {
		s.log.Printf("replay view: ticket %q did not resolve", ticket)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "ticket_expired"})
		return
	}
	if !s.store.flushWrites(sess) {
		// Serving a log that is still being written would hand the viewer half a game
		// and call it the game.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "log_busy"})
		return
	}
	raw, ok := s.store.replayFile(sess.ID)
	if !ok {
		s.log.Printf("replay view: no log for %s (%d bytes)", sess.ID, len(raw))
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no replay recorded"})
		return
	}
	rep, err := replay.Read(bytes.NewReader(raw))
	if err != nil {
		s.log.Printf("replay view: read %s: %v", sess.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "replay_unreadable"})
		return
	}
	// Rebuild the opening position and apply steps up to the one asked for.
	step := len(rep.Steps)
	if v := r.URL.Query().Get("step"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad step"})
			return
		}
		step = min(n, len(rep.Steps))
	}
	game, err := rep.Build()
	if err != nil {
		s.log.Printf("replay view: build %s: %v", sess.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "replay_invalid"})
		return
	}
	// The viewer shows "the move" for the current step: which seat acted, what it
	// asked for, and the events that followed (damage, a defeat, a draw). Rebuilding
	// already walks every step, so the last one keeps its events at no extra cost.
	var action map[string]any
	var lastEvents []engine.Event
	for i := 0; i < step; i++ {
		act, err := engine.UnmarshalAction(rep.Steps[i].Action)
		if err != nil {
			s.log.Printf("replay view: action %s step %d: %v", sess.ID, i, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "replay_invalid"})
			return
		}
		rng := engine.NewRandAt(game.Seed, game.RGNDraws)
		next, events, err := engine.Transition(game, act, rng)
		if err != nil {
			s.log.Printf("replay view: transition %s step %d: %v", sess.ID, i, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "replay_invalid"})
			return
		}
		game = next
		action = replayActionView(rep.Steps[i].Seat, act)
		lastEvents = events
	}
	// The viewer may look through any seat: it is the host's own analysis tool.
	as := -1
	if v := r.URL.Query().Get("as"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < len(game.Players) {
			as = n
		}
	}
	state := engine.SnapshotFor(game, engine.View{Seat: as, OpenHands: false})
	state.Legal = []engine.ActionView{}
	// Names come from the seat book, matched by seat: a book written in any order must
	// not shuffle the names handed to the viewer.
	names := make(map[int]string, len(rep.Header.Players))
	for _, p := range rep.Header.Players {
		names[p.Seat] = p.Name
	}
	for i := range state.Players {
		if name, ok := names[state.Players[i].Seat]; ok && name != "" {
			state.Players[i].Name = name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":   rep.Header.Room,
		"step":   step,
		"steps":  len(rep.Steps),
		"as":     as,
		"state":  state,
		"result": rep.Result,
		"action": action,
		"events": lastEvents,
		"header": map[string]any{
			"theme": rep.Header.Theme, "seed": rep.Header.Seed,
			"jokers":     rep.Header.Jokers,
			"numPlayers": rep.Header.NumPlayers, "players": rep.Header.Players,
		},
	})
}

// replayActionView is the wire action, labelled for the viewer: the seat, the kind of
// move, the cards it named, and (for a Jester) the seat it picked.
func replayActionView(seat int, a engine.Action) map[string]any {
	out := map[string]any{"seat": seat, "type": engine.ActionName(a)}
	ids := func(list []engine.CardID) []string {
		out := make([]string, 0, len(list))
		for _, id := range list {
			out = append(out, string(id))
		}
		return out
	}
	switch act := a.(type) {
	case engine.Play:
		out["cards"] = ids(act.CardIDs)
	case engine.PayDamage:
		out["cards"] = ids(act.IDs())
	case engine.ChooseNextSeat:
		out["seatTarget"] = act.Seat
	}
	return out
}
