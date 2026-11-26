package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"regicide/engine"
	"regicide/replay"
)

// recordGame writes one verifiable replay for the actions a bench game took, then
// reads it back, verifies the hash chain, and returns its training decisions.
//
// Why rebuild instead of recording during play: bot.PlayWith owns the transition loop
// and hands the observer the pre-action game. Replaying the same seed and actions gives
// every step its post-action hash, and the read-back proves the file before it joins a
// corpus. A bench game is microseconds; the second pass is cheaper than a recorder
// threaded through the bot.
func recordGame(dir, seed string, seats int, actions []engine.Action) (string, []replay.TrainingRecord, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, safeName(seed)+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", nil, err
	}
	players := make([]replay.Player, seats)
	for i := range players {
		players[i] = replay.Player{Seat: i, Name: fmt.Sprintf("bench-%d", i)}
	}
	mode := "coop"
	if seats == 1 {
		mode = "solo"
	}
	w := replay.NewWriter(f)
	if err := w.WriteHeader(replay.Header{
		Mode:       mode,
		Seed:       seed,
		Theme:      "simple",
		Rules:      engine.DefaultRules(),
		NumPlayers: seats,
		Players:    players,
		StartedAt:  time.Now().UTC(),
	}); err != nil {
		f.Close()
		return "", nil, err
	}
	g := engine.NewGame(engine.SetupOptions{
		Seed: seed, Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: seats,
	}, engine.NewSeededRand(seed))
	for i, act := range actions {
		actor := g.ActiveSeat
		if g.SeatChoice != nil {
			actor = g.SeatChoice.By
		}
		raw, err := engine.MarshalAction(act)
		if err != nil {
			f.Close()
			return "", nil, err
		}
		next, _, err := engine.Transition(g, act, engine.NewRandAt(g.Seed, g.RGNDraws))
		if err != nil {
			f.Close()
			return "", nil, fmt.Errorf("step %d: %w", i, err)
		}
		if err := w.WriteStep(replay.Step{
			Seq: i + 1, Seat: actor, Action: raw, Hash: engine.StateHash(next),
			Turn: next.TurnNo, Phase: next.Phase.String(), Hands: engine.HandSizes(next),
		}); err != nil {
			f.Close()
			return "", nil, err
		}
		g = next
	}
	result := replay.Result{
		Phase: g.Phase.String(), Won: g.Phase == engine.Won,
		TurnNo: g.TurnNo, CastleLeft: len(g.Castle), JestersUsed: g.JestersUsed,
	}
	if g.Phase == engine.Lost {
		result.LostReason = g.LostReason.String()
		result.LostSeat = g.LostSeat
	}
	if err := w.WriteResult(result); err != nil {
		f.Close()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}

	rf, err := os.Open(path)
	if err != nil {
		return path, nil, err
	}
	rep, err := replay.Read(rf)
	rf.Close()
	if err != nil {
		return path, nil, err
	}
	if _, err := rep.Verify(); err != nil {
		return path, nil, fmt.Errorf("verify: %w", err)
	}
	recs, err := rep.Training()
	if err != nil {
		return path, nil, err
	}
	return path, recs, nil
}

// safeName keeps a seed usable as a file name.
func safeName(seed string) string {
	var b strings.Builder
	for _, r := range seed {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "game"
	}
	return b.String()
}
