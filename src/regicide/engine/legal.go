package engine

import "sort"

// Action is a player intent. The engine validates it; the client never decides
// rules.
type Action interface{ actionName() string }

// Play commits cards from hand in step 1 (one card, a same-rank combo, or an
// Animal Companion paired with one other card).
type Play struct {
	CardIDs []CardID `json:"cardIds"`
}

// FlipJester spends a solo Jester to discard the hand and refill.
type FlipJester struct{}

// Yield skips steps 2 and 3 and suffers the attack (solo: only with the setting).
type Yield struct{}

// PayDamage discards one or more cards to cover enemy damage. Cards are applied in
// order: the run of damage stops at the required total (see applyPayDamage).
type PayDamage struct {
	CardIDs []CardID `json:"cardIds"`
	// CardID is the pre-multi-select wire form; still accepted so an old client or a
	// stored action decodes.
	CardID CardID `json:"cardId,omitempty"`
}

// IDs returns the cards this payment names, in the order they will be discarded.
func (p PayDamage) IDs() []CardID {
	if len(p.CardIDs) > 0 {
		return p.CardIDs
	}
	if p.CardID != "" {
		return []CardID{p.CardID}
	}
	return nil
}

// EndPayment ends step 4 early when over-paying is allowed.
type EndPayment struct{}

// ChooseNextSeat is the multiplayer Jester effect (unused in solo).
type ChooseNextSeat struct {
	Seat int `json:"seat"`
}

func (Play) actionName() string           { return "play" }
func (FlipJester) actionName() string     { return "flip_jester" }
func (Yield) actionName() string          { return "yield" }
func (PayDamage) actionName() string      { return "pay_damage" }
func (EndPayment) actionName() string     { return "end_payment" }
func (ChooseNextSeat) actionName() string { return "choose_next_seat" }

// ActionName exposes the wire name of an action.
func ActionName(a Action) string { return a.actionName() }

// LegalActions enumerates everything the active seat may do right now. The
// liveness invariant asserts this is non-empty unless the game is over.
func LegalActions(g *Game) []Action {
	if g.gameOver() {
		return nil
	}
	// A lobby has a zero game: no seats, no cards, nothing to do. Returning empty
	// rather than indexing hand() keeps the projection safe before a deal.
	if len(g.Players) == 0 || g.ActiveSeat < 0 || g.ActiveSeat >= len(g.Players) {
		return nil
	}
	out := make([]Action, 0, 16)

	// A Jester's "who goes next" is a blocking choice: until it is made the only
	// legal action is naming a seat.
	if g.SeatChoice != nil {
		for _, seat := range g.SeatChoice.Next {
			out = append(out, ChooseNextSeat{Seat: seat})
		}
		return out
	}

	switch g.Phase {
	case Step1Await:
		if g.canFlipJester() {
			out = append(out, FlipJester{})
		}
		if g.yieldLegal() {
			out = append(out, Yield{})
		}
		for _, play := range LegalPlays(g) {
			out = append(out, play)
		}
	case Step4Pre:
		// Step 4 opens with the Jester window: flipping is always legal while a
		// Jester remains, but paying is only offered when the hand can actually
		// cover the attack (otherwise discarding would just be a slower loss).
		if g.canFlipJester() {
			out = append(out, FlipJester{})
		}
		if g.handValueTotal() >= g.Step4Required {
			for _, c := range g.hand() {
				out = append(out, PayDamage{CardIDs: []CardID{c.ID}})
			}
		}
	case Step4Discard:
		for _, c := range g.hand() {
			out = append(out, PayDamage{CardIDs: []CardID{c.ID}})
		}
		if g.Rules.AllowOverpayDiscard && g.Step4Paid >= g.Step4Required {
			out = append(out, EndPayment{})
		}
	}
	return out
}

// previewAttack is the damage a play would deal right now: the card values, doubled
// when a club is present and the enemy is not club-immune. It is the single source
// of truth for the number the Play button shows.
func (g *Game) previewAttack(play Play) int {
	attack := 0
	clubs := false
	for _, id := range play.CardIDs {
		for _, c := range g.hand() {
			if c.ID != id {
				continue
			}
			attack += c.PlayValue()
			if c.Suit == Clubs {
				clubs = true
			}
			break
		}
	}
	if clubs && !(g.Enemy.Suit == Clubs && !g.ImmunityCancelled) {
		attack *= 2
	}
	return attack
}

// effectiveShield is the spade shield that actually applies against the current
// enemy. Spades played while a spades enemy is still immune accumulate but only
// start reducing the attack once immunity is cancelled (docs/DECISIONS.md
// ADR-006 §4: accumulate unconditionally, gate at step 4).
// EffectiveShield is the public form of the shield that applies against the current
// royal: zero while a spades royal still blocks it, even though the points are banked.
func (g *Game) EffectiveShield() int { return g.effectiveShield() }

// effectiveShield is the shield that actually applies against the current royal.
func (g *Game) effectiveShield() int {
	if g.Enemy.Suit == Spades && !g.ImmunityCancelled {
		return 0
	}
	return g.Shield
}

// incomingDamage is what the enemy would deal right now.
func (g *Game) incomingDamage() int {
	in := g.EnemyAttack() - g.effectiveShield()
	if in < 0 {
		return 0
	}
	return in
}

