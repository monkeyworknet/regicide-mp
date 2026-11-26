package bot

import (
	"math"
	"sort"

	"regicide/engine"
)

// Greedy is policy v1: economy-aware, one action at a time.
//
// The economy is the whole game. Every turn the active seat must pay the royal's attack
// out of its hand, and the hand is refilled from a finite tavern, so the resource that
// runs out is *cards*. A play is therefore judged by what the whole turn costs —
// the cards it spends plus the cheapest payment the rest of the hand can still make —
// against the damage it buys. Paying with the biggest cards is the classic way to lose:
// it burns hand value on a debt that two small cards would have settled.
func Greedy(obs Observation) engine.Action {
	return greedyWith(obs, DefaultWeights())
}

// legalFor builds the action list from the observation alone: the policies never see a
// Game, so they cannot be tempted by a state they would not be allowed to know. The
// caller still validates against engine.LegalActions.
func legalFor(obs Observation) []engine.Action {
	out := make([]engine.Action, 0, 16)
	if len(obs.Choices) > 0 {
		for _, seat := range obs.Choices {
			out = append(out, engine.ChooseNextSeat{Seat: seat})
		}
		return out
	}
	if obs.Phase == engine.Step4Pre || obs.Phase == engine.Step4Discard {
		if obs.FlipLegal {
			out = append(out, engine.FlipJester{})
		}
		// The engine only offers a discard when the hand can cover the bill, and only
		// offers to stop once it is paid.
		cover := 0
		for _, c := range obs.Hand {
			cover += c.DiscardValue()
		}
		if cover >= obs.Required {
			for _, c := range obs.Hand {
				out = append(out, engine.PayDamage{CardIDs: []engine.CardID{c.ID}})
			}
		}
		if obs.Required <= 0 {
			out = append(out, engine.EndPayment{})
		}
		return out
	}
	if obs.FlipLegal {
		out = append(out, engine.FlipJester{})
	}
	if obs.YieldLegal {
		out = append(out, engine.Yield{})
	}
	for _, play := range plays(obs) {
		out = append(out, play)
	}
	return out
}

// plays enumerates the legal plays: every single card, the same-rank combos the rules
// allow, and the Animal Companion pairings worth considering.
func plays(obs Observation) []engine.Play {
	out := make([]engine.Play, 0, len(obs.Hand)+16)
	for _, c := range obs.Hand {
		// Every card is a legal single, an Animal Companion included (the engine offers
		// it alone as well as paired; the pairing is a stronger option, not a condition).
		out = append(out, engine.Play{CardIDs: []engine.CardID{c.ID}})
	}
	byRank := map[engine.Rank][]engine.Card{}
	for _, c := range obs.Hand {
		if jester(c) || isCompanion(c) {
			continue
		}
		byRank[c.Rank] = append(byRank[c.Rank], c)
	}
	ranks := make([]engine.Rank, 0, len(byRank))
	for r := range byRank {
		ranks = append(ranks, r)
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i] < ranks[j] })
	for _, r := range ranks {
		group := byRank[r]
		// Every same-rank subset, not just a prefix in hand order: the suits in the
		// subset decide which powers are spent (diamonds draw, clubs double, spades
		// shield, hearts heal), so "the first two" is not the same play as "the two
		// clubs". A hand is at most eight cards, so this stays tiny.
		for n := 2; n <= len(group) && n <= 4; n++ {
			eachCombination(len(group), n, func(idx []int) {
				pick := make([]engine.Card, 0, n)
				for _, i := range idx {
					pick = append(pick, group[i])
				}
				if playable(pick) {
					out = append(out, engine.Play{CardIDs: ids(pick)})
				}
			})
		}
	}
	// Animal Companion pairings: every Ace with every partner, because the partner's
	// suit is the power the pair buys.
	var aces, others []engine.Card
	for _, c := range obs.Hand {
		if isCompanion(c) {
			aces = append(aces, c)
		} else if !jester(c) {
			others = append(others, c)
		}
	}
	for _, ace := range aces {
		for _, partner := range others {
			pair := []engine.Card{ace, partner}
			if playable(pair) {
				out = append(out, engine.Play{CardIDs: ids(pair)})
			}
		}
	}
	// Companion + companion is legal too (two Aces: attack 2, both powers once each).
	for i := 0; i < len(aces); i++ {
		for j := i + 1; j < len(aces); j++ {
			pair := []engine.Card{aces[i], aces[j]}
			if playable(pair) {
				out = append(out, engine.Play{CardIDs: ids(pair)})
			}
		}
	}
	return out
}

