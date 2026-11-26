package engine

import (
	"strconv"
	"strings"
	"testing"
)

// ---- test helpers ---------------------------------------------------------

// parseCard builds a Card from an id like "10H", "AS", "KD", "J1".
func parseCard(t *testing.T, id string) Card {
	t.Helper()
	if id == "J1" || id == "J2" {
		return Card{ID: CardID(id), Rank: Joker, Suit: NoSuit}
	}
	if len(id) < 2 {
		t.Fatalf("bad card id %q", id)
	}
	suit := id[len(id)-1]
	rankPart := id[:len(id)-1]
	var suitVal Suit
	switch suit {
	case 'C':
		suitVal = Clubs
	case 'D':
		suitVal = Diamonds
	case 'H':
		suitVal = Hearts
	case 'S':
		suitVal = Spades
	default:
		t.Fatalf("bad suit in %q", id)
	}
	var rank Rank
	switch rankPart {
	case "A":
		rank = Ace
	case "J":
		rank = Jack
	case "Q":
		rank = Queen
	case "K":
		rank = King
	default:
		n, err := strconv.Atoi(rankPart)
		if err != nil || n < 2 || n > 10 {
			t.Fatalf("bad rank in %q", id)
		}
		rank = Rank(n)
	}
	return Card{ID: CardID(id), Rank: rank, Suit: suitVal}
}

func cards(t *testing.T, ids ...string) []Card {
	t.Helper()
	out := make([]Card, len(ids))
	for i, id := range ids {
		out[i] = parseCard(t, id)
	}
	return out
}

func ids(cs ...string) []CardID {
	out := make([]CardID, len(cs))
	for i, c := range cs {
		out[i] = CardID(c)
	}
	return out
}

// baseGame returns a deterministic solo game with scripted randomness so pile
// order never matters unless a test sets it.
func baseGame(t *testing.T) Game {
	t.Helper()
	g := NewGame(SetupOptions{
		Seed:       "TEST-TEST-TEST",
		SeedRandom: false,
		Theme:      "official",
		Rules:      DefaultRules(),
		NumPlayers: 1,
	}, NewScriptedRand())
	return g
}

// scenario builds a game and lets the test drive the piles directly.
func scenario(t *testing.T, mod func(*Game)) Game {
	t.Helper()
	g := baseGame(t)
	g.Phase = Step1Await
	g.LostReason = LostNone
	g.Players[0].Hand = nil
	g.Tavern = nil
	g.Discard = nil
	g.AttackArea = nil
	g.Castle = nil
	g.Damage = 0
	g.Shield = 0
	g.Step4Required = 0
	g.Step4Paid = 0
	if mod != nil {
		mod(&g)
	}
	return g
}

func setEnemy(g *Game, t *testing.T, id string) {
	t.Helper()
	g.Enemy = parseCard(t, id)
	g.Damage = 0
	g.Shield = 0
	g.ImmunityCancelled = false
}

func apply(t *testing.T, g Game, a Action) (Game, []Event) {
	t.Helper()
	next, evs, err := Transition(g, a, NewScriptedRand())
	if err != nil {
		t.Fatalf("Transition(%s): %v", ActionName(a), err)
	}
	return next, evs
}

func applyErr(t *testing.T, g Game, a Action) error {
	t.Helper()
	_, _, err := Transition(g, a, NewScriptedRand())
	return err
}

// zoneCounts returns a multiset of every card id in play, for conservation tests.
func zoneCounts(g Game) map[CardID]int {
	out := map[CardID]int{}
	add := func(cs []Card) {
		for _, c := range cs {
			out[c.ID]++
		}
	}
	add(g.Castle)
	add(g.Tavern)
	add(g.Discard)
	add(g.AttackArea)
	if !g.Enemy.ID.Empty() {
		out[g.Enemy.ID]++
	}
	for _, p := range g.Players {
		add(p.Hand)
	}
	return out
}

// totalCards counts every card in play, the solo Jesters set aside (unused and
// spent), and, once won, the final enemy which no longer occupies a zone.
func totalCards(g Game) int {
	n := g.JestersLeft + g.JestersUsed
	for _, v := range zoneCounts(g) {
		n += v
	}
	return n
}

// ---- R-001..R-005: setup and invariants -----------------------------------