// LegalPlays returns every legal step-1 play, in a deterministic order (hand
// order, then combinations by size) so server responses are stable.
func LegalPlays(g *Game) []Play {
	hand := g.hand()
	plays := make([]Play, 0, 32)

	// Singles: every card may be played alone (a Jester deals 0 and skips steps
	// 3-4, which only exists in the multiplayer deck).
	for _, c := range hand {
		plays = append(plays, Play{CardIDs: []CardID{c.ID}})
	}

	// Animal Companion pairings: companion + one other card (never a Joker), or
	// companion + companion. Same-suit pairs are legal (power applies once).
	companions := make([]Card, 0, 4)
	others := make([]Card, 0, len(hand))
	for _, c := range hand {
		if c.IsCompanion() {
			companions = append(companions, c)
		} else if !c.IsJoker() {
			others = append(others, c)
		}
	}
	seen := map[string]bool{}
	addPair := func(a, b Card) {
		key := pairKey(a.ID, b.ID)
		if seen[key] {
			return
		}
		seen[key] = true
		plays = append(plays, Play{CardIDs: []CardID{a.ID, b.ID}})
	}
	for _, a := range companions {
		for _, b := range others {
			addPair(a, b)
		}
		for _, b := range companions {
			if a.ID != b.ID {
				addPair(a, b)
			}
		}
	}

	// Same-rank combos of 2-4 cards whose total value is 10 or less.
	byRank := map[Rank][]Card{}
	for _, c := range hand {
		if c.IsJoker() || c.IsCompanion() {
			continue
		}
		byRank[c.Rank] = append(byRank[c.Rank], c)
	}
	ranks := make([]int, 0, len(byRank))
	for rank := range byRank {
		ranks = append(ranks, int(rank))
	}
	sort.Ints(ranks)
	for _, rank := range ranks {
		cards := byRank[Rank(rank)]
		value := RankValue(Rank(rank))
		// Every subset counts: same-rank cards differ by suit, and only the
		// suits in the play decide whether clubs double, hearts heal, diamonds
		// draw or spades shield. Offering only the first N cards hid legal,
		// materially different moves from the UI.
		for size := 2; size <= 4 && size <= len(cards); size++ {
			if value*size > 10 {
				break
			}
			for _, combo := range combinations(cards, size) {
				plays = append(plays, Play{CardIDs: idsOf(combo)})
			}
		}
	}
	return plays
}

// combinations returns every size-length subset of cards, in hand order.
func combinations(cards []Card, size int) [][]Card {
	if size <= 0 || size > len(cards) {
		return nil
	}
	if size == len(cards) {
		return [][]Card{append([]Card(nil), cards...)}
	}
	out := make([][]Card, 0, 6)
	var walk func(start int, acc []Card)
	walk = func(start int, acc []Card) {
		if len(acc) == size {
			out = append(out, append([]Card(nil), acc...))
			return
		}
		for i := start; i < len(cards); i++ {
			walk(i+1, append(acc, cards[i]))
		}
	}
	walk(0, nil)
	return out
}

func idsOf(cards []Card) []CardID {
	ids := make([]CardID, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	return ids
}

func pairKey(a, b CardID) string {
	if a > b {
		a, b = b, a
	}
	return string(a) + "+" + string(b)
}

// ValidatePlay checks a play against the rules and returns the cards in hand
// order. It is used both by Transition and by the client's legality preview.
func ValidatePlay(g *Game, ids []CardID) ([]Card, error) {
	if len(ids) == 0 {
		return nil, illegal("empty play")
	}
	if len(ids) > 4 {
		return nil, illegal("a play is at most 4 cards")
	}
	seen := map[CardID]bool{}
	cards := make([]Card, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, illegal("duplicate card %s", id)
		}
		seen[id] = true
		c, ok := g.findInHand(id)
		if !ok {
			return nil, illegal("card %s is not in hand", id)
		}
		cards = append(cards, c)
	}

	// A Joker is always played alone.
	for _, c := range cards {
		if c.IsJoker() && len(cards) > 1 {
			return nil, illegal("a Jester is played on its own")
		}
	}

	companions := 0
	ranks := map[Rank]bool{}
	for _, c := range cards {
		if c.IsCompanion() {
			companions++
		} else if !c.IsJoker() {
			ranks[c.Rank] = true
		}
	}

	switch {
	case len(cards) == 1:
		return cards, nil
	case companions > 0:
		// Companion + one card, or companion + companion. Never a combo member.
		if len(cards) != 2 {
			return nil, illegal("an Animal Companion pairs with exactly one other card")
		}
		if companions == 1 {
			// The other card must not be a number-combo partner: it is a pair, fine.
			return cards, nil
		}
		if companions == 2 {
			return cards, nil
		}
		return nil, illegal("an Animal Companion pairs with exactly one other card")
	default:
		// Numbers only: same rank, 2-4 cards, total value <= 10.
		if len(ranks) != 1 {
			return nil, illegal("a combo needs cards of the same number")
		}
		var rank Rank
		for r := range ranks {
			rank = r
		}
		if RankValue(rank)*len(cards) > 10 {
			return nil, illegal("a combo's total value cannot exceed 10")
		}
		return cards, nil
	}
}

// findInHand looks a card up in the active hand.
func (g *Game) findInHand(id CardID) (Card, bool) {
	for _, c := range g.hand() {
		if c.ID == id {
			return c, true
		}
	}
	return Card{}, false
}

// removeFromHand removes the given ids and returns the new hand.
func removeFromHand(hand []Card, ids []CardID) []Card {
	drop := map[CardID]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	// A fresh slice, not hand[:0]: the filter would otherwise write over the caller's
	// backing array, which is only safe while nobody else holds a slice of it. Eight
	// cards is not worth that reasoning.
	out := make([]Card, 0, len(hand))
	for _, c := range hand {
		if !drop[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