// eachCombination calls fn for every k-subset of 0..n-1, in index order, so the caller
// can build a play without allocating a combinatorial list first.
func eachCombination(n, k int, fn func([]int)) {
	if k <= 0 || k > n {
		return
	}
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	for {
		fn(idx)
		i := k - 1
		for i >= 0 && idx[i] == n-k+i {
			i--
		}
		if i < 0 {
			return
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}

// playOutcome is what a play would do, computed from the observation alone.
type playOutcome struct {
	play      engine.Play
	cards     []engine.Card
	damage    int
	kills     bool
	shield    int
	heal      int
	healCards int // cards a hearts play returns from the discard
	draws     int // diamond cards in the play (the old proxy)
	drawCards int // cards the diamond power actually draws, across the table
	drawSelf  int // of those, the cards that land in the acting seat's hand
	spent     int
	rest      []engine.Card
	payCost   int // the cheapest payment the rest of the hand can make
	payable   bool
	unlock    bool // a Jester in the play, cancelling the royal's immunity
}

// evaluatePlay mirrors the rules closely enough to choose between plays; the engine
// remains the only judge of what is legal.
func evaluatePlay(obs Observation, play engine.Play, w Weights) playOutcome {
	cards := make([]engine.Card, 0, len(play.CardIDs))
	used := map[engine.CardID]bool{}
	for _, id := range play.CardIDs {
		for _, c := range obs.Hand {
			if c.ID == id && !used[id] {
				cards = append(cards, c)
				used[id] = true
				break
			}
		}
	}
	out := playOutcome{play: play, cards: cards}
	clubs, spades, hearts := false, false, false
	attackValue := 0
	for _, c := range cards {
		if jester(c) {
			out.unlock = true
		}
		out.damage += c.PlayValue()
		out.spent += c.PlayValue()
		attackValue += c.PlayValue()
		if c.Suit == obs.Enemy.Suit && !obs.ImmunityCancelled {
			continue // the royal is immune to its own suit's power, not to the damage
		}
		switch c.Suit {
		case engine.Clubs:
			clubs = true
		case engine.Spades:
			spades = true
		case engine.Hearts:
			hearts = true
		case engine.Diamonds:
			out.draws++
		}
	}
	if clubs {
		out.damage *= 2
	}
	// The rulebook resolves a suit power once at the play's TOTAL attack value, not per
	// card: a 3♥+3♣ combo heals six, and 5♠+5♣ shields ten. Summing only the
	// suited cards understated every mixed-rank pair with a spade or a heart.
	if spades {
		out.shield = attackValue
	}
	if hearts {
		// The engine moves min(attack, len(Discard)) cards; crediting the full attack
		// when the discard is thin made a heart look like a refill it cannot be.
		out.healCards = attackValue
		if out.healCards > obs.Discard {
			out.healCards = obs.Discard
		}
		out.heal = out.healCards
	}
	if out.draws > 0 && (obs.Enemy.Suit != engine.Diamonds || obs.ImmunityCancelled) && obs.Tavern+out.healCards > 0 {
		// drawRoundRobin mirrors the engine: one card at a time, starting at the acting
		// seat, skipping a seat at its limit, and stopping when the tavern is empty.
		caps := make([]int, obs.Seats)
		for i := range caps {
			held := 0
			if i == obs.Seat {
				held = len(obs.Hand) - len(cards)
			} else if i < len(obs.HandCounts) {
				held = obs.HandCounts[i]
			}
			caps[i] = obs.MaxHand - held
			if caps[i] < 0 {
				caps[i] = 0
			}
		}
		left := attackValue
		if cap := obs.Tavern + out.healCards; left > cap {
			left = cap
		}
		for left > 0 {
			progress := false
			for i := 0; i < obs.Seats && left > 0; i++ {
				seat := (obs.Seat + i) % obs.Seats
				if caps[seat] == 0 {
					continue
				}
				caps[seat]--
				out.drawCards++
				if seat == obs.Seat {
					out.drawSelf++
				}
				left--
				progress = true
			}
			if !progress {
				break
			}
		}
	}
	out.kills = out.damage >= obs.Health
	for _, c := range obs.Hand {
		if !used[c.ID] {
			out.rest = append(out.rest, c)
		}
	}
	shieldUse := out.shield
	if cap := obs.Attack - obs.Shield; shieldUse > cap {
		shieldUse = cap
	}
	if shieldUse < 0 {
		shieldUse = 0
	}
	need := obs.Attack - obs.Shield - shieldUse
	if w.DrawCover > 0 && out.drawSelf > 0 && need > 0 {
		need -= int(math.Round(float64(out.drawSelf) * w.DrawCover))
	}
	// A kill and a Jester both skip step 4 entirely: the royal is gone, or the Jester
	// hands the turn on without a payment. The old model charged a Jester play the
	// whole bill, so the bot treated its escape card as the most expensive play.
	if out.kills || out.unlock {
		need = 0
	}
	if need < 0 {
		need = 0
	}
	out.payCost, out.payable = cheapestPayment(out.rest, need, w, obs.Enemy.Suit, obs.ImmunityCancelled)
	return out
}

// cheapestPayment finds the least hand value that covers need, preferring an exact
// cover and then the fewest cards. A hand is at most eight cards, so the search is over
// 256 subsets and costs nothing.
// paymentValue is what a card costs in the payment model: its discard value scaled by
// the policy's suit order, with Aces treated as the combo cards they are. One is the
// neutral (raw value) model.
func (w Weights) paymentValue(c engine.Card, blocked engine.Suit, cancelled bool) float64 {
	m := 1.0
	switch c.Suit {
	case engine.Hearts:
		m = orOne(w.PayHeart)
	case engine.Clubs:
		m = orOne(w.PayClub)
	case engine.Spades:
		m = orOne(w.PaySpade)
	case engine.Diamonds:
		m = orOne(w.PayDiamond)
	}
	if w.BlockedPay && !cancelled && c.Suit != engine.NoSuit && c.Suit == blocked {
		// The royal blocks this suit's power this turn, so its "keep it for later"
		// premium is void. Pay with it if the value is right.
		m = 1
	}
	if c.IsCompanion() {
		m *= orOne(w.PayAce)
	}
	return float64(c.DiscardValue()) * m
}

func cheapestPayment(hand []engine.Card, need int, w Weights, blocked engine.Suit, cancelled bool) (int, bool) {
	if need <= 0 {
		return 0, true
	}
	if len(hand) > 16 {
		// Not reachable in Regicide, but never exponentiate on an unbounded input.
		total := 0
		for _, c := range hand {
			total += c.DiscardValue()
		}
		if total < need {
			return total, false
		}
		return need, true
	}
	bestCost, bestCount := -1.0, -1
	n := len(hand)
	for mask := 1; mask < 1<<n; mask++ {
		sum, count, cost := 0, 0, 0.0
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sum += hand[i].DiscardValue()
				cost += w.paymentValue(hand[i], blocked, cancelled)
				count++
			}
		}
		if sum < need {
			continue
		}
		if over := sum - need; over > 0 {
			cost += float64(over) * w.PayOverpay
		}
		better := bestCost < 0 ||
			cost < bestCost-1e-9 ||
			(math.Abs(cost-bestCost) < 1e-9 && count < bestCount)
		if better {
			bestCost, bestCount = cost, count
		}
	}
	if bestCost < 0 {
		total := 0
		for _, c := range hand {
			total += c.DiscardValue()
		}
		return total, false
	}
	return int(math.Round(bestCost)), true
}

