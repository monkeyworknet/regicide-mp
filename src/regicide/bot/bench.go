package bot

import (
	"fmt"
	"math/rand"

	"regicide/engine"
)

// Policy turns one seat's observation into one action. Every policy in this package has
// this shape, so the bench can measure any of them, and the server can run any of them
// for a bot seat.
type Policy func(Observation) engine.Action

// Result is one finished game.
type Result struct {
	Seed      string
	Seats     int
	Won       bool
	LostFor   string
	Castles   int // royals cleared
	Turns     int
	Steps     int
	DeckEmpty bool
}

// Play runs one game to the end with the policy in every seat.
//
// It applies actions through engine.Transition exactly as the server does, and it gives
// each seat only its own observation — the same restricted view the bot seat uses
// (T7.5). A bench that reads the full game would be measuring a cheat.
func Play(seed string, seats int, policy Policy) (Result, error) {
	return PlayWith(seed, seats, policy, nil)
}

// PlayWith is Play with an optional per-step observer, for teaching and for tests.
func PlayWith(seed string, seats int, policy Policy, onStep func(g engine.Game, act engine.Action)) (Result, error) {
	return playWith(seed, seats, policy, onStep, nil)
}

func playWith(seed string, seats int, policy Policy, onStep func(g engine.Game, act engine.Action), substitutions *int) (Result, error) {
	g := engine.NewGame(engine.SetupOptions{
		Seed: seed, Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: seats,
	}, engine.NewSeededRand(seed))
	res := Result{Seed: seed, Seats: seats}
	const maxSteps = 4000
	for step := 0; step < maxSteps; step++ {
		if g.Phase == engine.Won || g.Phase == engine.Lost {
			break
		}
		legal := engine.LegalActions(&g)
		if len(legal) == 0 {
			return res, fmt.Errorf("seed %s: no legal action in phase %s", seed, g.Phase)
		}
		actor := g.ActiveSeat
		if g.SeatChoice != nil {
			actor = g.SeatChoice.By
		}
		obs, ok := Observe(g, actor)
		if !ok {
			return res, fmt.Errorf("seed %s: seat %d is not at the table", seed, actor)
		}
		// The policy's answer is judged by the engine, not by the enumerated list: the
		// list offers single-card payments while a multi-card payment is equally legal,
		// so "was it in the list" would fail a correct policy. What must never happen is
		// the engine refusing what the policy asked for.
		want := policy(obs)
		rng := engine.NewRandAt(g.Seed, g.RGNDraws)
		next, _, err := engine.Transition(g, want, rng)
		var act engine.Action = want
		if err != nil {
			if substitutions != nil {
				*substitutions++
			}
			act = legal[0]
			next, _, err = engine.Transition(g, act, engine.NewRandAt(g.Seed, g.RGNDraws))
			if err != nil {
				return res, fmt.Errorf("seed %s step %d: %s: %w", seed, step, engine.ActionName(act), err)
			}
		}
		if onStep != nil {
			onStep(g, act)
		}
		res.Steps++
		g = next
	}
	res.Won = g.Phase == engine.Won
	res.Castles = 12 - len(g.Castle)
	res.Turns = g.TurnNo
	res.DeckEmpty = len(g.Tavern) == 0
	if g.Phase == engine.Lost {
		res.LostFor = g.LostReason.String()
	}
	return res, nil
}

// PlayStrictWith is PlayStrict with the per-step observer, for building a training
// corpus from the same games the strict bench measures.
func PlayStrictWith(seed string, seats int, policy Policy, onStep func(g engine.Game, act engine.Action)) (Result, int, error) {
	substitutions := 0
	res, err := playWith(seed, seats, policy, onStep, &substitutions)
	return res, substitutions, err
}

// PlayStrict is PlayWith that also reports how many times the policy proposed an action
// the rules did not offer. A substitution is a policy bug, not a rules bug, so the tests
// assert it is zero rather than letting the bench quietly paper over it.
func PlayStrict(seed string, seats int, policy Policy) (Result, int, error) {
	substitutions := 0
	res, err := playWith(seed, seats, policy, nil, &substitutions)
	return res, substitutions, err
}

// Random is the floor: a uniform choice among the legal actions. It is the baseline the
// plan quotes, and it is here so the bench can prove the numbers are not an artefact of
// the rules alone.
func Random(r *rand.Rand) Policy {
	return func(obs Observation) engine.Action {
		legal := legalFor(obs)
		if len(legal) == 0 {
			return engine.Yield{}
		}
		return legal[r.Intn(len(legal))]
	}
}

// Tally summarises a bench run.
type Tally struct {
	Games     int
	Wins      int
	SumCastle int
	SumTurns  int
	MaxTurns  int
	LostFor   map[string]int
}

func (t *Tally) Add(r Result) {
	t.Games++
	if r.Won {
		t.Wins++
	}
	t.SumCastle += r.Castles
	if r.Turns > t.MaxTurns {
		t.MaxTurns = r.Turns
	}
	t.SumTurns += r.Turns
	if t.LostFor == nil {
		t.LostFor = map[string]int{}
	}
	if r.LostFor != "" {
		t.LostFor[r.LostFor]++
	}
}

func (t Tally) WinRate() float64 {
	if t.Games == 0 {
		return 0
	}
	return float64(t.Wins) / float64(t.Games)
}

func (t Tally) CastlesPerGame() float64 {
	if t.Games == 0 {
		return 0
	}
	return float64(t.SumCastle) / float64(t.Games)
}

func (t Tally) TurnsPerGame() float64 {
	if t.Games == 0 {
		return 0
	}
	return float64(t.SumTurns) / float64(t.Games)
}
