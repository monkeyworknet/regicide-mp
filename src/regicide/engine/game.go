package engine

import "strconv"

// NewGame builds a solo game (or a multiplayer game when NumPlayers > 1) and
// performs setup: shuffle Kings, Queens, Jacks, stack the castle, shuffle the
// tavern, deal hands, reveal the first enemy. The shuffle order is fixed so a
// seed reproduces the same game everywhere (docs/PLAN.md §4.4).
func NewGame(opts SetupOptions, r Rand) Game {
	if opts.NumPlayers <= 0 {
		opts.NumPlayers = 1
	}
	if opts.Theme == "" {
		opts.Theme = "official"
	}
	g := Game{
		Version:    Version,
		Rules:      opts.Rules,
		Seed:       opts.Seed,
		SeedRandom: opts.SeedRandom,
		Theme:      opts.Theme,
		Jokers:     append([]string(nil), opts.Jokers...),
		Players:    make([]Player, opts.NumPlayers),
		MaxHand:    MaxHandSize(opts.NumPlayers),
	}

	// Setup shuffles are counted so a seeded game reports the same RGNDraws.
	setupRand := countingRand{r: r, draws: &g.RGNDraws}
	g.Castle = buildCastle(opts.Rules.Chaos, setupRand)
	g.Tavern = buildTavern(opts.NumPlayers, setupRand)
	if opts.NumPlayers == 1 && opts.Rules.SoloJester {
		g.JestersLeft = soloJesters
	}

	for seat := 0; seat < opts.NumPlayers; seat++ {
		g.Players[seat].Hand = g.draw(g.Players[seat].Hand, g.MaxHand)
	}
	g.log(Event{
		Type:   EvSetup,
		Amount: opts.NumPlayers,
		Note:   "castle " + strconv.Itoa(len(g.Castle)) + ", tavern " + strconv.Itoa(len(g.Tavern)),
	})

	g.TurnNo = 1
	g.revealNext()
	g.log(Event{Type: EvEnemyReveal, Cards: []CardID{g.Enemy.ID}})
	g.enterTurnStart()
	return g
}

// buildCastle returns the castle deck with the next card to reveal at index 0:
// Jacks on top, then Queens, then Kings at the bottom.
func buildCastle(chaos bool, r Rand) []Card {
	byRank := map[Rank][]Card{}
	for _, s := range suits {
		byRank[Jack] = append(byRank[Jack], NewCard(Jack, s))
		byRank[Queen] = append(byRank[Queen], NewCard(Queen, s))
		byRank[King] = append(byRank[King], NewCard(King, s))
	}
	if chaos {
		// Chaos mode: one shuffled pile of all twelve royals, so a King can open
		// the run and a Jack can close it. The same shuffle primitive keeps seeded
		// runs reproducible; note that shuffling 12 cards consumes 11 draws versus
		// 3×3 = 9 for the pinned ramp, so RGNDraws differs between the two modes.
		all := make([]Card, 0, 12)
		for _, rank := range []Rank{Jack, Queen, King} {
			all = append(all, byRank[rank]...)
		}
		shuffleFisherYates(all, r)
		return all
	}
	// Shuffle order is pinned: Kings, Queens, Jacks, then the tavern deck.
	for _, rank := range []Rank{King, Queen, Jack} {
		shuffleFisherYates(byRank[rank], r)
	}
	out := make([]Card, 0, 12)
	out = append(out, byRank[Jack]...) // Jacks are on top: revealed first
	out = append(out, byRank[Queen]...)
	out = append(out, byRank[King]...)
	return out
}

// buildTavern returns the draw pile with index 0 = top: 36 number cards, 4 Animal
// Companions and the Jesters required by the player count.
func buildTavern(numPlayers int, r Rand) []Card {
	cards := make([]Card, 0, 40)
	for _, rank := range numberedRanks {
		for _, s := range suits {
			cards = append(cards, NewCard(rank, s))
		}
	}
	for _, s := range suits {
		cards = append(cards, NewCard(Ace, s))
	}
	for i := 0; i < JestersInTavern(numPlayers); i++ {
		id := CardID("J2")
		if i == 0 {
			id = "J1"
		}
		cards = append(cards, Card{ID: id, Rank: Joker, Suit: NoSuit})
	}
	shuffleFisherYates(cards, r)
	return cards
}

// draw moves up to n cards from the top of the tavern into hand, respecting the
// hand limit. It returns the new hand; an empty tavern is not an error.
func (g *Game) draw(hand []Card, n int) []Card {
	for i := 0; i < n && len(g.Tavern) > 0 && len(hand) < g.MaxHand; i++ {
		hand = append(hand, g.Tavern[0])
		g.Tavern = g.Tavern[1:]
	}
	return hand
}

// drawRoundRobin implements the diamonds power: the current player draws first,
// then the others clockwise, skipping full hands, until n cards have been drawn
// or nobody can draw.
// counts[seat] is how many cards that seat drew, so the caller can emit one event
// per seat and the other players' draws can be animated.
func (g *Game) drawRoundRobin(n int) []int {
	counts := make([]int, len(g.Players))
	drawn := 0
	for drawn < n {
		progress := false
		for i := 0; i < len(g.Players) && drawn < n; i++ {
			seat := (g.ActiveSeat + i) % len(g.Players)
			if len(g.Players[seat].Hand) >= g.MaxHand || len(g.Tavern) == 0 {
				continue
			}
			g.Players[seat].Hand = append(g.Players[seat].Hand, g.Tavern[0])
			g.Tavern = g.Tavern[1:]
			counts[seat]++
			drawn++
			progress = true
		}
		if !progress {
			break
		}
	}
	return counts
}