// paymentCards returns the cheapest set of cards that covers need: least total value
// first, then fewest cards. Ties are broken by card id so a game is reproducible.
func paymentCards(hand []engine.Card, need int, w Weights, blocked engine.Suit, cancelled bool) []engine.Card {
	if need <= 0 {
		return nil
	}
	if len(hand) > 16 {
		return nil
	}
	n := len(hand)
	bestCost, bestCount, bestMask := -1.0, -1, 0
	for mask := 1; mask < 1<<n; mask++ {
		sum, count, cost := 0, 0, 0.0
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sum += hand[i].DiscardValue()
				cost += w.paymentValue(hand[i], blocked, cancelled)
				count++
			}
		}
		if sum < need {
			continue
		}
		if over := sum - need; over > 0 {
			cost += float64(over) * w.PayOverpay
		}
		if bestCost < 0 || cost < bestCost-1e-9 || (math.Abs(cost-bestCost) < 1e-9 && count < bestCount) {
			bestCost, bestCount, bestMask = cost, count, mask
		}
	}
	if bestMask == 0 {
		return nil
	}
	out := make([]engine.Card, 0, bestCount)
	for i := 0; i < n; i++ {
		if bestMask&(1<<i) != 0 {
			out = append(out, hand[i])
		}
	}
	return out
}

