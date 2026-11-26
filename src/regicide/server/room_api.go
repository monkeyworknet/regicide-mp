package server

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"regicide/engine"
)

// roomResponse is the shape every room endpoint returns. The seat token appears
// exactly once, in the create and join answers: it is the only copy that will ever
// exist outside the browser that holds it.
type roomResponse struct {
	Code string `json:"code"`
	Seat int    `json:"seat"`
	// WatchID is present only for a spectator: it is not a seat token and can never
	// act.
	WatchID string `json:"watchId,omitempty"`
	// Spectator marks a read-only client; WatchingAs is the seat whose board it is
	// looking at, or -1 for the table view.
	Spectator  bool                `json:"spectator,omitempty"`
	WatchingAs int                 `json:"watchingAs"`
	Token      string              `json:"token,omitempty"`
	Seq        int                 `json:"seq"`
	Lobby      bool                `json:"lobby"`
	Room       *publicRoom         `json:"room,omitempty"`
	State      *engine.ClientState `json:"state,omitempty"`
}

// publicRoom is the client-safe room book: names and readiness, never a token hash.
type publicRoom struct {
	Code     string `json:"code"`
	HostSeat int    `json:"hostSeat"`
	// Capacity is the waiting-room size; Players is how many have actually sat down,
	// and that is the number the rules are dealt from.
	Capacity  int  `json:"capacity"`
	Players   int  `json:"players"`
	OpenHands bool `json:"openHands"`
	Lobby     bool `json:"lobby"`
	Startable bool `json:"startable"`
	// Cancelled ends the table for everyone; the reason travels with it so the other
	// seats see "the host closed the table" instead of a missing room.
	Cancelled bool `json:"cancelled,omitempty"`
	// WaitingFor names the away seat whose turn it is, which is exactly when the
	// table is stopped and the host has to choose (D-42).
	WaitingFor string `json:"waitingFor,omitempty"`
	// WaitingLeft says the seat named by WaitingFor left on purpose rather than
	// going quiet, so the banner does not promise a return that will not happen.
	WaitingLeft bool `json:"waitingLeft,omitempty"`
	// Watchers is how many spectators are connected, AllowWatch is the host's switch
	// for them, and WatchLink is the shareable spectator URL: no key, because a
	// spectator has no seat to protect (D-43).
	Watchers int `json:"watchers"`
	// WatchNames labels each connected spectator in arrival order. The menu lists them
	// beside the count; a spectator has no seat and no hand, so a label is all there
	// is to show (T2).
	WatchNames []string `json:"watchNames,omitempty"`
	AllowWatch bool     `json:"allowWatch"`
	// WatchHands tells a spectator whether the seat rail is clickable: with it on,
	// tapping a player redraws the board as that player (D-45).
	WatchHands bool `json:"watchHands"`
	// BotFill is the room's 60-second disconnect recovery switch, and the switch
	// that gives a left or kicked seat a bot at once.
	BotFill   bool   `json:"botFill"`
	WatchLink string `json:"watchLink,omitempty"`
	// ReplayDegraded says the game log fell behind: the download will be short, and
	// the host should know that before they rely on it (F-8).
	ReplayDegraded bool             `json:"replayDegraded,omitempty"`
	Seats          []publicRoomSeat `json:"seats"`
	Themes         []string         `json:"themes"`
	// Theme is the card set this room will deal (in the lobby) or is playing. The
	// host's switch in the lobby reads it, so the tile that says "official" is the
	// deck the deal will actually use.
	Theme string             `json:"theme"`
	Rules engine.RuleOptions `json:"rules"`
	// NextRules is the setup table for the seats taken right now: hand limit and
	// Jesters in the tavern both change with the count, so the lobby shows it.
	NextRules nextRules `json:"nextRules"`
}

// nextRules is what a deal would use at this moment.
type nextRules struct {
	Players         int `json:"players"`
	MaxHand         int `json:"maxHand"`
	JestersInTavern int `json:"jestersInTavern"`
}

