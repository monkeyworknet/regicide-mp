package engine

import (
	"fmt"
)

// Suit is a card suit. Jokers have NoSuit.
type Suit uint8

const (
	Clubs Suit = iota
	Diamonds
	Hearts
	Spades
	NoSuit
)

func (s Suit) String() string {
	switch s {
	case Clubs:
		return "clubs"
	case Diamonds:
		return "diamonds"
	case Hearts:
		return "hearts"
	case Spades:
		return "spades"
	}
	return "none"
}

func (s Suit) letter() string {
	switch s {
	case Clubs:
		return "C"
	case Diamonds:
		return "D"
	case Hearts:
		return "H"
	case Spades:
		return "S"
	}
	return ""
}

// Rank is a card rank. Joker is 0 so a zero value means "joker", which is also
// what the rules use for a Jester's attack value.
type Rank uint8

const (
	Joker Rank = 0
	Ace   Rank = 1
	Two   Rank = 2
	Three Rank = 3
	Four  Rank = 4
	Five  Rank = 5
	Six   Rank = 6
	Seven Rank = 7
	Eight Rank = 8
	Nine  Rank = 9
	Ten   Rank = 10
	Jack  Rank = 11
	Queen Rank = 12
	King  Rank = 13
)

// CardID is a stable, printable identifier: rank + suit ("AS", "10H", "KD") with
// the two Jokers named "J1" and "J2" so every card in the deck is distinguishable
// (card conservation depends on it).
type CardID string

// Card is a single playing card.
type Card struct {
	ID   CardID
	Rank Rank
	Suit Suit
}

// PlayValue is the card's attack value when played.
// Ace 1, 2-10 face value, Jack 10, Queen 15, King 20, Joker 0.
func (c Card) PlayValue() int { return RankValue(c.Rank) }

// DiscardValue is the card's value when discarded to pay enemy damage. The rules
// state Jacks count as 10, Queens 15, Kings 20 and Animal Companions 1 — the same
// mapping as PlayValue, including Jokers at 0.
func (c Card) DiscardValue() int { return RankValue(c.Rank) }

// RankValue maps a rank to its Regicide value.
func RankValue(r Rank) int {
	switch r {
	case Joker:
		return 0
	case Ace:
		return 1
	case Jack:
		return 10
	case Queen:
		return 15
	case King:
		return 20
	}
	return int(r)
}

func (c Card) String() string { return string(c.ID) }

// itoa keeps integer formatting local and allocation-light.
func rankLabel(r Rank) string {
	switch r {
	case Joker:
		return "Joker"
	case Ace:
		return "A"
	case Jack:
		return "J"
	case Queen:
		return "Q"
	case King:
		return "K"
	}
	return fmt.Sprintf("%d", int(r))
}

// NewCard builds a card from a rank and suit, deriving its id.
func NewCard(r Rank, s Suit) Card {
	if r == Joker {
		return Card{ID: "J1", Rank: Joker, Suit: NoSuit}
	}
	return Card{ID: CardID(rankLabel(r) + s.letter()), Rank: r, Suit: s}
}

// IsRoyal reports whether the card is a Jack, Queen or King (a castle card which
// can later be recruited into a hand).
func (c Card) IsRoyal() bool { return c.Rank >= Jack && c.Rank <= King }

// IsCompanion reports whether the card is an Animal Companion (an Ace).
func (c Card) IsCompanion() bool { return c.Rank == Ace }

// IsJoker reports whether the card is a Jester/Joker.
func (c Card) IsJoker() bool { return c.Rank == Joker }

var suits = []Suit{Clubs, Diamonds, Hearts, Spades}

// numberedRanks are the ranks that form the tavern deck's number cards.
var numberedRanks = []Rank{Two, Three, Four, Five, Six, Seven, Eight, Nine, Ten}

// EnemyStats returns the immutable attack and health of a royal.
// Jack 10/20, Queen 15/30, King 20/40.
func EnemyStats(r Rank) (attack, health int) {
	switch r {
	case Jack:
		return 10, 20
	case Queen:
		return 15, 30
	case King:
		return 20, 40
	}
	return 0, 0
}

// EnemyAttack is the base attack of the current enemy.
func (g *Game) EnemyAttack() int {
	a, _ := EnemyStats(g.Enemy.Rank)
	return a
}

// EnemyHealth is the health of the current enemy.
func (g *Game) EnemyHealth() int {
	_, h := EnemyStats(g.Enemy.Rank)
	return h
}
