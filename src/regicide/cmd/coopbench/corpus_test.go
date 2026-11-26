package main

import (
	"os"
	"path/filepath"
	"testing"

	"regicide/bot"
	"regicide/engine"
	"regicide/replay"
)

// TestRecordGameWritesAVerifiedReplay is the corpus tool's contract: one file per game,
// a hash chain that verifies, and one training record per action.
func TestRecordGameWritesAVerifiedReplay(t *testing.T) {
	var actions []engine.Action
	res, subs, err := bot.PlayStrictWith("CORPUS-TOOL-1", 3, bot.Greedy, func(_ engine.Game, act engine.Action) {
		actions = append(actions, act)
	})
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if subs != 0 {
		t.Fatalf("greedy substituted %d actions", subs)
	}
	if res.Steps == 0 {
		t.Fatal("game took no steps")
	}
	dir := t.TempDir()
	path, recs, err := recordGame(dir, "CORPUS-TOOL-1", 3, actions)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if filepath.Dir(path) != dir || filepath.Ext(path) != ".jsonl" {
		t.Fatalf("path = %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rep, err := replay.Read(f)
	f.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := rep.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Header.Mode != "coop" || rep.Header.NumPlayers != 3 {
		t.Errorf("header = %+v", rep.Header)
	}
	if len(rep.Steps) != len(actions) {
		t.Errorf("steps = %d, actions = %d", len(rep.Steps), len(actions))
	}
	if rep.Result == nil || rep.Result.Phase == "" {
		t.Errorf("result = %+v", rep.Result)
	}
	if len(recs) != len(actions) {
		t.Errorf("training records = %d, actions = %d", len(recs), len(actions))
	}
	if len(recs) > 0 && (len(recs[0].Legal) == 0 || len(recs[0].Chosen) == 0) {
		t.Errorf("first record has no legal/chosen action: %+v", recs[0])
	}
}