type publicRoomSeat struct {
	Seat  int    `json:"seat"`
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	You   bool   `json:"you"`
	Away  bool   `json:"away"`
	// IdleSeconds is how long the seat has been silent. The table waits for an away
	// seat; the host may take it over after five minutes.
	IdleSeconds int  `json:"idleSeconds"`
	Takeable    bool `json:"takeable"`
	// Bot says a bot holds this seat. Everyone sees it, not just the host.
	Bot bool `json:"bot,omitempty"`
	// Left says the player chose to leave a dealt game. The seat stays (the rules
	// never shrink) and is labelled differently from an away seat.
	Left bool `json:"left,omitempty"`
	// RejoinLive is true while a reconnect link the host minted is still usable.
	// The link itself is never sent back: the host's browser keeps it.
	RejoinLive bool `json:"rejoinLive,omitempty"`
	// RejoinSeconds is the remaining life of that link, for the host's sheet.
	RejoinSeconds int `json:"rejoinSeconds,omitempty"`
}

type createRoomRequest struct {
	Theme     string              `json:"theme"`
	Rules     *engine.RuleOptions `json:"rules"`
	OpenHands bool                `json:"openHands"`
	Seed      string              `json:"seed"`
	// Jokers is the host browser's Jester pair; the deal resolves it against the
	// theme the table plays. Custom marks a hand pick.
	Jokers       []string `json:"jokers"`
	JokersCustom bool     `json:"jokersCustom"`
}

// startRequest carries the sequence the host's screen was showing. Required: a join
// racing the deal could otherwise land a seat the host never saw (T5).
type startRequest struct {
	ExpectedSeq *int `json:"expectedSeq"`
}

// hostRequest asks the host to hand the crown to a seat (D-60).
type hostRequest struct {
	Seat int `json:"seat"`
}

// botRequest asks the host to hand a seat to (or take it back from) the bot.
type botRequest struct {
	Seat int  `json:"seat"`
	On   bool `json:"on"`
}

type readyRequest struct {
	Ready bool `json:"ready"`
}

type reclaimRequest struct {
	Key string `json:"key"`
}

type watchingRequest struct {
	Allowed bool `json:"allowed"`
	// Hands lets a spectator view the board as any player, one at a time (D-45).
	Hands bool `json:"hands"`
}

// themeRequest is the lobby's card-set choice, host only and frozen at the deal.
type themeRequest struct {
	Theme string `json:"theme"`
	// Jokers lets the host's browser re-send its Jester pair with the deck change:
	// a generated pair follows the new set, a hand pick is kept.
	Jokers       []string `json:"jokers"`
	JokersCustom bool     `json:"jokersCustom"`
}

// rulesRequest is the lobby's table settings: the same switches a solo game offers.
type rulesRequest struct {
	Rules engine.RuleOptions `json:"rules"`
}

// botFillRequest turns the 60-second disconnect recovery on or off.
type botFillRequest struct {
	On bool `json:"on"`
}