// Weights are the policy's judgement, in one place so the bench can tune them rather
// than a hunch being buried in an if-statement.
type Weights struct {
	Kill        float64 // ending the royal, which also cancels the payment
	Shield      float64 // each point of attack a spade removes from the bill
	Heal        float64 // each card a heart puts back under the tavern
	Draw        float64 // each card a diamond draws
	RoyalInPlay float64 // penalty per point of a Queen or King spent as damage
	Overkill    float64 // penalty per point of damage beyond the royal's health
	HandDeep    float64 // value of finishing the turn with cards still in hand
	Tavern      float64 // value of leaving cards in the tavern for later turns
	// Unlock values a Jester played while the royal is still immune: it opens the suit
	// powers the table has been unable to use. Zero is the old "never play a Jester"
	// behaviour, which lost games with a Jester in hand (corpus, 2026-11-18).
	Unlock float64
	// DiamondUnlock is an extra Unlock point per diamond point still in hand when the
	// royal is a diamond. Diamonds are the draw engine, so opening that suit is worth
	// more than opening hearts or spades (operator's strategy, 2026-11-18).
	DiamondUnlock float64
	// Payment suit multipliers: the search treats a card as this many times its discard
	// value when it is spent on the royal's attack. One is neutral (the old policy).
	// A player's suit order is a real strategy: hearts are weak cards, diamonds are the
	// engine's card draw, and an Ace is a combo waiting to happen.
	PayHeart, PayClub, PaySpade, PayDiamond, PayAce float64
	// HeartPlay penalises spending a heart as damage; hearts are payment fodder.
	HeartPlay float64
	// HealDiamonds is the value of one expected diamond recovered by a hearts play.
	// The discard is public, so the expected diamonds returned are
	// healCards * discardDiamonds / discard.
	HealDiamonds float64
	// DrawCards values one card actually drawn by a diamond play, and DrawSelf values
	// the acting seat's share of it. The draw is round-robin and stops at every seat's
	// hand limit, so a high diamond into full hands draws almost nothing. Zero keeps
	// the older per-diamond-card proxy (Draw).
	DrawCards float64
	DrawSelf  float64
	// ExactKill values landing the killing blow exactly: the royal goes on top of the
	// tavern, so the table's next draw is a face card instead of a number card.
	ExactKill float64
	// DelayKill discourages finishing a royal that can no longer hurt the table. When
	// the attack is fully shielded, another turn can draw cards and put discard cards
	// back under the tavern before the next royal arrives.
	DelayKill float64
	// LastDiamond penalises spending the hand's only diamond of value 5+ as a play:
	// it is the table's refill, and losing it can hand-lock the seat.
	LastDiamond float64
	// DiamondRest values a diamond point kept in hand; it is the other side of the same
	// judgement, and it makes spending a high diamond costlier than spending a low one.
	DiamondRest float64
	// DrawPast scales the value of own-hand draws beyond six cards, where another card
	// rarely changes the tactical outcome. One is the undiminished value.
	DrawPast float64
	// BurstRoyal values clubs and spades against a diamond Queen or King, whose immunity
	// blocks the draw the table would otherwise lean on.
	BurstRoyal float64
	// ComboFive values a same-rank five combo, the cheapest exact-ten play.
	ComboFive float64
	// HeartFace penalises spending a face-card heart on healing; it is a card that a
	// later fight needs more than this heal does.
	HeartFace float64
	// DrawCover is the payment capacity credited to each card the acting seat draws.
	// Diamonds resolve before step 4, so a drawn card can settle the same bill; the
	// payment search does not see it. Keep it conservative: the value is unknown.
	DrawCover float64
	// PayOverpay prices each point a payment exceeds the bill. The subset search
	// minimises weighted card value, which can prefer a big heart that overshoots to a
	// precise set of clubs; the waste is real deck value even when the suits say the
	// heart is cheap.
	PayOverpay float64
	// SpadeEarly values a spade on the first play of a turn, when the shield still has
	// the whole enemy attack to absorb.
	SpadeEarly float64
	// Parity penalises a non-killing play that leaves an odd remainder against an even
	// health total: exact damage is harder to find next turn.
	Parity float64
	// ComboPenalty subtracts a fixed cost per extra card in a multi-card play.
	ComboPenalty float64
	// BlockedPay voids the suit premium when the royal on the table is immune to that
	// suit: a diamond still "draws" nothing against a diamond royal, so paying with it
	// is no worse than paying with a heart. Aces keep their premium: an Animal
	// Companion's pairing is not a suit power.
	BlockedPay bool
	// BlockPenalty prices keeping cards in a suit that is more likely than average to
	// be the next royal's suit: that suit's power is the one the royal would block.
	BlockPenalty float64
	// Diversity rewards keeping power in more than one suit, which is the cheap hedge
	// against any single royal blocking the hand.
	Diversity float64
}

