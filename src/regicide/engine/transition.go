package engine

// Transition applies one action and returns the new state plus the events it
// produced, in the order they happened. The receiver state is never mutated: the
// engine clones on entry so a caller can keep the old state for replay, tests and
// request retries.
//
// All events are appended to Game.Log by the helpers below; the returned slice is
// the slice of Log added by this transition. That keeps one ordering, one source
// of truth and no duplicated "lose" events.
import "strconv"

func Transition(g Game, a Action, r Rand) (Game, []Event, error) {
	if g.gameOver() {
		return g, nil, illegal("the game is over")
	}
	// A pending Jester choice is a gate on the whole action set, not a rule inside one
	// handler: while a seat is choosing who acts next, the only legal action is that
	// choice. Enforcing it in the dispatch means a new action type cannot forget it
	// (F-6).
	if g.SeatChoice != nil {
		if _, ok := a.(ChooseNextSeat); !ok {
			return g, nil, illegal("a seat choice is pending")
		}
	}
	next := clone(g)
	start := len(next.Log)

	var err error
	switch act := a.(type) {
	case Play:
		err = applyPlay(&next, act, r)
	case FlipJester:
		err = applyFlipJester(&next)
	case Yield:
		err = applyYield(&next)
	case PayDamage:
		err = applyPayDamage(&next, act)
	case EndPayment:
		err = applyEndPayment(&next)
	case ChooseNextSeat:
		err = applyChooseNextSeat(&next, act)
	default:
		err = illegal("unknown action")
	}
	if err != nil {
		return g, nil, err
	}

	// Slice out this transition's events before trimming, so the cap can never
	// shift the offset and hand the client someone else's events.
	events := append([]Event{}, next.Log[start:]...)
	next.Log = trimLogTo(next.Log, next.logCap())
	return next, events, nil
}

// maxLog bounds the stored history. A full solo game emits well under 400 events,
// so the cap only ever trims pathological games; it keeps persisted sessions small.
// A co-op turn emits one event per seat per phase, so the cap doubles there — a
// three-seat game was hitting the solo ceiling and losing the early history that a
// replay needs to be worth watching.
const maxLog = 512
const maxLogCoop = 1024

// logCap is the ceiling for this game: solo keeps 512, a table keeps 1024.
func (g *Game) logCap() int {
	if len(g.Players) > 1 {
		return maxLogCoop
	}
	return maxLog
}

// log appends events to the game history, stamping the current turn on the way in.
func (g *Game) log(evs ...Event) {
	for i := range evs {
		if evs[i].Turn == 0 {
			evs[i].Turn = g.TurnNo
		}
	}
	g.Log = append(g.Log, evs...)
}

// NoteEvent appends a server-side note (a host change) and applies the same cap a rule
// event gets. The room used to append straight to Log, so host churn could grow the
// log past the ceiling the snapshot and replay contracts assume.
func (g *Game) NoteEvent(ev Event) {
	if ev.Turn == 0 {
		ev.Turn = g.TurnNo
	}
	g.Log = append(g.Log, ev)
	g.Log = trimLogTo(g.Log, g.logCap())
}

// trimLog keeps the newest maxLog entries; trimLogTo is the same with an explicit cap.
func trimLogTo(log []Event, cap int) []Event {
	if len(log) <= cap {
		return log
	}
	out := make([]Event, cap)
	copy(out, log[len(log)-cap:])
	return out
}