// viewRoom projects a room for one seat. A token hash must never leave the server.
// viewRoomLocked builds the room view. The caller must hold sess.mu: it reads the
// seat book, the watcher map and the pending rules (F-3).
func viewRoomLocked(sess *Session, viewer int, now time.Time) *publicRoom {
	if sess.Room == nil {
		return nil
	}
	players := len(sess.Room.Seats)
	activeSeat := sess.Game.ActiveSeat
	if sess.Game.SeatChoice != nil {
		activeSeat = sess.Game.SeatChoice.By
	}
	out := &publicRoom{
		Code:     sess.Room.Code,
		HostSeat: sess.Room.HostSeat,
		Capacity: roomCapacity,
		Players:  players,
		Lobby:    sess.Room.Lobby,
		Themes:   coopThemeOrder,
		Seats:    make([]publicRoomSeat, 0, players),
	}
	if sess.Pending != nil {
		out.OpenHands = sess.Pending.OpenHands
		out.Rules = sess.Pending.Rules
		out.Theme = sess.Pending.Theme
	} else if sess.Room != nil {
		out.Theme = sess.Game.Theme
	}
	out.NextRules = nextRules{
		Players:         players,
		MaxHand:         engine.MaxHandSize(players),
		JestersInTavern: engine.JestersInTavern(players),
	}
	out.Cancelled = sess.Room.Cancelled
	out.AllowWatch = sess.Room.AllowWatch
	out.WatchHands = sess.Room.WatchHands
	out.BotFill = sess.Room.BotFill
	// A degraded log is the host's problem to notice before a download surprises
	// them, so the room says it (F-8).
	out.ReplayDegraded = sess.degraded
	out.Watchers = len(sess.watchers)
	// Arrival order, so the list and the count agree and do not reshuffle on every
	// frame. Watchers are anonymous: the label is the whole identity they have.
	if len(sess.watchers) > 0 {
		names := make([]*Watcher, 0, len(sess.watchers))
		for _, w := range sess.watchers {
			names = append(names, w)
		}
		sort.Slice(names, func(i, j int) bool {
			if names[i].JoinedAt.Equal(names[j].JoinedAt) {
				return names[i].ID < names[j].ID
			}
			return names[i].JoinedAt.Before(names[j].JoinedAt)
		})
		out.WatchNames = make([]string, len(names))
		for i := range names {
			out.WatchNames[i] = "Spectator " + strconv.Itoa(i+1)
		}
	}
	if sess.Room.AllowWatch {
		out.WatchLink = "/?room=" + sess.Room.Code + "&watch=1"
	}
	if sess.Room.Lobby && players >= 2 {
		ready := true
		for _, seat := range sess.Room.Seats {
			if !seat.Ready {
				ready = false
			}
		}
		out.Startable = ready
	}
	for _, seat := range sess.Room.Seats {
		idle := now.Sub(seat.LastSeen)
		// The table is stopped when the seat that must act is the one that went
		// quiet (or left); that is the moment the host is offered the choices.
		if seat.Seat == activeSeat && !sess.Room.Lobby && (seat.Left || idle > awayAfter) {
			out.WaitingFor = seat.Name
			out.WaitingLeft = seat.Left
		}
		// Zero when no link was ever minted: Sub on the zero time would report a huge
		// negative and the check below would read as "live".
		rejoinLeft := 0
		if !seat.RejoinExpiry.IsZero() {
			rejoinLeft = int(seat.RejoinExpiry.Sub(now).Seconds())
		}
		// Whether a seat has a live reconnect link, and whether it could be taken
		// over, are the host's business: the invite and take routes are host-only,
		// so telling everyone else only leaks when a seat is unattended (F-11).
		host := viewer >= 0 && viewer == sess.Room.HostSeat
		row := publicRoomSeat{
			Seat:        seat.Seat,
			Name:        seat.Name,
			Ready:       seat.Ready,
			You:         seat.Seat == viewer,
			Away:        !seat.Left && idle > awayAfter,
			IdleSeconds: int(idle.Seconds()),
			Bot:         seat.Bot,
			Left:        seat.Left,
		}
		if host {
			row.Takeable = seat.Left || idle > takeAfter
			row.RejoinLive = seat.RejoinKeyHash != "" && rejoinLeft > 0
			row.RejoinSeconds = max(rejoinLeft, 0)
		}
		out.Seats = append(out.Seats, row)
	}
	return out
}

// handleRooms handles GET (list) and POST (create) on /api/rooms.
func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"rooms": s.store.Rooms()})
	case http.MethodPost:
		if !s.limits.allow(s.clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
			return
		}
		var req createRoomRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Co-op plays two sets only (D-32). The check is here, not in the picker:
		// a hidden tile is not a rule.
		theme := strings.ToLower(strings.TrimSpace(req.Theme))
		if theme == "" {
			theme = "simple"
		}
		if !CoopTheme(theme) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "coop_theme",
				"detail": "co-op rooms play with the official, simple or 8bit deck only",
			})
			return
		}
		rules := engine.DefaultRules()
		rules.SoloJester = false
		if req.Rules != nil {
			rules = *req.Rules
			// The solo Jester is a single-player power; a room ignores it.
			rules.SoloJester = false
		}
		if req.Seed != "" && !engine.ValidSeed(req.Seed) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad seed"})
			return
		}
		sess, seat, token, err := s.store.CreateRoomWith(theme, rules, req.OpenHands, req.Seed, JokerPick{Pair: req.Jokers, Custom: req.JokersCustom})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// One projection, taken under one lock: the code, the room view and the
		// lobby flag describe the same instant (F-3).
		p := s.store.ProjectFor(sess, seat)
		resp := roomResponse{Code: p.Room.Code, Seat: seat, Token: token, Seq: p.Seq, Lobby: p.Lobby, Room: p.Room}
		if p.HasGame {
			resp.State = &p.State
		}
		writeJSON(w, http.StatusCreated, resp)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

