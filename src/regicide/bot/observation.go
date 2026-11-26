// Package bot plays Regicide with a policy instead of a person.
//
// Two rules shape everything here:
//
//  1. A bot may only know what the seat it plays knows. Observation is built from the
//     engine's own per-seat snapshot plus the public table, and it never holds another
//     hand, the tavern's order, or the seed. The bench measures the same observation,
//     or its numbers would be measuring a cheat (T7.5).
//  2. A policy returns one legal action. It does not bend rules, and the caller applies
//     the action through the same Transition the server uses.
package bot

import (
	"sort"

	"regicide/engine"
)

// Observation is everything one seat can see: its own hand, the royal on the table,
// the public counters, and what is left in the castle and tavern by count. It carries
// no other seat's cards, no tavern order, and no seed.
type Observation struct {
	Seat   int
	Seats  int
	Hand   []engine.Card
	Enemy  engine.Card
	Health int // the royal's remaining health
	Attack int // the royal's base attack
	Damage int // damage already dealt to it this turn
	Shield int // shield raised this turn
	// Required is what this seat must pay in step 4, already net of shield.
	Required int
	Paid     int
	Phase    engine.Phase
	Castle   int // royals still to be dealt
	// CastleSuits is the public suit composition of the castle (clubs, diamonds,
	// hearts, spades). The next royal is unknown; this is what makes its chance of
	// blocking a suit computable.
	CastleSuits [4]int
	// NextSuits is the suit count of the rank on top of the castle (the next royal's
	// block): Jacks first, then Queens, then Kings. The order inside the block is
	// hidden; the odds of the next royal blocking a suit are exact from this.
	NextSuits [4]int
	Tavern    int // cards left in the tavern
	// Discard is public, and its composition matters: a hearts play returns cards from
	// it to the tavern, so a discard full of diamonds makes hearts worth more.
	Discard         int
	DiscardDiamonds int
	// JestersLeft is how many Jester flips the table has left; a public resource.
	JestersLeft int
	// MaxHand is the seat's own hand limit.
	MaxHand int
	// ImmunityCancelled is the public fact that a Jester has opened the royal's suit
	// powers. A policy that holds a Jester may want to cancel them before spending the
	// hand on damage the suit would otherwise reduce.
	ImmunityCancelled bool
	// YieldLegal and FlipLegal are whether those options are on offer. The rules decide
	// that, not the bot, so the observation carries the answer rather than a guess.
	YieldLegal bool
	FlipLegal  bool
	// LeastCard is true when the hand is down to its last card: spending it means
	// relying on the draw, so the economy policy avoids it while it has a choice.
	LeastCard bool

	// HandCounts is every seat's hand size. It is public (D-44), so a bot may use it.
	HandCounts []int
	// Choices is non-empty while a Jester is waiting to name the next seat: until that
	// is answered nothing else is legal, which is exactly what the engine enforces.
	Choices []int
}

// Observe turns a game into one seat's observation. It is the only constructor, so a
// policy cannot accidentally be handed the full state.
func Observe(g engine.Game, seat int) (Observation, bool) {
	if seat < 0 || seat >= len(g.Players) {
		return Observation{}, false
	}
	obs := Observation{
		Seat:              seat,
		Seats:             len(g.Players),
		Hand:              append([]engine.Card(nil), g.Players[seat].Hand...),
		Enemy:             g.Enemy,
		Health:            g.EnemyHealth() - g.Damage,
		Attack:            g.EnemyAttack(),
		Damage:            g.Damage,
		Shield:            g.EffectiveShield(), // immune-spade points are banked, not paid
		Phase:             g.Phase,
		Castle:            len(g.Castle),
		Tavern:            len(g.Tavern),
		Discard:           len(g.Discard),
		JestersLeft:       g.JestersLeft,
		MaxHand:           g.MaxHand,
		LeastCard:         len(g.Players[seat].Hand) <= 1,
		ImmunityCancelled: g.ImmunityCancelled,
	}
	for _, c := range g.Discard {
		if c.Suit == engine.Diamonds {
			obs.DiscardDiamonds++
		}
	}
	for _, c := range g.Castle {
		if int(c.Suit) < len(obs.CastleSuits) {
			obs.CastleSuits[c.Suit]++
		}
	}
	if len(g.Castle) > 0 {
		lowest := g.Castle[0].Rank
		for _, c := range g.Castle {
			if c.Rank < lowest {
				lowest = c.Rank
			}
		}
		for _, c := range g.Castle {
			if c.Rank == lowest && int(c.Suit) < len(obs.NextSuits) {
				obs.NextSuits[c.Suit]++
			}
		}
	}
	obs.HandCounts = make([]int, len(g.Players))
	for i := range g.Players {
		obs.HandCounts[i] = len(g.Players[i].Hand)
	}
	if g.SeatChoice != nil {
		obs.Choices = append([]int(nil), g.SeatChoice.Next...)
	}
	// Required is net of shield, exactly as the engine computes it for step 4.
	incoming := obs.Attack - obs.Shield
	if obs.Phase == engine.Step4Pre || obs.Phase == engine.Step4Discard {
		incoming = g.Step4Required - g.Step4Paid
	}
	if incoming < 0 {
		incoming = 0
	}
	obs.Required = incoming
	obs.Paid = g.Step4Paid
	for _, a := range engine.LegalActions(&g) {
		switch a.(type) {
		case engine.Yield:
			obs.YieldLegal = true
		case engine.FlipJester:
			obs.FlipLegal = true
		}
	}
	return obs, true
}

// HandValue is the sum of the hand at play value, the currency of every decision.
func (o Observation) HandValue() int {
	total := 0
	for _, c := range o.Hand {
		total += c.PlayValue()
	}
	return total
}

// sortedDesc returns the hand cheapest-to-spend first in one sense and strongest first
// in another: policies pick from here so their choices are deterministic.
func (o Observation) byValueDesc() []engine.Card {
	out := append([]engine.Card(nil), o.Hand...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PlayValue() != out[j].PlayValue() {
			return out[i].PlayValue() > out[j].PlayValue()
		}
		return out[i].Rank < out[j].Rank
	})
	return out
}

// isCompanion reports an Animal Companion, which pairs with any one other card.
func isCompanion(c engine.Card) bool { return c.IsCompanion() }

// jester reports the Jester, worth nothing to play and worth a whole hand to flip.
func jester(c engine.Card) bool { return c.IsJoker() }

// playable reports whether the given cards form a play the rules allow. The bot uses
// it to build candidate actions without asking the engine to refuse them one by one.
func playable(cards []engine.Card) bool {
	if len(cards) == 0 || len(cards) > 4 {
		return false
	}
	companions := 0
	ranks := map[engine.Rank]bool{}
	for _, c := range cards {
		if jester(c) {
			return len(cards) == 1
		}
		if isCompanion(c) {
			companions++
			continue
		}
		ranks[c.Rank] = true
	}
	switch {
	case len(cards) == 1:
		return true
	case companions > 0:
		return len(cards) == 2
	default:
		if len(ranks) != 1 {
			return false
		}
		for r := range ranks {
			if engine.RankValue(r)*len(cards) > 10 {
				return false
			}
		}
		return true
	}
}

func ids(cards []engine.Card) []engine.CardID {
	out := make([]engine.CardID, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}