// applyPlay resolves steps 1-3 (and the defeat cascade) for one play.
func applyPlay(g *Game, act Play, r Rand) error {
	if g.Phase != Step1Await {
		return illegal("cards can only be played in step 1 (phase %s)", g.Phase)
	}
	cards, err := ValidatePlay(g, act.CardIDs)
	if err != nil {
		return err
	}

	attack := 0
	suitSet := map[Suit]bool{}
	hasJoker := false
	for _, c := range cards {
		attack += c.PlayValue()
		if c.IsJoker() {
			hasJoker = true
			continue
		}
		suitSet[c.Suit] = true
	}

	g.setHand(removeFromHand(g.hand(), act.CardIDs))

	// A Jester is played alone: it cancels immunity, skips steps 3 and 4, and the
	// turn passes on. Solo never reaches this branch (Jesters are set aside).
	if hasJoker {
		g.Discard = append(g.Discard, cards...)
		g.ImmunityCancelled = true
		g.Players[g.ActiveSeat].YieldedLastTurn = false
		// One event for the play, and it names its seat. The old code logged the Jester
		// twice, the second time without a seat: the rail then credited the card to
		// seat 0 and the bot's own row kept its previous attack (playtest 4, notes 6–7).
		g.log(Event{Type: EvPlay, Cards: cloneIDs(act.CardIDs), Note: "jester: immunity cancelled", Seat: g.ActiveSeat})
		if len(g.Players) > 1 {
			// Co-op: the Jester's player chooses who goes next (R-049). The turn
			// does not advance until that choice arrives.
			g.SeatChoice = &SeatChoice{By: g.ActiveSeat, Next: allSeats(len(g.Players))}
			return nil
		}
		g.endTurn()
		return nil
	}

	g.log(Event{Type: EvPlay, Cards: cloneIDs(act.CardIDs), Amount: attack, Seat: g.ActiveSeat})
	g.AttackArea = append(g.AttackArea, cards...)
	g.Players[g.ActiveSeat].YieldedLastTurn = false

	// Step 2a — hearts, before diamonds. Cards move under the tavern deck.
	if g.suitActive(suitSet, Hearts) {
		n := attack
		if n > len(g.Discard) {
			n = len(g.Discard)
		}
		if n > 0 {
			shuffleCounted(g.Discard, r, &g.RGNDraws)
			moved := append([]Card(nil), g.Discard[:n]...)
			g.Discard = g.Discard[n:]
			g.Tavern = append(g.Tavern, moved...)
			// Note carries the attack value that was requested, so the history can say
			// "3 of 5 returned" when the discard pile was smaller than the play.
			g.log(Event{Type: EvHearts, Amount: n, Note: "requested " + strconv.Itoa(attack)})
		}
	}

	// Step 2b — diamonds.
	if g.suitActive(suitSet, Diamonds) {
		counts := g.drawRoundRobin(attack)
		if len(g.Players) > 1 {
			// One event per seat so every player's draw can be animated. Seats draw
			// in clockwise order, so the events keep that order.
			for i := 0; i < len(counts); i++ {
				seat := (g.ActiveSeat + i) % len(counts)
				if counts[seat] > 0 {
					g.log(Event{Type: EvDraw, Amount: counts[seat], Seat: seat})
				}
			}
		} else {
			total := 0
			for _, n := range counts {
				total += n
			}
			if total > 0 {
				g.log(Event{Type: EvDraw, Amount: total, Seat: g.ActiveSeat})
			}
		}
	}

	// Step 2c — spades accumulate unconditionally (immunity only gates step 4, so
	// a spade played before a Jester still counts once immunity is cancelled).
	if suitSet[Spades] {
		g.Shield += attack
		g.log(Event{Type: EvShield, Amount: g.Shield})
	}

	// Step 3 — damage, doubled by clubs unless the enemy is immune to clubs.
	damage := attack
	if g.suitActive(suitSet, Clubs) {
		damage *= 2
	}
	g.Damage += damage
	g.log(Event{Type: EvDamage, Amount: damage, Note: "total " + strconv.Itoa(g.Damage)})

	if g.Damage >= g.EnemyHealth() {
		g.defeatCascade()
		return nil
	}

	g.enterStep4()
	return nil
}

// defeatCascade resolves a defeated enemy in rulebook order: enemy away (top of
// the tavern on an exact kill), attack area to discard, reveal next enemy, and a
// new turn for the player who struck the blow.
func (g *Game) defeatCascade() {
	exact := g.Damage == g.EnemyHealth()
	enemy := g.Enemy
	g.log(Event{Type: EvDefeat, Cards: []CardID{enemy.ID}, Amount: g.Damage, Note: exactNote(exact)})

	if exact && g.Rules.ExactDamage {
		g.Tavern = append([]Card{enemy}, g.Tavern...)
		g.log(Event{Type: EvRecruit, Cards: []CardID{enemy.ID}})
	} else {
		g.Discard = append(g.Discard, enemy)
	}
	g.Discard = append(g.Discard, g.AttackArea...)
	g.AttackArea = nil

	// The win check precedes any reveal: the last enemy ends the game.
	if len(g.Castle) == 0 {
		g.Enemy = Card{}
		g.Damage = 0
		g.Shield = 0
		tier := TierFor(g.JestersUsed)
		g.Phase = Won
		g.log(Event{Type: EvTier, Note: string(tier)}, Event{Type: EvWin, Note: string(tier)})
		return
	}

	g.revealNext()
	// The rulebook gives the killer the next turn (R-034). PassOnDefeat is the
	// easier table variant: the next seat takes the fresh royal instead.
	if g.Rules.PassOnDefeat && len(g.Players) > 1 {
		g.ActiveSeat = (g.ActiveSeat + 1) % len(g.Players)
	}
	g.TurnNo++
	g.log(Event{Type: EvEnemyReveal, Cards: []CardID{g.Enemy.ID}, Seat: g.ActiveSeat})
	g.enterTurnStart()
}

