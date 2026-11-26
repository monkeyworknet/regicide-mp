// Package server owns the HTTP surface: routing, session storage, static asset
// serving and the embedded UI. All game rules live in regicide/engine; this
// package only translates HTTP to engine calls.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Version is reported by /api/health. Bump on any deploy-visible change.
const Version = "0.9.9"

// Options configures a Server.
type Options struct {
	// DataDir is where session snapshots and the leaderboard live. It is
	// created if missing.
	DataDir string
	// WebDir, when set, serves the UI from disk instead of the embedded copy
	// (development convenience).
	WebDir string
	// Logger receives diagnostics. Defaults to log.Default().
	Logger *log.Logger
	// TrustProxy makes the rate limiter key on the first X-Forwarded-For address.
	// Enable it only when a reverse proxy (cloudflared, nginx) sets that header:
	// otherwise every client behind one shared address would share a bucket.
	TrustProxy bool
	// BotFillAfter overrides how long a silent seat waits before a bot takes its
	// chair. Zero keeps the shipped minute. A browser check sets it short so the
	// gate does not sleep through a real disconnect window.
	BotFillAfter time.Duration
}

// Server is an http.Handler plus its dependencies.
type Server struct {
	opts          Options
	log           *log.Logger
	mux           *http.ServeMux
	store         *Store
	limits        *rateLimiter
	once          sync.Once
	sessionsTotal atomic.Int64
}

// New builds a Server. It does not start listening.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.DataDir == "" {
		opts.DataDir = "data"
	}
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	if opts.BotFillAfter > 0 {
		store.botFill = opts.BotFillAfter
	}
	s := &Server{opts: opts, log: opts.Logger, mux: http.NewServeMux(), store: store, limits: store.limits}
	s.routes()
	return s, nil
}

// FlushWrites drains every session's write queue so no acknowledged action is lost
// on shutdown.
func (s *Server) FlushWrites() { s.store.FlushWrites() }

// Handler exposes the router (used by tests and by main).
func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }

// CloseStreams ends every live stream. Shutdown waits for handlers, and a stream
// handler only returns when its stream ends (up to streamSoftLife); closing them first
// is what lets a restart finish inside the shutdown grace.
func (s *Server) CloseStreams() { s.store.CloseAllStreams("shutdown") }

// securityHeaders sets the response headers that are true for every route. The game is
// LAN-only today, but these cost nothing and are the difference between "safe behind a
// proxy" and "safe to expose".
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// Same-origin, not DENY: the layout harness frames the board (it needs a real
		// viewport), and a page framing itself is not clickjacking.
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'self'; "+
				"base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// routes registers every path. Method dispatch is manual by design: the project
// runs on net/http's ServeMux with no third-party router.
func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", allow(http.MethodGet)(s.handleHealth))
	s.mux.HandleFunc("/api/sessions", allow(http.MethodPost)(s.handleSessionsCreate))
	s.mux.HandleFunc("/api/sessions/", s.handleSessionRoutes)
	s.mux.HandleFunc("/api/rooms", s.handleRooms)
	s.mux.HandleFunc("/api/rooms/", s.handleRoomRoutes)
	s.mux.HandleFunc("/api/leaderboard", s.handleLeaderboard)
	// The replay viewer's data route is authenticated by its ticket, not by a seat.
	s.mux.HandleFunc("/api/replay/", s.handleReplayView)
	s.mux.Handle("/", s.staticHandler())
}

// StartBackground runs periodic housekeeping until stop is closed: idle session
// GC every minute and orphan file pruning once an hour.
func (s *Server) StartBackground(stop <-chan struct{}) {
	go func() {
		// GC runs every minute so an hour-old room cannot be rejoined for long; orphan
		// files are pruned hourly.
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		ticks := 0
		for {
			select {
			case <-stop:
				// Every live stream says goodbye and its goroutine exits, so Shutdown
				// does not wait on a handler that will never return (T1.3).
				s.store.CloseAllStreams("shutdown")
				return
			case <-ticker.C:
				ticks++
				if n := s.store.GC(roomIdleTTL); n > 0 {
					s.log.Printf("gc: removed %d idle sessions", n)
				}
				if ticks%60 == 0 {
					if n := s.store.PruneOrphans(roomIdleTTL); n > 0 {
						s.log.Printf("prune: removed %d orphan session files", n)
					}
				}
			}
		}
	}()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// No filesystem paths: the health check is reachable by anyone on the LAN.
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  Version,
		"sessions": s.sessionsTotal.Load(),
	})
}

// allow wraps a handler so that only the given method is accepted; every other
// method gets 405 with an Allow header.
func allow(method string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				methodNotAllowed(w, method)
				return
			}
			next(w, r)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so writing an error body here would
		// corrupt the response; the client sees a truncated document instead.
		return
	}
}