// keepPower is what a suit's power is worth if the next royal does not block it:
// diamonds draw cards, clubs double damage, spades shield the bill, hearts heal.
func (w Weights) keepPower(c engine.Card) float64 {
	switch c.Suit {
	case engine.Diamonds:
		return w.Draw
	case engine.Clubs:
		return float64(c.PlayValue())
	case engine.Spades:
		return float64(c.PlayValue()) * w.Shield
	case engine.Hearts:
		return float64(c.PlayValue()) * w.Heal
	}
	return 0
}

func orOne(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// DefaultWeights is the tuned policy v1 (see docs/BOT.md for the bench that chose it).
func DefaultWeights() Weights {
	return Weights{
		Kill: 26, Shield: 0.55, Heal: 0.5, Draw: 5.0,
		RoyalInPlay: 0.55, Overkill: 0.15, HandDeep: 0.1, Tavern: 0.05,
		Unlock: 0,
	}
}

// GreedyWith is Greedy at an explicit weighting. It is how a persona is built without
// copying the policy: the weights are the judgement, the search is shared.
func GreedyWith(w Weights) Policy {
	return func(obs Observation) engine.Action { return greedyWith(obs, w) }
}

// greedyWith is Greedy at an explicit weighting, for tuning and for tests.
func greedyWith(obs Observation, w Weights) engine.Action {
	// A Jester's "who goes next" blocks everything else. Give the turn to the seat with
	// the most cards, because that is the seat best able to pay; hand sizes are public.
	if len(obs.Choices) > 0 {
		// Most cards first, because that is the seat best able to pay; lowest seat breaks
		// the tie so a game is reproducible.
		best := obs.Choices[0]
		for _, seat := range obs.Choices {
			count := func(x int) int {
				if x >= 0 && x < len(obs.HandCounts) {
					return obs.HandCounts[x]
				}
				return 0
			}
			if count(seat) > count(best) || (count(seat) == count(best) && seat < best) {
				best = seat
			}
		}
		return engine.ChooseNextSeat{Seat: best}
	}
	if obs.Phase == engine.Step4Pre || obs.Phase == engine.Step4Discard {
		return greedyPayment(obs, w)
	}
	best := engine.Action(nil) // the best play that can still pay
	bestScore := -1e9
	doomed := engine.Action(nil) // the best play that cannot: only used when nothing can
	doomedScore := -1e9
	for _, play := range plays(obs) {
		out := evaluatePlay(obs, play, w)
		// Damage per card spent, where the spend includes the payment it forces.
		cost := float64(out.spent + out.payCost)
		if out.kills {
			cost = float64(out.spent)
		}
		score := 0.0
		if cost > 0 {
			score = float64(out.damage) / cost * 10
		} else {
			score = float64(out.damage) * 10
		}
		if out.kills {
			score += w.Kill
			// Damage past the royal's health is spent for nothing.
			score -= float64(out.damage-obs.Health) * w.Overkill
			if out.damage == obs.Health {
				score += w.ExactKill
			}
			// A royal that cannot hurt the table is a free setup turn: its shield is
			// already paid, so drawing and healing now costs no exposure.
			if w.DelayKill > 0 && obs.Required == 0 {
				score -= w.DelayKill
			}
		}
		// A shield is not worth one payment: it reduces the bill on every remaining turn
		// against this royal. Estimate those turns from the health left and the damage a
		// turn can realistically bring (the hand refills to its limit from the tavern).
		turnsLeft := 1
		if !out.kills && obs.Health > 0 {
			perTurn := obs.MaxHand * 6 / 2 // a card averages ~6 and half of them reach the table
			if perTurn < 1 {
				perTurn = 1
			}
			// The shield's future value is over the health this play leaves, not the
			// health it found: a big hit shortens the royal's life and the shield's.
			left := obs.Health - out.damage
			if left < 1 {
				left = 1
			}
			turnsLeft = (left + perTurn - 1) / perTurn
			if turnsLeft > 6 {
				turnsLeft = 6
			}
		}
		use := out.shield
		if cap := obs.Attack - obs.Shield; use > cap {
			use = cap
		}
		if use < 0 {
			use = 0
		}
		if !out.kills {
			// Defeating the royal clears the shield with it; a killing spade buys no
			// future discount. Shield beyond the bill cannot discount more than the bill.
			score += float64(use) * w.Shield * float64(turnsLeft)
		}
		// Hearts put cards back under the tavern. That is worth nothing while the deck is
		// deep and a great deal when it is not: the table dies of an empty deck.
		if obs.Tavern < obs.MaxHand*obs.Seats {
			score += float64(out.heal) * w.Heal * 4
		}
		score += float64(out.heal) * w.Heal
		if w.HealDiamonds > 0 && out.healCards > 0 && obs.Discard > 0 {
			expected := float64(out.healCards) * float64(obs.DiscardDiamonds) / float64(obs.Discard)
			score += expected * w.HealDiamonds
		}
		if out.drawCards > 0 {
			score += float64(out.draws) * w.Draw
		}
		score += float64(out.drawCards) * w.DrawCards
		selfValue := float64(out.drawSelf)
		if w.DrawPast > 0 && w.DrawPast < 1 && out.drawSelf > 0 {
			held := len(obs.Hand) - len(out.cards)
			cap := 6
			if obs.MaxHand < cap {
				cap = obs.MaxHand
			}
			if over := held + out.drawSelf - cap; over > 0 {
				selfValue -= float64(over) * (1 - w.DrawPast)
			}
		}
		score += selfValue * w.DrawSelf
		if w.DiamondRest > 0 {
			for _, c := range out.rest {
				if c.Suit == engine.Diamonds {
					score += float64(c.PlayValue()) * w.DiamondRest
				}
			}
		}
		if w.LastDiamond > 0 {
			playedDiamond, handDiamonds := 0, 0
			for _, c := range obs.Hand {
				if c.Suit == engine.Diamonds {
					handDiamonds++
				}
			}
			for _, c := range out.cards {
				if c.Suit == engine.Diamonds {
					playedDiamond++
				}
			}
			if handDiamonds == 1 && playedDiamond == 1 {
				for _, c := range out.cards {
					if c.Suit == engine.Diamonds && c.PlayValue() >= 5 {
						score -= w.LastDiamond
					}
				}
			}
		}
		if w.HeartPlay > 0 {
			for _, c := range out.cards {
				if c.Suit == engine.Hearts {
					score -= float64(c.PlayValue()) * w.HeartPlay
				}
			}
		}
		if w.ComboPenalty > 0 && len(out.play.CardIDs) > 1 {
			score -= float64(len(out.play.CardIDs)-1) * w.ComboPenalty
		}
		if w.BurstRoyal > 0 && obs.Enemy.Suit == engine.Diamonds &&
			(obs.Enemy.Rank == engine.Queen || obs.Enemy.Rank == engine.King) {
			for _, c := range out.cards {
				if c.Suit == engine.Clubs || c.Suit == engine.Spades {
					score += float64(c.PlayValue()) * w.BurstRoyal
				}
			}
		}
		if w.ComboFive > 0 && len(out.play.CardIDs) >= 2 {
			fives := 0
			for _, c := range out.cards {
				if c.Rank == engine.Five {
					fives++
				}
			}
			if fives >= 2 {
				score += float64(fives) * w.ComboFive
			}
		}
		if w.HeartFace > 0 {
			for _, c := range out.cards {
				if c.Suit == engine.Hearts && c.Rank >= engine.Jack {
					score -= w.HeartFace
				}
			}
		}
		if w.SpadeEarly > 0 && obs.Damage == 0 && out.shield > 0 {
			for _, c := range out.cards {
				if c.Suit == engine.Spades {
					score += float64(c.PlayValue()) * w.SpadeEarly
				}
			}
		}
		if w.Parity > 0 && !out.kills && (obs.Health-out.damage)%2 != 0 {
			score -= w.Parity
		}
		if out.unlock && !obs.ImmunityCancelled && obs.Enemy.Suit != engine.NoSuit {
			// The royal's own suit is the one the table cannot use. A Jester is the only
			// card that unlocks it, and it costs the seat nothing but the card. A diamond
			// royal is the best target: every diamond left in hand becomes a draw.
			score += w.Unlock
			if obs.Enemy.Suit == engine.Diamonds && w.DiamondUnlock > 0 {
				diamonds := 0
				for _, c := range out.rest {
					if c.Suit == engine.Diamonds {
						diamonds += c.PlayValue()
					}
				}
				score += float64(diamonds) * w.DiamondUnlock
			}
		}
		for _, c := range out.cards {
			if c.PlayValue() >= 15 {
				score -= float64(c.PlayValue()) * w.RoyalInPlay / 10
			}
		}
		score += float64(len(out.rest)-out.payCost/4) * w.HandDeep
		score += float64(obs.Tavern+out.heal) * w.Tavern
		if w.BlockPenalty > 0 {
			next := 0
			for _, n := range obs.NextSuits {
				next += n
			}
			if next > 0 {
				for _, c := range out.rest {
					if c.Suit == engine.NoSuit {
						continue
					}
					p := float64(obs.NextSuits[c.Suit]) / float64(next)
					if excess := p - 0.25; excess > 0 {
						score -= w.keepPower(c) * excess * w.BlockPenalty
					}
				}
			}
		}
		if w.Diversity > 0 {
			seen := map[engine.Suit]bool{}
			for _, c := range out.rest {
				if c.Suit != engine.NoSuit {
					seen[c.Suit] = true
				}
			}
			score += float64(len(seen)) * w.Diversity
		}
		// A play that cannot pay the attack afterwards loses the table, so it is ranked
		// below every play that can. It is still ranked: when NOTHING can pay, the seat
		// is doomed either way, and taking the most damage with it is strictly better
		// than refusing to act.
		if !out.kills && !out.payable {
			score -= 1e6
		}
		// The last card is the seat's whole hand: spending it is a real cost when it
		// leaves a bill, and no cost at all when it does not.
		if obs.LeastCard && !out.kills && out.payCost > 0 {
			score -= 1e5
		}
		if !out.kills && !out.payable {
			if score > doomedScore {
				doomedScore = score
				doomed = play
			}
			continue
		}
		if score > bestScore {
			bestScore = score
			best = play
		}
	}
	if best != nil {
		return best
	}
	// No play can pay the attack. Yielding keeps the cards and takes the damage on the
	// chin, which is better than spending a hand that cannot settle the bill anyway.
	if obs.YieldLegal {
		return engine.Yield{}
	}
	if doomed != nil {
		return doomed
	}
	if obs.FlipLegal {
		return engine.FlipJester{}
	}
	// Nothing legal is left, which the engine treats as a loss at turn start. Say
	// something the engine already refuses rather than inventing an action.
	return engine.EndPayment{}
}

func greedyPayment(obs Observation, w Weights) engine.Action {
	need := obs.Required
	if need <= 0 {
		return engine.EndPayment{}
	}
	cards := paymentCards(obs.Hand, need, w, obs.Enemy.Suit, obs.ImmunityCancelled)
	if len(cards) == 0 {
		return engine.EndPayment{}
	}
	return engine.PayDamage{CardIDs: ids(cards)}
}
