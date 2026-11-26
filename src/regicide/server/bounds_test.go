package server

import (
	"fmt"
	"testing"
	"time"

	"regicide/engine"
)

// The board must stay bounded: an append-only slice grows with every win and
// rankOf copies the whole thing on each submit.
func TestLeaderboardCapsPerTier(t *testing.T) {
	srv := newTestServer(t)
	srv.store.now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

	for i := 0; i < maxBoardPerTier+25; i++ {
		sess := wonSession(t, srv, 0)
		sess.Game.TurnNo = 40 + i // later submissions are strictly worse
		if _, _, err := srv.store.Submit(sess, fmt.Sprintf("p%03d", i)); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	srv.store.boardMu.Lock()
	total := len(srv.store.board.Entries)
	srv.store.boardMu.Unlock()
	if total > maxBoardPerTier {
		t.Fatalf("board kept %d entries, want <= %d", total, maxBoardPerTier)
	}

	top := srv.store.Top(maxBoardPerTier + 100)[engine.TierGold]
	if len(top) != maxBoardPerTier {
		t.Fatalf("gold tier has %d entries, want %d", len(top), maxBoardPerTier)
	}
	// The cap keeps the best entries, so the last (worst) submission is gone.
	for _, e := range top {
		if e.Turns >= 40+maxBoardPerTier {
			t.Fatalf("kept a worse entry (turns %d) while trimming", e.Turns)
		}
	}
	if top[0].Turns != 40 {
		t.Fatalf("best entry has %d turns, want 40", top[0].Turns)
	}
}

// Buckets are created per client IP and were never removed: a port scan grows the
// map without bound.
func TestRateLimiterEvictsIdleBuckets(t *testing.T) {
	l := newRateLimiter()
	base := time.Now()
	l.now = func() time.Time { return base }
	for i := 0; i < 50; i++ {
		l.allow(fmt.Sprintf("10.0.0.%d", i))
	}
	if got := l.bucketCount(); got != 50 {
		t.Fatalf("buckets = %d, want 50", got)
	}

	// Half an hour later every bucket is idle: a single request sweeps them.
	l.now = func() time.Time { return base.Add(30 * time.Minute) }
	l.allow("198.51.100.50")
	if got := l.bucketCount(); got != 1 {
		t.Fatalf("buckets after sweep = %d, want 1", got)
	}

	// Fresh buckets keep their budget: the sweep must not refill or reset anyone.
	if !l.allow("198.51.100.50") {
		t.Fatal("a fresh bucket should have budget")
	}
}

func TestRateLimiterSweepKeepsActiveClients(t *testing.T) {
	l := newRateLimiter()
	base := time.Now()
	l.now = func() time.Time { return base }
	l.allow("busy")
	for i := 0; i < 50; i++ {
		l.allow(fmt.Sprintf("10.0.1.%d", i))
	}
	// "busy" is used again before the sweep window expires.
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	l.allow("busy")
	l.now = func() time.Time { return base.Add(30 * time.Minute) }
	l.allow("newcomer")
	if _, ok := l.buckets["busy"]; ok {
		t.Fatal("an idle-for-28-minutes bucket should have been swept")
	}
	if got := l.bucketCount(); got != 1 {
		t.Fatalf("buckets = %d, want 1 (only newcomer)", got)
	}
}

// The idempotency cache is keyed by client-supplied action ids.
func TestSessionCacheIsBounded(t *testing.T) {
	sess := &Session{cache: map[string]cachedAction{}}
	for i := 0; i < maxCachedActions*2; i++ {
		sess.remember(fmt.Sprintf("action-%04d", i), cachedAction{seq: i})
	}
	if got := len(sess.cache); got > maxCachedActions {
		t.Fatalf("cache holds %d entries, want <= %d", got, maxCachedActions)
	}
	// The newest id survives; the oldest is evicted.
	if _, ok := sess.cache["action-0000"]; ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := sess.cache[fmt.Sprintf("action-%04d", maxCachedActions*2-1)]; !ok {
		t.Fatal("newest entry should still be cached")
	}
}