// handleRoomRoutes dispatches /api/rooms/{code} and its sub-paths.
func (s *Server) handleRoomRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/rooms/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	code := parts[0]
	sess, ok := s.store.ByCode(code)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "room not found"})
		return
	}

	// watch is open to anyone with the code: a spectator has no seat, so there is no
	// token to require and nothing to leak (D-43).
	if len(parts) >= 2 && len(parts) <= 3 && parts[1] == "watch" {
		s.handleWatch(w, r, sess)
		return
	}

	// The stream authenticates itself: a seat token or a watch id, never both and
	// never neither. It must be routed before the seat check for exactly that reason.
	if len(parts) == 2 && parts[1] == "stream" {
		s.handleStream(w, r, sess)
		return
	}

	// join and reclaim are the only paths that do not carry a seat token: they are
	// how a device gets one.
	if len(parts) == 2 && parts[1] == "join" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if !s.limits.allow(s.clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
			return
		}
		seat, token, err := s.store.JoinRoom(sess)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		p := s.store.ProjectFor(sess, seat)
		resp := roomResponse{Code: p.Room.Code, Seat: seat, Token: token, Seq: p.Seq, Lobby: p.Lobby, Room: p.Room}
		if p.HasGame {
			resp.State = &p.State
		}
		s.store.Publish(sess, "join")
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if len(parts) == 4 && parts[1] == "seats" && parts[3] == "reclaim" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		target, err := strconv.Atoi(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad seat"})
			return
		}
		var req reclaimRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		token, err := s.store.ReclaimSeat(sess, target, req.Key)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		p := s.store.ProjectFor(sess, target)
		resp := roomResponse{Code: p.Room.Code, Seat: target, Token: token, Seq: p.Seq, Lobby: p.Lobby, Room: p.Room}
		if p.HasGame {
			resp.State = &p.State
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	seat, ok := s.seatFromRequest(sess, r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "seat_required"})
		return
	}

	switch {
	case len(parts) == 1:
		switch r.Method {
		case http.MethodGet:
			s.store.TouchSeat(sess, seat)
			s.writeRoomState(w, sess, seat, "")
		case http.MethodDelete:
			if _, hostSeat, ok := s.store.roomFacts(sess); !ok || seat != hostSeat {
				writeRoomError(w, ErrNotHost)
				return
			}
			// F-25: a table that has been dealt is cancelled, not deleted. Deleting it
			// took the log with it, and the other seats got a bare 404 instead of the
			// reason. A lobby is nobody's record, so it still goes.
			_, _, started := s.store.lobbyFacts(sess)
			if started {
				if err := s.store.Cancel(sess, seat); err != nil {
					writeRoomError(w, err)
					return
				}
				s.store.CloseStreams(sess, "cancelled")
				s.store.Publish(sess, "cancelled")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			s.store.Delete(sess.ID)
			w.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(w, "GET, DELETE")
		}
	case len(parts) == 2 && parts[1] == "ready":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req readyRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetReady(sess, seat, req.Ready); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "ready")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "start":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req startRequest
		if r.ContentLength > 0 {
			if err := decodeBody(r, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		// A deal must say which lobby it is based on, like every other room write.
		// Without it a seat that joined after the host's last view could be dealt in
		// unseen (T5).
		if req.ExpectedSeq == nil || *req.ExpectedSeq < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_seq_required"})
			return
		}
		if err := s.store.Start(sess, seat, *req.ExpectedSeq); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "start")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "again":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req startRequest
		if r.ContentLength > 0 {
			if err := decodeBody(r, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		// Re-dealing is a room write like the deal: it must say which view it is based
		// on, or a stale client can rotate the log it never saw.
		if req.ExpectedSeq == nil || *req.ExpectedSeq < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_seq_required"})
			return
		}
		if err := s.store.PlayAgain(sess, seat, *req.ExpectedSeq); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "again")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "cancel":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if err := s.store.Cancel(sess, seat); err != nil {
			writeRoomError(w, err)
			return
		}
		// Everyone watching the table hears it from the stream, with a reason, rather
		// than noticing on the next poll that the room is gone (T3.3).
		s.store.CloseStreams(sess, "cancelled")
		s.store.Publish(sess, "cancelled")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "watching":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req watchingRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetWatchSettings(sess, seat, req.Allowed, req.Hands); err != nil {
			writeRoomError(w, err)
			return
		}
		// Switching hands off must reach spectators immediately: the stream re-projects
		// each subscriber, so their next state frame has no hand in it (F-12, D-45).
		s.store.Publish(sess, "watching")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 3 && parts[1] == "replay" && parts[2] == "ticket":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		// A replay holds every hand at every moment, so the browser viewer needs the
		// host's authority — but a page cannot send a seat header. The host mints a
		// ticket, and the ticket is the only thing in the URL (D-47).
		if _, hostSeat, ok := s.store.roomFacts(sess); !ok || seat != hostSeat {
			writeRoomError(w, ErrNotHost)
			return
		}
		ticket, seconds := s.store.ReplayTicket(sess)
		writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "seconds": seconds})
	case len(parts) == 2 && parts[1] == "replay":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		// A replay contains every hand at every moment: it is the host's analysis
		// file, not a spectator's stream, and it is never served to a watcher.
		code, hostSeat, ok := s.store.roomFacts(sess)
		if !ok || seat != hostSeat {
			writeRoomError(w, ErrNotHost)
			return
		}
		if !s.store.flushWrites(sess) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "log_busy"})
			return
		}
		raw, ok := s.store.replayFile(sess.ID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no replay recorded"})
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="regicide-`+code+`.jsonl"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	case len(parts) == 2 && parts[1] == "ping":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		s.store.TouchSeat(sess, seat)
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "cursor":
		s.handleCursor(w, r, sess, seat)
	case len(parts) == 2 && parts[1] == "leave":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		// Leaving ends this device's hold on the chair: in a lobby the chair is gone
		// (and everybody above it is renumbered), in a dealt game the seat stays and
		// is marked left. Either way there is no view to answer with.
		if err := s.store.LeaveSeat(sess, seat); err != nil {
			writeRoomError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[1] == "seats":
		if r.Method != http.MethodDelete {
			methodNotAllowed(w, "DELETE")
			return
		}
		target, err := strconv.Atoi(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad seat"})
			return
		}
		// The host removes somebody else. The same chair rules as Leave: a lobby row
		// disappears, a dealt seat is left to a bot or the host's menu.
		if err := s.store.KickSeat(sess, seat, target); err != nil {
			writeRoomError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "addbot":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		seatNo, err := s.store.AddBotSeat(sess, seat)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "join")
		// The answer is the host's own view with the new chair's number beside it: the
		// lobby names the bot it just added without another round trip.
		p := s.store.ProjectFor(sess, seat)
		resp := roomResponse{Code: p.Room.Code, Seat: seat, Seq: p.Seq, Lobby: p.Lobby, Room: p.Room}
		if p.HasGame {
			resp.State = &p.State
		}
		writeJSON(w, http.StatusOK, struct {
			roomResponse
			BotSeat int `json:"botSeat"`
		}{resp, seatNo})
	case len(parts) == 2 && parts[1] == "theme":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req themeRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetRoomThemeWith(sess, seat, req.Theme, JokerPick{Pair: req.Jokers, Custom: req.JokersCustom}); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "theme")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "rules":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req rulesRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetRoomRules(sess, seat, req.Rules); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "rules")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "botfill":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req botFillRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetBotFill(sess, seat, req.On); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "botfill")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "actions":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		s.handleRoomAction(w, r, sess, seat)
	case len(parts) == 2 && parts[1] == "host":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req hostRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.HandOverHost(sess, seat, req.Seat); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "host")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 2 && parts[1] == "bot":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var req botRequest
		if err := decodeBody(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.SetBotSeat(sess, seat, req.Seat, req.On); err != nil {
			writeRoomError(w, err)
			return
		}
		s.store.Publish(sess, "bot")
		s.writeRoomState(w, sess, seat, "")
	case len(parts) == 4 && parts[1] == "seats" && parts[3] == "invite":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		target, err := strconv.Atoi(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad seat"})
			return
		}
		key, expires, err := s.store.SeatInvite(sess, seat, target)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		// The host's browser keeps this link: it is shown in the sheet, copyable, and
		// the server never stores the key in the clear.
		code, _, _ := s.store.roomFacts(sess)
		link := "/?room=" + code + "&seat=" + strconv.Itoa(target) + "&rejoin=" + key
		writeJSON(w, http.StatusOK, map[string]any{
			"link":    link,
			"expires": expires.UTC().Format(time.RFC3339),
			"seconds": int(rejoinTTL.Seconds()),
		})
	case len(parts) == 4 && parts[1] == "seats" && parts[3] == "take":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		target, err := strconv.Atoi(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad seat"})
			return
		}
		// The host hands an away seat to a new device: the old token dies, so two
		// clients can never both hold the seat (D-41).
		token, err := s.store.TakeSeat(sess, seat, target)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		// The answer is the *target's* view with the target's new token: the host's
		// browser is a relay for the new device, and a projection of the host's own
		// seat would bootstrap nothing (F-28).
		s.writeRoomState(w, sess, target, token)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) writeRoomState(w http.ResponseWriter, sess *Session, seat int, token string) {
	p := s.store.ProjectFor(sess, seat)
	resp := roomResponse{Code: p.Room.Code, Seat: seat, Token: token, Seq: p.Seq, Lobby: p.Lobby, Room: p.Room}
	if p.HasGame {
		resp.State = &p.State
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRoomAction runs one action for the authenticated seat.
func (s *Server) handleRoomAction(w http.ResponseWriter, r *http.Request, sess *Session, seat int) {
	var req actionRequest
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// One seat, one budget (T8.6). A legal action is cheap, but a client stuck in a
	// retry loop is not, and it would otherwise spend the table's whole turn budget.
	if !s.store.seatLimits.allow(sess.ID + ":" + strconv.Itoa(seat)) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow_down"})
		return
	}
	act, err := decodeAction(req.Action)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// A room action must say which sequence it is based on. "Omitted" used to mean
	// "unchecked", so a desynchronised client could act on a stale view and only the
	// turn gate stood in the way (F-24). The client always sends coop.seq.
	if req.ExpectedSeq == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_seq_required"})
		return
	}
	expected := *req.ExpectedSeq
	if expected < 0 {
		expected = 0
	}
	_, events, _, err := s.store.ApplyFor(sess, seat, req.ActionID, expected, act)
	if err != nil {
		if errors.Is(err, ErrNotYourTurn) || errors.Is(err, ErrStaleSeq) ||
			errors.Is(err, ErrRoomStarted) || errors.Is(err, ErrCancelled) {
			// A refusal always carries the caller's own current state, so a
			// desynchronised client repairs itself in one round trip.
			p := s.store.ProjectFor(sess, seat)
			writeJSON(w, statusForRoomError(err), map[string]any{
				"error": err.Error(),
				"seq":   p.Seq,
				"state": p.State,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	p := s.store.ProjectFor(sess, seat)
	// One state frame per applied seq, projected per subscriber (T1.1).
	s.store.Publish(sess, "action")
	writeJSON(w, http.StatusOK, struct {
		Code   string             `json:"code"`
		Seat   int                `json:"seat"`
		Seq    int                `json:"seq"`
		State  engine.ClientState `json:"state"`
		Events []engine.Event     `json:"events,omitempty"`
	}{Code: p.Room.Code, Seat: seat, Seq: p.Seq, State: p.State, Events: events})
}

// seatFromRequest reads X-Regicide-Seat: <seat>:<token> and authenticates it.
//
// The seat number in the header is a label, not a credential: a lobby compaction
// renumbers every chair above the one that left, and the browser that was told
// "seat 2" a second ago now owns seat 1. The token is what the server trusts, so
// the number is parsed for shape and then ignored (D-41: the token is the seat).
func (s *Server) seatFromRequest(sess *Session, r *http.Request) (int, bool) {
	raw := r.Header.Get("X-Regicide-Seat")
	if raw == "" {
		return 0, false
	}
	seatPart, token, found := strings.Cut(raw, ":")
	if !found {
		return 0, false
	}
	if seat, err := strconv.Atoi(strings.TrimSpace(seatPart)); err != nil || seat < 0 {
		return 0, false
	}
	authed, ok := sess.SeatFor(token)
	if !ok {
		return 0, false
	}
	return authed, true
}

// handleWatch serves the spectator endpoints: POST to join, GET to read, DELETE to
// leave, and the cursor relay under the same prefix. A watch id is ephemeral and
// never becomes a seat.
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request, sess *Session) {
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/cursor") {
		s.handleWatchCursor(w, r, sess)
		return
	}
	switch r.Method {
	case http.MethodPost:
		if !s.limits.allow(s.clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
			return
		}
		id, err := s.store.AddWatcher(sess)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		p, ok := s.store.ProjectForWatcher(sess, id, watcherSeat(r))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "watch_required"})
			return
		}
		resp := roomResponse{Code: p.Room.Code, Seat: -1, Seq: p.Seq, Lobby: !p.HasGame, Room: p.Room,
			WatchID: id, Spectator: true, WatchingAs: p.WatchingAs}
		if p.HasGame {
			resp.State = &p.State
		}
		s.store.Publish(sess, "watch")
		writeJSON(w, http.StatusOK, resp)
	case http.MethodGet:
		id := r.Header.Get("X-Regicide-Watch")
		// Auth, the liveness stamp and the projection happen in one critical
		// section: a watch id revoked mid-read would otherwise serve one more frame
		// of a hand the host has just closed (D-45).
		p, ok := s.store.ProjectForWatcher(sess, id, watcherSeat(r))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "watch_required"})
			return
		}
		resp := roomResponse{Code: p.Room.Code, Seat: -1, Seq: p.Seq, Lobby: !p.HasGame, Room: p.Room,
			Spectator: true, WatchingAs: p.WatchingAs}
		if p.HasGame {
			resp.State = &p.State
		}
		writeJSON(w, http.StatusOK, resp)
	case http.MethodDelete:
		// F-16: dropping a watcher is authenticated like every other watcher call.
		// A raw id used to be enough to disconnect somebody else's view.
		id := r.Header.Get("X-Regicide-Watch")
		// Auth and removal in one critical section: as two calls, a slot could be
		// re-added or observed between the check and the drop.
		if !s.store.DropWatcherIfAuth(sess, id) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "watch_required"})
			return
		}
		s.store.Publish(sess, "watch")
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, "GET, POST, DELETE")
	}
}

