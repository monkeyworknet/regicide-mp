package replay

import (
	"encoding/json"
	"fmt"

	"regicide/engine"
)

// Verify rebuilds the game from the header and replays every step, comparing the
// recorded state hash after each one. It returns the reconstructed game so a caller
// can analyse it further.
//
// A mismatch means the file does not describe what actually happened: a tampered
// action, a truncated file, or a rules change that altered a transition.
func (r *Replay) Verify() (engine.Game, error) {
	game, err := r.Build()
	if err != nil {
		return engine.Game{}, err
	}
	// F-15: the sequence has to climb. A file with a step cut out of the middle still
	// matches its own hashes, so ordering is checked independently of them.
	lastSeq := -1
	// The seat book has to name the players it deals to, once each, inside range.
	// Comparing the dealt count to the header was tautological: Build derives the
	// players from that same number (F-44).
	seen := map[int]bool{}
	for _, p := range r.Header.Players {
		if p.Seat < 0 || p.Seat >= r.Header.NumPlayers {
			return game, fmt.Errorf("header: seat %d is outside 0..%d", p.Seat, r.Header.NumPlayers-1)
		}
		if seen[p.Seat] {
			return game, fmt.Errorf("header: seat %d is listed twice", p.Seat)
		}
		seen[p.Seat] = true
	}
	// A book with a hole (seats 0 and 2) passes the range and duplicate checks, and then
	// every seat-indexed summary reads the wrong player. The engine deals contiguous
	// seats, so a file that does not list 0..NumPlayers-1 is not one of ours.
	for i := 0; i < r.Header.NumPlayers; i++ {
		if !seen[i] {
			return game, fmt.Errorf("header: seat %d is missing", i)
		}
	}
	for i, step := range r.Steps {
		if step.Seq <= lastSeq {
			return game, fmt.Errorf("step %d: seq %d does not advance past %d", i+1, step.Seq, lastSeq)
		}
		lastSeq = step.Seq
		act, err := engine.UnmarshalAction(step.Action)
		if err != nil {
			return game, fmt.Errorf("step %d (seq %d): %w", i+1, step.Seq, err)
		}
		// The recorded actor must be the seat whose turn it was. The state hash covers
		// position, not attribution, so without this an edited `seat` still verified
		// and the training corpus credited one player's cards to another (F-44).
		actor := game.ActiveSeat
		if game.SeatChoice != nil {
			actor = game.SeatChoice.By
		}
		if step.Seat != actor {
			return game, fmt.Errorf("step %d (seq %d): recorded seat %d, the table moved seat %d",
				i+1, step.Seq, step.Seat, actor)
		}
		rng := engine.NewRandAt(game.Seed, game.RGNDraws)
		next, _, err := engine.Transition(game, act, rng)
		if err != nil {
			return game, fmt.Errorf("step %d (seq %d, %s): %w", i+1, step.Seq, engine.ActionName(act), err)
		}
		game = next
		if got := engine.StateHash(game); got != step.Hash {
			return game, fmt.Errorf("step %d (seq %d): state hash %s, recorded %s", i+1, step.Seq, got, step.Hash)
		}
	}
	if r.Result != nil {
		if game.Phase.String() != r.Result.Phase {
			return game, fmt.Errorf("final phase %s, recorded %s", game.Phase, r.Result.Phase)
		}
		// The result line is a claim about the rebuilt game, not a copy of it.
		if won := game.Phase == engine.Won; won != r.Result.Won {
			return game, fmt.Errorf("result says won=%v, the rebuilt game says %v", r.Result.Won, won)
		}
		if left := len(game.Castle); left != r.Result.CastleLeft {
			return game, fmt.Errorf("result says %d royals left, the rebuilt game has %d", r.Result.CastleLeft, left)
		}
	}
	return game, nil
}

// Build reconstructs the opening position from the header alone.
func (r *Replay) Build() (engine.Game, error) {
	h := r.Header
	if h.NumPlayers <= 0 {
		return engine.Game{}, fmt.Errorf("header: numPlayers %d", h.NumPlayers)
	}
	return engine.NewGame(engine.SetupOptions{
		Seed:       h.Seed,
		SeedRandom: h.SeedRandom,
		Theme:      h.Theme,
		Jokers:     h.Jokers,
		Rules:      h.Rules,
		NumPlayers: h.NumPlayers,
	}, engine.NewSeededRand(h.Seed)), nil
}

