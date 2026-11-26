package server

import (
	"encoding/json"
	"testing"

	"regicide/engine"
)

// benchRoom is a three-seat table that has already dealt: the shape every poll and
// every state frame projects from.
func benchRoom(b *testing.B) (*Store, *Session) {
	b.Helper()
	store, err := NewStore(b.TempDir())
	if err != nil {
		b.Fatalf("store: %v", err)
	}
	sess, _, _, err := store.CreateRoom("simple", engine.DefaultRules(), false, "BENCH-ROOM")
	if err != nil {
		b.Fatalf("create room: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, _, err := store.JoinRoom(sess); err != nil {
			b.Fatalf("join: %v", err)
		}
	}
	sess.mu.Lock()
	for i := range sess.Room.Seats {
		sess.Room.Seats[i].Ready = true
	}
	seq := sess.Seq
	sess.mu.Unlock()
	if err := store.Start(sess, 0, seq); err != nil {
		b.Fatalf("start: %v", err)
	}
	return store, sess
}

// BenchmarkProjectFor is the per-subscriber cost of one table frame.
func BenchmarkProjectFor(b *testing.B) {
	store, sess := benchRoom(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p := store.ProjectFor(sess, i%3); !p.HasGame {
			b.Fatal("no game in the projection")
		}
	}
}

// BenchmarkProjectFrame includes the JSON encoding the stream would send, because a
// client's frame cost is both.
func BenchmarkProjectFrame(b *testing.B) {
	store, sess := benchRoom(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := store.ProjectFor(sess, i%3)
		payload := map[string]any{"seq": p.Seq, "seat": p.Seat, "lobby": p.Lobby, "room": p.Room, "state": p.State}
		if _, err := json.Marshal(payload); err != nil {
			b.Fatalf("marshal: %v", err)
		}
	}
}