func TestR001_SoloSetupSizes(t *testing.T) {
	g := baseGame(t)
	if got := len(g.Players[0].Hand); got != 8 {
		t.Errorf("hand = %d, want 8", got)
	}
	if got := len(g.Tavern); got != 32 {
		t.Errorf("tavern = %d, want 32", got)
	}
	if got := len(g.Discard); got != 0 {
		t.Errorf("discard = %d, want 0", got)
	}
	if got := len(g.Castle); got != 11 {
		t.Errorf("castle = %d, want 11 after reveal", got)
	}
	if !g.Enemy.IsRoyal() || g.Enemy.Rank != Jack {
		t.Errorf("enemy = %s, want a Jack", g.Enemy.ID)
	}
	if g.JestersLeft != 2 || g.JestersUsed != 0 {
		t.Errorf("jesters = %d/%d, want 2 left 0 used", g.JestersLeft, g.JestersUsed)
	}
	if got := totalCards(g); got != 54 {
		t.Errorf("total cards = %d, want 54", got)
	}
}

func TestR002_DeckComposition(t *testing.T) {
	g := baseGame(t)
	tavern := append([]Card(nil), g.Tavern...)
	tavern = append(tavern, g.Players[0].Hand...)
	companions, jokers, numbered := 0, 0, 0
	for _, c := range tavern {
		switch {
		case c.IsCompanion():
			companions++
		case c.IsJoker():
			jokers++
		default:
			numbered++
		}
	}
	if companions != 4 || jokers != 0 || numbered != 36 {
		t.Errorf("tavern+hand = %d companions, %d jokers, %d numbered; want 4/0/36", companions, jokers, numbered)
	}

	castle := append([]Card{g.Enemy}, g.Castle...)
	jack, queen, king := 0, 0, 0
	for _, c := range castle {
		switch c.Rank {
		case Jack:
			jack++
		case Queen:
			queen++
		case King:
			king++
		}
	}
	if jack != 4 || queen != 4 || king != 4 {
		t.Errorf("castle = %dJ/%dQ/%dK, want 4/4/4", jack, queen, king)
	}
}

func TestR003_CastleOrder(t *testing.T) {
	g := baseGame(t)
	want := []Rank{Jack, Jack, Jack, Queen, Queen, Queen, Queen, King, King, King, King}
	for i, want := range want {
		if i >= len(g.Castle) {
			t.Fatalf("castle too short at %d", i)
		}
		if g.Castle[i].Rank != want {
			t.Fatalf("castle[%d] = %s (%v), want %v", i, g.Castle[i].ID, g.Castle[i].Rank, want)
		}
	}
}

func TestR004_CardConservation(t *testing.T) {
	g := baseGame(t)
	r := NewScriptedRand()
	for step := 0; step < 200 && !g.gameOver(); step++ {
		acts := LegalActions(&g)
		if len(acts) == 0 {
			t.Fatalf("no legal actions at %s", g.Phase)
		}
		next, _, err := Transition(g, acts[step%len(acts)], r)
		if err != nil {
			t.Fatalf("transition: %v", err)
		}
		g = next
		if got := totalCards(g); got != 54 {
			t.Fatalf("card count = %d at step %d, want 54", got, step)
		}
		for id, n := range zoneCounts(g) {
			if n != 1 {
				t.Fatalf("card %s appears %d times", id, n)
			}
		}
	}
}

func TestR005_HandLimit(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JD")
		g.Players[0].Hand = cards(t, "2C", "3C", "4C", "5C", "6C", "7C", "8C", "9C")
		g.Tavern = cards(t, "10C", "AC", "2D", "3D")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")}) // diamonds? no: clubs, so no draw
	if len(g.Players[0].Hand) != 7 {
		t.Fatalf("hand = %d after playing a club, want 7", len(g.Players[0].Hand))
	}
	// The limit is enforced per hand: an empty slice may take up to MaxHand.
	g.Players[0].Hand = cards(t, "2C", "3C", "4C", "5C", "6C", "7C", "8C", "9C")
	if h := g.draw(nil, 4); len(h) != 4 {
		t.Fatalf("drew %d into an empty slice, want 4", len(h))
	}
	if h := g.draw(nil, 12); len(h) != len(g.Tavern) && len(h) > g.MaxHand {
		t.Fatalf("drew %d cards, above the limit %d", len(h), g.MaxHand)
	}
}

// ---- R-056..R-059: model guarantees ---------------------------------------