// Summary is the human-facing shape of a finished game.
type Summary struct {
	Mode          string
	Theme         string
	Seed          string
	Players       []Player
	Steps         int
	Phase         string
	Won           bool
	LostReason    string
	LostSeat      int
	Turns         int
	CastleLeft    int
	ActionsBySeat []int
	// Damage dealt per seat, from the recorded play events of the rebuilt game.
	DamageBySeat []int
}

// Summarise rebuilds the game and reports the numbers an after-action review wants.
func (r *Replay) Summarise() (Summary, error) {
	game, err := r.Verify()
	if err != nil {
		return Summary{}, err
	}
	out := Summary{
		Mode: r.Header.Mode, Theme: r.Header.Theme, Seed: r.Header.Seed,
		Players: r.Header.Players, Steps: len(r.Steps),
		Phase: game.Phase.String(), CastleLeft: len(game.Castle), Turns: game.TurnNo,
		LostReason: game.LostReason.String(), LostSeat: game.LostSeat,
		ActionsBySeat: make([]int, len(game.Players)),
		DamageBySeat:  make([]int, len(game.Players)),
	}
	out.Won = game.Phase == engine.Won
	for _, step := range r.Steps {
		if step.Seat >= 0 && step.Seat < len(out.ActionsBySeat) {
			out.ActionsBySeat[step.Seat]++
		}
	}
	for _, ev := range game.Log {
		if ev.Type == engine.EvPlay && ev.Seat >= 0 && ev.Seat < len(out.DamageBySeat) {
			out.DamageBySeat[ev.Seat] += ev.Amount
		}
	}
	return out, nil
}

// TrainingRecord is one decision point, ready for imitation learning or offline
// evaluation: the position, everything the player could have done, what they did,
// and how it turned out.
type TrainingRecord struct {
	Seq       int             `json:"seq"`
	Seat      int             `json:"seat"`
	SeatName  string          `json:"seatName,omitempty"`
	Turn      int             `json:"turn"`
	Phase     string          `json:"phase"`
	State     json.RawMessage `json:"state"`
	Legal     json.RawMessage `json:"legal"`
	Chosen    json.RawMessage `json:"chosen"`
	Hands     []int           `json:"hands"`
	Incoming  int             `json:"incoming"`
	Enemy     string          `json:"enemy,omitempty"`
	WonTable  bool            `json:"wonTable"`
	StepsLeft int             `json:"stepsLeft"`
}

// Training emits one record per step, each holding the viewer-scoped state *before*
// the action, the legal action list, and the action taken. It is the corpus a bot
// would learn from, and it is reproducible from the same replay file.
func (r *Replay) Training() ([]TrainingRecord, error) {
	game, err := r.Build()
	if err != nil {
		return nil, err
	}
	// Whether the table won is a fact about the rebuilt game, so it is recomputed once
	// rather than trusted from the file (F-15).
	rebuiltWon := false
	if rebuilt, verifyErr := r.Verify(); verifyErr == nil {
		rebuiltWon = rebuilt.Phase == engine.Won
	}
	names := map[int]string{}
	for _, p := range r.Header.Players {
		names[p.Seat] = p.Name
	}
	out := make([]TrainingRecord, 0, len(r.Steps))
	for i, step := range r.Steps {
		snap := engine.SnapshotFor(game, engine.View{Seat: step.Seat})
		stateJSON, err := json.Marshal(snap)
		if err != nil {
			return nil, err
		}
		legalJSON, err := json.Marshal(snap.Legal)
		if err != nil {
			return nil, err
		}
		act, err := engine.UnmarshalAction(step.Action)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		chosenJSON, err := engine.MarshalAction(act)
		if err != nil {
			return nil, err
		}
		rec := TrainingRecord{
			Seq: step.Seq, Seat: step.Seat, SeatName: names[step.Seat], Turn: game.TurnNo,
			Phase: game.Phase.String(), State: stateJSON, Legal: legalJSON, Chosen: chosenJSON,
			Hands: engine.HandSizes(game), Incoming: snap.Incoming,
			// Derived from the rebuilt game, not read off the file's own result line.
			WonTable: rebuiltWon, StepsLeft: len(r.Steps) - i,
		}
		if snap.Enemy != nil {
			rec.Enemy = snap.Enemy.Label
		}
		out = append(out, rec)

		rng := engine.NewRandAt(game.Seed, game.RGNDraws)
		next, _, err := engine.Transition(game, act, rng)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		game = next
	}
	return out, nil
}