// handleWatchCursor accepts a spectator's pointer frame. The relay lands with the
// presence stream; this stores the frame and enforces the switch.
func (s *Server) handleWatchCursor(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	// Pointer frames have their own bucket: at 30 Hz the client legitimately exceeds the
	// general IP limit, and dropping two frames in three is not a rate policy.
	if !s.store.cursorLimits.allow(s.clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		return
	}
	id := r.Header.Get("X-Regicide-Watch")
	if !s.store.WatcherAuth(sess, id) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "watch_required"})
		return
	}
	var body cursorBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Clamp rather than reject: a pointer on the very edge is still meaningful, and a
	// stray value must never widen the board.
	cursor := body.Cursor().Normalised()
	// The session records the watcher's last pointer (liveness and the floor); the HUB
	// holds what the presence frame carries. Writing only the session meant a grey orb
	// was stored and never drawn, for every spectator, always (F-51).
	if _, ok := s.store.SetWatcherCursor(sess, id, cursor); !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "watching_disabled"})
		return
	}
	sess.mu.Lock()
	h := s.store.hubForLocked(sess)
	sess.mu.Unlock()
	applied, ok := h.setWatcherCursor(id, cursor, s.store.Now())
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "watching_disabled"})
		return
	}
	if applied {
		h.flushPresence(s.store.Now())
	}
	// A frame inside the floor is accepted and dropped: the pointer is still where
	// the last accepted frame put it.
	_ = applied
	w.WriteHeader(http.StatusNoContent)
}

// watcherSeat reads ?as=N: which player's board a spectator asked to see. An
// unparseable or negative value means the table view.
func watcherSeat(r *http.Request) int {
	raw := r.URL.Query().Get("as")
	if raw == "" {
		return -1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

func statusForRoomError(err error) int {
	switch {
	case errors.Is(err, ErrNotYourTurn):
		return http.StatusForbidden
	case errors.Is(err, ErrStaleSeq), errors.Is(err, ErrRoomStarted),
		errors.Is(err, ErrRoomFull), errors.Is(err, ErrNotReady), errors.Is(err, ErrNotHost),
		errors.Is(err, ErrNotAway), errors.Is(err, ErrSeatPresent),
		errors.Is(err, ErrCancelled), errors.Is(err, ErrBadKey), errors.Is(err, ErrWatchFull):
		return http.StatusConflict
	case errors.Is(err, ErrWatchOff):
		return http.StatusForbidden
	case errors.Is(err, ErrBadTheme):
		return http.StatusBadRequest
	case errors.Is(err, ErrNoRoom):
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

func writeRoomError(w http.ResponseWriter, err error) {
	writeJSON(w, statusForRoomError(err), map[string]string{"error": err.Error()})
}
