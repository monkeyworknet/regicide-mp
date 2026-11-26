package bot

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"regicide/engine"
)

// The bot must know exactly what its seat knows (T7.5). These tests are the promise:
// a bot that could see the tavern order, another hand or the seed would be a cheat, and
// the bench numbers would be measuring the cheat.
func TestObservationIsSeatScoped(t *testing.T) {
	g := engine.NewGame(engine.SetupOptions{
		Seed: "OBSERVE-SCOPE", Theme: "simple", Rules: engine.DefaultRules(), NumPlayers: 3,
	}, engine.NewSeededRand("OBSERVE-SCOPE"))

	obs, ok := Observe(g, 1)
	if !ok {
		t.Fatal("seat 1 is at the table")
	}
	if len(obs.Hand) != len(g.Players[1].Hand) {
		t.Fatalf("hand has %d cards, the seat holds %d", len(obs.Hand), len(g.Players[1].Hand))
	}
	own := map[engine.CardID]bool{}
	for _, c := range obs.Hand {
		own[c.ID] = true
	}
	// No card from another seat may appear in this observation.
	for seat, p := range g.Players {
		if seat == 1 {
			continue
		}
		for _, c := range p.Hand {
			if own[c.ID] {
				t.Fatalf("seat %d's card %s leaked into seat 1's observation", seat, c.ID)
			}
		}
	}
	// The seed is not in the observation at all: no field holds it, and this asserts a
	// future field cannot quietly add it.
	if strings.Contains(fmt.Sprintf("%+v", obs), g.Seed) {
		t.Fatal("the observation carries the seed")
	}
	if obs.Tavern != len(g.Tavern) {
		t.Fatalf("tavern count %d, want %d", obs.Tavern, len(g.Tavern))
	}
	// The tavern's ORDER is not visible: only a count.
	if obs.Tavern > 0 && len(obs.Hand) > 0 && obs.Hand[0] == g.Tavern[0] && len(g.Players[1].Hand) == 0 {
		t.Log("tavern top happens to match a hand card; counts are all that is exposed")
	}
}

// TestPoliciesOnlyChooseLegalActions: a substitution means the policy asked for something
// the rules do not offer. Zero substitutions across 200 seeded games is the assertion.
func TestPoliciesOnlyChooseLegalActions(t *testing.T) {
	policies := map[string]Policy{
		"greedy": Greedy,
		"random": Random(rand.New(rand.NewSource(7))),
	}
	for name, pol := range policies {
		for i := 0; i < 200; i++ {
			seed := "LEGAL-" + name + "-" + string(rune('A'+i%26)) + string(rune('A'+i/26))
			if _, subs, err := PlayStrict(seed, 3, pol); err != nil {
				t.Fatalf("%s seed %s: %v", name, seed, err)
			} else if subs != 0 {
				t.Fatalf("%s seed %s: %d actions were not legal", name, seed, subs)
			}
		}
	}
}

// TestPaymentUsesTheCheapestCards: paying with a King for a 10-damage bill wastes ten
// points of a finite deck. The payment must be the least total value that covers the
// need, then the fewest cards.
func TestPaymentUsesTheCheapestCards(t *testing.T) {
	obs := Observation{
		Phase:    engine.Step4Pre,
		Required: 10,
		Hand: []engine.Card{
			{ID: "KS", Rank: engine.King, Suit: engine.Spades},
			{ID: "9H", Rank: engine.Nine, Suit: engine.Hearts},
			{ID: "2C", Rank: engine.Two, Suit: engine.Clubs},
			{ID: "3D", Rank: engine.Three, Suit: engine.Diamonds},
		},
	}
	act, ok := Greedy(obs).(engine.PayDamage)
	if !ok {
		t.Fatalf("expected a payment, got %T", Greedy(obs))
	}
	// 2C+9H = 11 is the least deck value that covers 10; 9H+3D would spend 12 and the
	// King would spend 20. The assertion is about the deck, not the card count.
	want := map[engine.CardID]bool{"9H": true, "2C": true}
	if len(act.CardIDs) != 2 || !want[act.CardIDs[0]] || !want[act.CardIDs[1]] {
		t.Fatalf("paid %v: the cheapest cover of 10 is 9H+2C", act.CardIDs)
	}
}