func TestR056_SeatsAreParameterised(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "SEATS", Rules: DefaultRules(), NumPlayers: 2}, NewScriptedRand())
	if len(g.Players) != 2 {
		t.Fatalf("players = %d, want 2", len(g.Players))
	}
	if g.MaxHand != 7 {
		t.Fatalf("maxHand = %d, want 7 for two players", g.MaxHand)
	}
	for i, p := range g.Players {
		if len(p.Hand) != 7 {
			t.Fatalf("seat %d hand = %d, want 7", i, len(p.Hand))
		}
	}
	// Two-player deck keeps Jesters in the tavern (0 of them) and supports plays.
	g.Players[0].Hand = cards(t, "2C", "3C")
	next, _, err := Transition(g, Play{CardIDs: ids("2C")}, NewScriptedRand())
	if err != nil {
		t.Fatalf("two-seat play: %v", err)
	}
	if next.ActiveSeat != 0 {
		t.Fatalf("active seat changed mid-turn to %d", next.ActiveSeat)
	}
}

func TestR057_Determinism(t *testing.T) {
	run := func() Game {
		r := NewSeededRand("AAAA-AAAA-AAAA")
		g := NewGame(SetupOptions{Seed: "AAAA-AAAA-AAAA", Rules: DefaultRules(), NumPlayers: 1}, r)
		for i := 0; i < 60 && !g.gameOver(); i++ {
			acts := LegalActions(&g)
			if len(acts) == 0 {
				break
			}
			next, _, err := Transition(g, acts[0], r)
			if err != nil {
				t.Fatalf("transition: %v", err)
			}
			g = next
		}
		return g
	}
	a, b := run(), run()
	if a.Phase != b.Phase || a.Damage != b.Damage || a.TurnNo != b.TurnNo || a.RGNDraws != b.RGNDraws {
		t.Fatalf("states diverged: %+v vs %+v", a.Phase, b.Phase)
	}
	if got := totalCards(a); got != 54 {
		t.Fatalf("cards = %d, want 54", got)
	}
	if len(a.Tavern) != len(b.Tavern) || len(a.Discard) != len(b.Discard) {
		t.Fatalf("pile sizes diverged")
	}
	for i := range a.Tavern {
		if a.Tavern[i].ID != b.Tavern[i].ID {
			t.Fatalf("tavern[%d] = %s vs %s", i, a.Tavern[i].ID, b.Tavern[i].ID)
		}
	}
}

func TestR058_HeartsShuffleIsDeterministic(t *testing.T) {
	setup := func() Game {
		return scenario(t, func(g *Game) {
			setEnemy(g, t, "JS")
			g.Players[0].Hand = cards(t, "5H")
			g.Discard = cards(t, "2C", "3C", "4C", "5C", "6C", "7D")
		})
	}
	moved := func() []CardID {
		g := setup()
		next, _, err := Transition(g, Play{CardIDs: ids("5H")}, NewSeededRand("HEARTS-SEED"))
		if err != nil {
			t.Fatalf("play: %v", err)
		}
		// The last 5 tavern cards are the moved ones (they go under the deck).
		moved := next.Tavern[len(next.Tavern)-5:]
		out := make([]CardID, len(moved))
		for i, c := range moved {
			out[i] = c.ID
		}
		return out
	}
	a, b := moved(), moved()
	if strings.Join(cardIDsToStrings(a), ",") != strings.Join(cardIDsToStrings(b), ",") {
		t.Fatalf("hearts shuffle differs: %v vs %v", a, b)
	}
	if len(a) != 5 {
		t.Fatalf("moved %d cards, want 5", len(a))
	}
}

func TestR059_CountersNeverNegative(t *testing.T) {
	r := NewSeededRand("NONNEG")
	g := NewGame(SetupOptions{Seed: "NONNEG", Rules: DefaultRules(), NumPlayers: 1}, r)
	for i := 0; i < 400 && !g.gameOver(); i++ {
		acts := LegalActions(&g)
		if len(acts) == 0 {
			break
		}
		next, _, err := Transition(g, acts[i%len(acts)], r)
		if err != nil {
			t.Fatalf("transition: %v", err)
		}
		g = next
		if g.Shield < 0 || g.Damage < 0 || g.JestersLeft < 0 || g.JestersUsed < 0 {
			t.Fatalf("negative counter: shield=%d damage=%d jesters=%d/%d", g.Shield, g.Damage, g.JestersLeft, g.JestersUsed)
		}
		if g.Rules.SoloJester && (g.JestersLeft > 2 || g.JestersUsed > 2) {
			t.Fatalf("jester accounting broken: %d left, %d used", g.JestersLeft, g.JestersUsed)
		}
	}
}

func cardIDsToStrings(ids []CardID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
