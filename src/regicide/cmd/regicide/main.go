// Command regicide serves the Regicide web UI and API.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"regicide/server"
)

func main() {
	addr := flag.String("addr", ":8123", "listen address (host:port)")
	dataDir := flag.String("data", "data", "directory for session and leaderboard data")
	webDir := flag.String("web", "", "serve UI from this directory instead of the embedded copy (development)")
	trustProxy := flag.Bool("trust-proxy", os.Getenv("TRUST_PROXY") == "1",
		"trust X-Forwarded-For from a reverse proxy (set when running behind cloudflared/nginx)")
	botFill := flag.Duration("botfill", 0,
		"grace before a silent seat gets a bot (default 60s; browser checks set it short)")
	flag.Parse()

	logger := log.New(os.Stdout, "regicide ", log.LstdFlags|log.Lmsgprefix)

	srv, err := server.New(server.Options{DataDir: *dataDir, WebDir: *webDir, Logger: logger, TrustProxy: *trustProxy, BotFillAfter: *botFill})
	if err != nil {
		logger.Fatalf("setup: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Idle-session GC and orphan-file pruning run in the background; without this
	// call data/sessions/ grew forever.
	srv.StartBackground(ctx.Done())

	go func() {
		logger.Printf("listening on %s (version %s, data %s)", *addr, server.Version, *dataDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	logger.Print("shutdown requested")

	// A live stream only returns when the stream ends (up to the ten-minute soft
	// life), so Shutdown alone would time out and os.Exit would then skip the flush.
	// End the streams first, then drain the write queues on every path.
	srv.CloseStreams()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := httpSrv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		logger.Printf("shutdown: %v", shutdownErr)
	}
	// Writes are queued per session; drain them before exiting so the game log on
	// disk is the game that was played.
	srv.FlushWrites()
	if shutdownErr != nil {
		os.Exit(1)
	}
	logger.Print("stopped cleanly")
}
