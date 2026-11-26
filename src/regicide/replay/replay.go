// Package replay reads and writes Regicide game logs.
//
// A replay is a JSONL file: one header line, one line per action, one result line.
// It is enough to reconstruct the game exactly, because the engine is deterministic
// from (seed, rules, player count) and every action is recorded in order. Each step
// carries a StateHash, so playback does not merely trust the file — it proves the
// file by recomputing the position after every move.
//
// Format reference: docs/REPLAY-FORMAT.md.
package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"regicide/engine"
)

// Format and Version identify the file itself; Engine records the rules contract
// (engine.Version), because a rules change makes old files unplayable.
const (
	Format  = "regicide.replay"
	Version = 1
)

// Header describes the game that follows: everything needed to rebuild the initial
// position from nothing but the seed.
type Header struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Engine     int    `json:"engine"`
	Mode       string `json:"mode"` // "solo" | "coop"
	Room       string `json:"room,omitempty"`
	Seed       string `json:"seed"`
	SeedRandom bool   `json:"seedRandom"`
	Theme      string `json:"theme"`
	// Jokers is the pair of Jester faces the table showed, in tray order. Older
	// replays predate the field; a reader falls back to the theme's own pair.
	Jokers     []string           `json:"jokers,omitempty"`
	Rules      engine.RuleOptions `json:"rules"`
	NumPlayers int                `json:"numPlayers"`
	Players    []Player           `json:"players"`
	StartedAt  time.Time          `json:"startedAt"`
}

// Player is the seat book: a number and the name the server gave it. Nothing here is
// secret; the hands are what the game kept hidden, and they are recorded per step.
type Player struct {
	Seat int    `json:"seat"`
	Name string `json:"name"`
}

// Step is one action and its consequence.
type Step struct {
	Seq    int             `json:"seq"`
	Seat   int             `json:"seat"`
	Action json.RawMessage `json:"action"`
	// Hash is engine.StateHash after the action: the whole point of the format.
	Hash string `json:"hash"`
	// The fields below are for analysis tools that do not want to run a rules engine.
	Turn  int    `json:"turn"`
	Phase string `json:"phase"`
	Hands []int  `json:"hands"`
}

// Result closes the file with how the game ended.
type Result struct {
	Phase       string `json:"phase"`
	Won         bool   `json:"won"`
	LostReason  string `json:"lostReason,omitempty"`
	LostSeat    int    `json:"lostSeat,omitempty"`
	TurnNo      int    `json:"turnNo"`
	CastleLeft  int    `json:"castleLeft"`
	JestersUsed int    `json:"jestersUsed"`
}

// file is the in-memory shape of a whole replay.
type file struct {
	Header Header
	Steps  []Step
	Result *Result
}

type envelope struct {
	Kind   string  `json:"kind"` // header | step | result
	Header *Header `json:"header,omitempty"`
	Step   *Step   `json:"step,omitempty"`
	Result *Result `json:"result,omitempty"`
}

// Writer appends lines to a replay file. Writing is line-at-a-time so a crash loses
// at most the action in flight.
type Writer struct{ w io.Writer }

// NewWriter wraps w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (w *Writer) write(env envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = w.w.Write(raw)
	return err
}

// WriteHeader must be called first, once the game exists.
func (w *Writer) WriteHeader(h Header) error {
	h.Format, h.Version = Format, Version
	if h.Engine == 0 {
		h.Engine = engine.Version
	}
	return w.write(envelope{Kind: "header", Header: &h})
}

// WriteStep records one applied action.
func (w *Writer) WriteStep(s Step) error {
	if s.Hash == "" {
		return errors.New("replay: a step needs its state hash")
	}
	return w.write(envelope{Kind: "step", Step: &s})
}

// WriteResult closes the game.
func (w *Writer) WriteResult(r Result) error {
	return w.write(envelope{Kind: "result", Result: &r})
}

// Read parses a whole replay. Files are small (a few tens of KB), so streaming is not
// worth the complexity.
func Read(r io.Reader) (*Replay, error) {
	out := &Replay{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		switch env.Kind {
		case "header":
			if out.Header == nil {
				out.Header = env.Header
			}
		case "step":
			if env.Step != nil {
				out.Steps = append(out.Steps, *env.Step)
			}
		case "result":
			out.Result = env.Result
		default:
			return nil, fmt.Errorf("line %d: unknown kind %q", line, env.Kind)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if out.Header == nil {
		return nil, errors.New("replay: missing header")
	}
	if out.Header.Format != Format {
		return nil, fmt.Errorf("replay: not a %s file (%q)", Format, out.Header.Format)
	}
	if out.Header.Engine != engine.Version {
		return nil, fmt.Errorf("replay: recorded under engine v%d, this build is v%d",
			out.Header.Engine, engine.Version)
	}
	return out, nil
}

// Replay is a parsed file.
type Replay struct {
	Header *Header
	Steps  []Step
	Result *Result
}

// String is a one-line description, for CLI output.
func (r *Replay) String() string {
	mode := r.Header.Mode
	if mode == "" {
		mode = "solo"
	}
	out := fmt.Sprintf("%s %s seed=%s players=%d steps=%d",
		mode, r.Header.Theme, r.Header.Seed, r.Header.NumPlayers, len(r.Steps))
	if r.Result != nil {
		out += fmt.Sprintf(" -> %s", r.Result.Phase)
	}
	return out
}