// applyFlipJester spends a solo Jester: discard the hand and refill from the top
// of the tavern. It is not a draw for diamond-immunity purposes and it never
// cancels enemy immunity.
func applyFlipJester(g *Game) error {
	if !g.canFlipJester() {
		return illegal("a Jester cannot be flipped now (phase %s, %d left)", g.Phase, g.JestersLeft)
	}
	was := g.Phase

	g.Discard = append(g.Discard, g.hand()...)
	g.setHand(nil)
	refill := g.MaxHand
	if len(g.Tavern) < refill {
		refill = len(g.Tavern)
	}
	g.setHand(g.draw(nil, refill))
	g.JestersLeft--
	g.JestersUsed++
	g.log(Event{Type: EvJesterFlip, Amount: refill, Note: "hand refilled"})

	switch was {
	case Step1Await:
		// Window A closed: with an empty hand and no Jester left, the player cannot act.
		if g.handSize() == 0 && !g.canFlipJester() {
			g.loseWith(LostCannotAct)
		}
	case Step4Pre:
		// Window B: more Jesters may still be spent, and the new hand may pay.
		if g.handValueTotal() >= g.Step4Required {
			g.Phase = Step4Pre // payment is now offered alongside the remaining window
			return nil
		}
		if g.canFlipJester() {
			return nil // a second flip is still possible before damage
		}
		g.loseWith(LostCannotDefend)
	}
	return nil
}

// applyYield skips steps 2 and 3 and suffers the attack. It is a setting in solo
// (off by default) and is always available in multiplayer. It is illegal when it
// would do nothing, which keeps games finite.
func applyYield(g *Game) error {
	if g.SeatChoice != nil {
		return illegal("choose who goes next first")
	}
	if g.Phase != Step1Await {
		return illegal("yield is only legal in step 1")
	}
	if !g.yieldLegal() {
		if g.incomingDamage() == 0 {
			return illegal("yield is illegal when the enemy deals no damage")
		}
		if len(g.Players) == 1 {
			return illegal("yielding is disabled in this game")
		}
		return illegal("every other player has not yielded since their last turn")
	}
	g.Players[g.ActiveSeat].YieldedLastTurn = true
	g.log(Event{Type: EvYield, Seat: g.ActiveSeat})
	g.enterStep4()
	return nil
}

// applyChooseNextSeat resolves a Jester's "who goes next" choice. Only the seat
// that played the Jester may act, and only to name one of the offered seats.
func applyChooseNextSeat(g *Game, act ChooseNextSeat) error {
	if g.SeatChoice == nil {
		return illegal("no seat choice is pending")
	}
	found := false
	for _, seat := range g.SeatChoice.Next {
		if seat == act.Seat {
			found = true
			break
		}
	}
	if !found {
		return illegal("seat %d cannot go next", act.Seat)
	}
	by := g.SeatChoice.By
	g.SeatChoice = nil
	g.ActiveSeat = act.Seat
	g.TurnNo++
	g.log(Event{Type: EvChooseNextSeat, Seat: by, Amount: act.Seat, Note: "seat chosen"})
	g.log(Event{Type: EvTurnEnd, Seat: act.Seat})
	g.enterTurnStart()
	return nil
}