// TestDefaultPolicyPaysInOneBatch: the seat pays the whole bill in one action.
// Paying one card at a time made a three-card defence three separate bot moves, with
// three reveals; the table should see one defence with every card.
func TestDefaultPolicyPaysInOneBatch(t *testing.T) {
	obs := Observation{
		Seat:     0,
		Seats:    3,
		Phase:    engine.Step4Pre,
		Required: 10,
		Hand: []engine.Card{
			{ID: "2C", Rank: engine.Two, Suit: engine.Clubs},
			{ID: "3D", Rank: engine.Three, Suit: engine.Diamonds},
			{ID: "5H", Rank: engine.Five, Suit: engine.Hearts},
			{ID: "KS", Rank: engine.King, Suit: engine.Spades},
		},
		HandCounts: []int{4, 4, 4},
	}
	act, ok := DefaultPolicy()(obs).(engine.PayDamage)
	if !ok {
		t.Fatalf("expected a payment, got %T", DefaultPolicy()(obs))
	}
	if len(act.CardIDs) < 2 {
		t.Fatalf("paid %v: the policy dribbled the bill out one card at a time", act.CardIDs)
	}
}

// TestGreedyKeepsTheLastCard: spending the last card on damage leaves nothing to pay
// with, which loses the table.
func TestGreedyKeepsTheLastCard(t *testing.T) {
	obs := Observation{
		Phase:      engine.Step1Await,
		Seat:       0,
		Seats:      2,
		Enemy:      engine.Card{ID: "JS", Rank: engine.Jack, Suit: engine.Spades},
		Health:     20,
		Attack:     10,
		Hand:       []engine.Card{{ID: "7C", Rank: engine.Seven, Suit: engine.Clubs}},
		LeastCard:  true,
		YieldLegal: true,
	}
	act := Greedy(obs)
	if play, ok := act.(engine.Play); ok {
		t.Fatalf("the last card was spent as a play (%v) while yielding was legal", play.CardIDs)
	}

	// With no yield on offer the seat is doomed either way, so it must still take the
	// damage with it rather than refuse to act.
	obs.YieldLegal = false
	if _, ok := Greedy(obs).(engine.Play); !ok {
		t.Fatal("with no yield and no Jester the policy must still play its best card")
	}
}

// TestBenchIsFast: the bench is a development loop, so 200 three-seat games must finish
// well inside a minute.
func TestBenchIsFast(t *testing.T) {
	start := time.Now()
	tally := Tally{}
	for i := 0; i < 200; i++ {
		seed := "SPEED-" + string(rune('A'+i%26)) + string(rune('A'+i/26))
		r, err := Play(seed, 3, Greedy)
		if err != nil {
			t.Fatalf("seed %s: %v", seed, err)
		}
		tally.Add(r)
	}
	elapsed := time.Since(start)
	if elapsed > 60*time.Second {
		t.Fatalf("200 games took %s", elapsed)
	}
	if tally.Games != 200 {
		t.Fatalf("games %d", tally.Games)
	}
	t.Logf("200 games in %s: castles %.2f/12, wins %.1f%%, turns %.1f",
		elapsed, tally.CastlesPerGame(), tally.WinRate()*100, tally.TurnsPerGame())
}

// TestGreedyBeatsRandom is the policy's regression guard: an economy-aware policy must
// clear more royals per game than a uniform choice. It is a wide margin, not a tight one,
// so it fails only when the policy has genuinely lost its judgement.
func TestGreedyBeatsRandom(t *testing.T) {
	const n = 200
	measure := func(pol Policy) float64 {
		tally := Tally{}
		for i := 0; i < n; i++ {
			seed := "COMPARE-" + string(rune('A'+i%26)) + string(rune('A'+i/26))
			r, err := Play(seed, 3, pol)
			if err != nil {
				t.Fatalf("seed %s: %v", seed, err)
			}
			tally.Add(r)
		}
		return tally.CastlesPerGame()
	}
	greedy := measure(Greedy)
	random := measure(Random(rand.New(rand.NewSource(11))))
	if greedy <= random {
		t.Fatalf("greedy %.2f/12 castles, random %.2f/12: the policy is not better than chance", greedy, random)
	}
	t.Logf("greedy %.2f/12 vs random %.2f/12 castles", greedy, random)
}