// revealNext moves the top castle card into play, resetting per-enemy state.
func (g *Game) revealNext() {
	if len(g.Castle) == 0 {
		g.Enemy = Card{}
		return
	}
	g.Enemy = g.Castle[0]
	g.Castle = g.Castle[1:]
	g.Damage = 0
	g.Shield = 0
	g.ImmunityCancelled = false
	g.AttackArea = nil
	g.Step4Required = 0
	g.Step4Paid = 0
}

// enterTurnStart applies the only turn-start rule: a player who cannot play a
// card (and cannot flip a Jester) has lost. Window A (Jester flip) is offered
// first, which is why the check only fails when no Jester remains (D-1).
func (g *Game) enterTurnStart() {
	g.Phase = Step1Await
	if len(g.Players) > 1 {
		// Co-op: an empty hand is normal (R-042) and yielding is the escape hatch,
		// so the table only loses when there is no card, no Jester AND no yield.
		if !g.canAct() {
			g.loseWith(LostCannotAct)
		}
		return
	}
	// Solo keeps its documented rule untouched: an empty hand at step 1 is a loss.
	if g.handSize() == 0 && !g.canFlipJester() {
		g.loseWith(LostCannotAct)
	}
}

// canAct reports whether the active seat has any legal way to continue a turn.
func (g *Game) canAct() bool {
	return g.handSize() > 0 || g.canFlipJester() || g.yieldLegal()
}

// yieldLegal is the single source of truth for yielding: it must be a setting in
// solo and it is pointless when the enemy deals no damage. In co-op R-044 forbids a
// yield only when EVERY OTHER seat yielded on its own last turn — "if two players
// have yielded in a row the third player may not yield". The predicate used to
// require all of them to have yielded, which made yielding unreachable (nobody can
// be the first) and turned every empty hand into a lost table.
func (g *Game) yieldLegal() bool {
	if g.incomingDamage() == 0 || g.SeatChoice != nil {
		return false
	}
	if len(g.Players) == 1 {
		return g.Rules.AllowSoloYield
	}
	for i, p := range g.Players {
		if i != g.ActiveSeat && !p.YieldedLastTurn {
			return true
		}
	}
	return false
}

// enterStep4 computes the incoming attack (floored at 0) and decides whether the
// player must pay, may flip a Jester first, or has already lost.
func (g *Game) enterStep4() {
	incoming := g.EnemyAttack() - g.effectiveShield()
	if incoming < 0 {
		incoming = 0
	}
	g.Step4Required = incoming
	g.Step4Paid = 0

	if incoming == 0 {
		// Nothing to pay: the step resolves inside the transition.
		g.log(Event{Type: EvEnemyAttack, Amount: 0, Note: "no damage"})
		g.endTurn()
		return
	}

	g.Phase = Step4Pre
	// Window B stays open whenever a Jester remains; the player may flip, or pay
	// directly. Without a Jester the hand must cover the attack immediately.
	if g.canFlipJester() {
		return
	}
	g.resolveStep4Payment()
}

// resolveStep4Payment decides between asking for discards and losing.
func (g *Game) resolveStep4Payment() {
	if g.handValueTotal() < g.Step4Required {
		g.loseWith(LostCannotDefend)
		return
	}
	// Step4Pre keeps the (already closed or open) flip window semantics: paying is
	// offered by LegalActions from here.
	g.Phase = Step4Pre
}

// loseWith sets the terminal loss phase and records exactly one lose event.
func (g *Game) loseWith(reason LostReason) {
	g.Phase = Lost
	g.LostReason = reason
	g.LostSeat = g.ActiveSeat
	g.log(Event{Type: EvLose, Note: reason.String(), Seat: g.ActiveSeat})
}

// endTurn advances to the next seat and starts a fresh turn.
func (g *Game) endTurn() {
	if len(g.Players) > 0 {
		g.ActiveSeat = (g.ActiveSeat + 1) % len(g.Players)
	}
	// Log before the increment so the event carries the turn it happened in.
	g.log(Event{Type: EvTurnEnd, Seat: g.ActiveSeat})
	g.TurnNo++
	g.enterTurnStart()
}

// hand returns the active seat's hand.
func (g *Game) hand() []Card { return g.Players[g.ActiveSeat].Hand }

// setHand replaces the active seat's hand.
func (g *Game) setHand(h []Card) { g.Players[g.ActiveSeat].Hand = h }

// handSize is the active seat's hand size.
func (g *Game) handSize() int { return len(g.hand()) }

// handValueTotal is the total discard value of the active hand.
func (g *Game) handValueTotal() int {
	total := 0
	for _, c := range g.hand() {
		total += c.DiscardValue()
	}
	return total
}

// canFlipJester reports whether the solo Jester power is available right now.
func (g *Game) canFlipJester() bool {
	if !g.Rules.SoloJester || g.JestersLeft <= 0 {
		return false
	}
	return g.Phase == Step1Await || g.Phase == Step4Pre
}

// gameOver reports whether the game reached a terminal phase.
func (g *Game) gameOver() bool { return g.Phase == Won || g.Phase == Lost }