// allSeats returns 0..n-1 in order: every seat, the chooser included, may go next.
func allSeats(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// applyPayDamage discards one or more cards towards the enemy attack and ends the
// turn automatically when the required total is reached, unless over-paying is
// enabled. Cards are applied in the order the player gave them, so a batch that
// would overshoot before its last card is rejected rather than silently truncated.
func applyPayDamage(g *Game, act PayDamage) error {
	if g.Phase != Step4Pre && g.Phase != Step4Discard {
		return illegal("damage is not being paid right now (phase %s)", g.Phase)
	}
	if g.Phase == Step4Pre && g.handValueTotal() < g.Step4Required {
		// The hand cannot cover the attack: the only legal move is a Jester flip.
		// Rejecting keeps a stale client resynchronising instead of silently losing.
		return illegal("the hand cannot cover the attack; flip a Jester or give up")
	}
	ids := act.IDs()
	if len(ids) == 0 {
		return illegal("no cards named to pay with")
	}
	seen := make(map[CardID]bool, len(ids))
	cards := make([]Card, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			return illegal("card %s was named twice", id)
		}
		seen[id] = true
		card, ok := g.findInHand(id)
		if !ok {
			return illegal("card %s is not in hand", id)
		}
		cards = append(cards, card)
	}
	// Without over-paying, a card may not be discarded once the total is already
	// covered: that is the stop-at-threshold rule, applied to every card but the last.
	if !g.Rules.AllowOverpayDiscard {
		paid := g.Step4Paid
		for i := 0; i < len(cards)-1; i++ {
			paid += cards[i].DiscardValue()
			if paid >= g.Step4Required {
				return illegal("%s already covers the attack; discard fewer cards", cards[i].ID)
			}
		}
	}

	// The Jester window closes once discarding has begun.
	g.Phase = Step4Discard
	for _, card := range cards {
		g.setHand(removeFromHand(g.hand(), []CardID{card.ID}))
		g.Discard = append(g.Discard, card)
		g.Step4Paid += card.DiscardValue()
		// The payer is the actor. A payment is its own action (the bot pays in one
		// batch), so without the seat the client cannot attribute the card: the bubble
		// kept the previous attack and the defence reveal never fired.
		g.log(Event{Type: EvSuffer, Seat: g.ActiveSeat, Cards: []CardID{card.ID}, Amount: card.DiscardValue(), Note: "paid " + strconv.Itoa(g.Step4Paid) + "/" + strconv.Itoa(g.Step4Required)})
	}

	// Defensive: if what was paid plus the remaining hand can no longer cover the
	// attack, the player has died. Legal move generation never offers such a
	// payment, but a hand-crafted API call must not leave the game stuck.
	if g.Step4Paid+g.handValueTotal() < g.Step4Required {
		g.loseWith(LostCannotDefend)
		return nil
	}

	if !g.Rules.AllowOverpayDiscard && g.Step4Paid >= g.Step4Required {
		g.log(Event{Type: EvEnemyAttack, Amount: g.Step4Required, Note: "defended"})
		g.endTurn()
	}
	return nil
}

// applyEndPayment closes step 4 when over-paying is enabled.
func applyEndPayment(g *Game) error {
	if !g.Rules.AllowOverpayDiscard {
		return illegal("over-paying is disabled in this game")
	}
	if g.Phase != Step4Discard {
		return illegal("payment is not open (phase %s)", g.Phase)
	}
	if g.Step4Paid < g.Step4Required {
		return illegal("cannot end payment before covering the attack")
	}
	paid := g.Step4Paid
	g.log(Event{Type: EvEnemyAttack, Amount: paid, Note: "payment ended"})
	g.endTurn()
	return nil
}

// suitActive reports whether a suit's power fires: it does unless the enemy has
// that suit and its immunity has not been cancelled.
func (g *Game) suitActive(suits map[Suit]bool, want Suit) bool {
	if !suits[want] {
		return false
	}
	if g.Enemy.Suit == want && !g.ImmunityCancelled {
		return false
	}
	return true
}

func exactNote(exact bool) string {
	if exact {
		return "exact"
	}
	return "overkill"
}

// cloneIDs copies a card id slice so history and idempotency caches cannot be
// mutated through the caller's decoded request.
func cloneIDs(src []CardID) []CardID {
	if src == nil {
		return nil
	}
	out := make([]CardID, len(src))
	copy(out, src)
	return out
}

// clone deep-copies a Game so transitions cannot alias the caller's slices.
func clone(g Game) Game {
	out := g
	out.Players = make([]Player, len(g.Players))
	for i, p := range g.Players {
		out.Players[i] = p
		out.Players[i].Hand = append([]Card(nil), p.Hand...)
	}
	if g.SeatChoice != nil {
		choice := *g.SeatChoice
		choice.Next = append([]int(nil), g.SeatChoice.Next...)
		out.SeatChoice = &choice
	}
	out.Castle = append([]Card(nil), g.Castle...)
	out.Tavern = append([]Card(nil), g.Tavern...)
	out.Discard = append([]Card(nil), g.Discard...)
	out.AttackArea = append([]Card(nil), g.AttackArea...)
	out.Log = append([]Event(nil), g.Log...)
	return out
}

// countingRand counts stream draws so determinism tests can assert consumption.
type countingRand struct {
	r     Rand
	draws *int
}

func (c countingRand) Uint64() uint64 {
	*c.draws++
	return c.r.Uint64()
}

// shuffleCounted shuffles and records how many stream values were consumed.
func shuffleCounted(cards []Card, r Rand, draws *int) {
	shuffleFisherYates(cards, countingRand{r: r, draws: draws})
}