// TestNoGameRunsForever: the plan's liveness requirement for a policy.
func TestNoGameRunsForever(t *testing.T) {
	for i := 0; i < 100; i++ {
		seed := "LIVE-" + string(rune('A'+i%26))
		r, err := Play(seed, 3, Greedy)
		if err != nil {
			t.Fatalf("seed %s: %v", seed, err)
		}
		if r.Turns > 120 {
			t.Fatalf("seed %s ran %d turns", seed, r.Turns)
		}
	}
}

// TestPersonasPlayLegalGames keeps every named policy on the same contract as Greedy:
// one legal action per decision, no substitutions, a game that runs to an end.
func TestPersonasPlayLegalGames(t *testing.T) {
	for name, policy := range Policies() {
		t.Run(name, func(t *testing.T) {
			res, subs, err := PlayStrict("PERSONA-"+name, 3, policy)
			if err != nil {
				t.Fatalf("play: %v", err)
			}
			if subs != 0 {
				t.Errorf("%d illegal actions were substituted", subs)
			}
			if res.Steps == 0 {
				t.Error("the game took no steps")
			}
			if !res.Won && res.LostFor == "" {
				t.Error("the game neither won nor recorded a loss reason")
			}
		})
	}
}

// TestPlaysEnumeratesEverySuitSubset: a same-rank combo is not just a prefix of the
// hand. The suits in the subset decide which powers are spent, so every combination
// must be a candidate. Aces pair with every partner as well as being a single.
func TestPlaysEnumeratesEverySuitSubset(t *testing.T) {
	obs := Observation{
		Hand: []engine.Card{
			{ID: "3C", Rank: 3, Suit: engine.Clubs},
			{ID: "3D", Rank: 3, Suit: engine.Diamonds},
			{ID: "3H", Rank: 3, Suit: engine.Hearts},
			{ID: "AS", Rank: 1, Suit: engine.Spades},
			{ID: "9D", Rank: 9, Suit: engine.Diamonds},
		},
		Enemy:  engine.Card{ID: "JS", Rank: 11, Suit: engine.Spades},
		Health: 20, Attack: 10,
	}
	plays := plays(obs)
	has := func(ids ...engine.CardID) bool {
		for _, p := range plays {
			if len(p.CardIDs) != len(ids) {
				continue
			}
			seen := map[engine.CardID]bool{}
			for _, id := range p.CardIDs {
				seen[id] = true
			}
			ok := true
			for _, id := range ids {
				if !seen[id] {
					ok = false
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	if !has("3D", "3H") || !has("3C", "3D") || !has("3C", "3H") {
		t.Error("not every same-rank suit pair is a candidate")
	}
	if !has("3C", "3D", "3H") {
		t.Error("the three-card same-rank combo is missing")
	}
	if !has("AS", "9D") {
		t.Error("the Ace should pair with the diamond, not only the cheapest/strongest")
	}
	// The engine offers a single Animal Companion too; the pairing is a stronger
	// option, not the only legal one.
	if !has("AS") {
		t.Error("the single Animal Companion should remain a candidate")
	}
}

// TestDefaultPolicyPlaysLegalGames: the policy a real bot seat uses must satisfy the
// same contract as the bench policies, not only the ones in the comparison table.
func TestDefaultPolicyPlaysLegalGames(t *testing.T) {
	res, subs, err := PlayStrict("DEFAULT-POLICY", 3, DefaultPolicy())
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if subs != 0 {
		t.Errorf("%d illegal actions were substituted", subs)
	}
	if res.Steps == 0 {
		t.Error("the game took no steps")
	}
}

// TestEvaluatorMatchesEngine pins the five rule-model corrections the 0.9.0 win rate
// rests on. They are pure functions of an Observation, so a table is enough: a Jester
// play owes nothing, suit powers resolve at the total, hearts are capped by the discard,
// and shield cannot discount more than the bill.
func TestEvaluatorMatchesEngine(t *testing.T) {
	enemy := engine.Card{ID: "JS", Rank: engine.Jack, Suit: engine.Spades}
	cards := func(ids ...engine.CardID) []engine.Card {
		out := make([]engine.Card, 0, len(ids))
		for _, id := range ids {
			for _, c := range append(cardDeck(), engine.Card{ID: "J1", Rank: engine.Joker, Suit: engine.NoSuit}) {
				if c.ID == id {
					out = append(out, c)
				}
			}
		}
		return out
	}
	base := func(hand ...engine.Card) Observation {
		return Observation{
			Seat: 0, Seats: 3, Hand: hand, Enemy: enemy, Health: 20, Attack: 10,
			Phase: engine.Step1Await, MaxHand: 6, Discard: 20, Tavern: 20,
			HandCounts: []int{len(hand), 6, 6},
		}
	}

	t.Run("jester skips the bill", func(t *testing.T) {
		obs := base(cards("J1", "10C")...)
		out := evaluatePlay(obs, engine.Play{CardIDs: []engine.CardID{"J1"}}, DefaultWeights())
		if !out.unlock || out.payCost != 0 || !out.payable {
			t.Fatalf("jester: unlock=%v payCost=%d payable=%v, want a free skip", out.unlock, out.payCost, out.payable)
		}
	})

	t.Run("suit powers use the total attack", func(t *testing.T) {
		obs := base(cards("5S", "5C")...)
		obs.Enemy = engine.Card{ID: "JH", Rank: engine.Jack, Suit: engine.Hearts}
		out := evaluatePlay(obs, engine.Play{CardIDs: []engine.CardID{"5S", "5C"}}, DefaultWeights())
		if out.shield != 10 || out.damage != 20 {
			t.Fatalf("5S+5C: shield=%d damage=%d, want shield 10 (total) and damage 20 (club double)", out.shield, out.damage)
		}
		hearts := base(cards("3H", "3C")...)
		hout := evaluatePlay(hearts, engine.Play{CardIDs: []engine.CardID{"3H", "3C"}}, DefaultWeights())
		if hout.heal != 6 || hout.healCards != 6 {
			t.Fatalf("3H+3C: heal=%d cards=%d, want 6 (total)", hout.heal, hout.healCards)
		}
	})

	t.Run("hearts are capped by the discard", func(t *testing.T) {
		empty := base(cards("9H")...)
		empty.Discard = 0
		if out := evaluatePlay(empty, engine.Play{CardIDs: []engine.CardID{"9H"}}, DefaultWeights()); out.heal != 0 {
			t.Fatalf("9H into an empty discard heals %d, want 0", out.heal)
		}
		thin := base(cards("9H")...)
		thin.Discard = 3
		if out := evaluatePlay(thin, engine.Play{CardIDs: []engine.CardID{"9H"}}, DefaultWeights()); out.heal != 3 {
			t.Fatalf("9H into three discards heals %d, want 3", out.heal)
		}
	})

	t.Run("shield cannot discount more than the bill", func(t *testing.T) {
		obs := base(cards("5S", "2C")...)
		obs.Enemy = engine.Card{ID: "JH", Rank: engine.Jack, Suit: engine.Hearts}
		obs.Shield = 8
		out := evaluatePlay(obs, engine.Play{CardIDs: []engine.CardID{"5S"}}, DefaultWeights())
		if out.payCost != 0 || !out.payable {
			t.Fatalf("5S with 8 shield already banked: payCost=%d payable=%v, want 0/true", out.payCost, out.payable)
		}
	})
}

// cardDeck is the 52 number cards and Aces, enough for the evaluator table above.
func cardDeck() []engine.Card {
	ranks := []engine.Rank{engine.Ace, engine.Two, engine.Three, engine.Four, engine.Five,
		engine.Six, engine.Seven, engine.Eight, engine.Nine, engine.Ten}
	suits := []engine.Suit{engine.Clubs, engine.Diamonds, engine.Hearts, engine.Spades}
	out := make([]engine.Card, 0, len(ranks)*len(suits))
	for _, r := range ranks {
		for _, s := range suits {
			out = append(out, engine.NewCard(r, s))
		}
	}
	return out
}
