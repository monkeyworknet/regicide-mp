package bot

// Personas are named weightings of the same greedy search. They exist so a difference
// in playing style is a measurement on the fixed seed suite, not an anecdote: run
// coopbench -policy cautious -n 500 -seats 3 against the greedy baseline.
//
// The names describe what the weights buy, not a character:
//   - cautious   keeps cards in hand and avoids spending the last one
//   - economist  spends the least total value per turn and hoards the tavern
//   - guardian   overvalues spades, whose shield pays every turn against the royal
//   - unlocker   plays a Jester to cancel immunity instead of ignoring it
//
// The search, the legal-action mask and the observation are identical, so a win-rate
// difference is attributable to the weights.
func Cautious() Policy {
	return GreedyWith(Weights{
		Kill: 22, Shield: 0.8, Heal: 0.7, Draw: 6.0,
		RoyalInPlay: 0.7, Overkill: 0.25, HandDeep: 0.3, Tavern: 0.1,
		Unlock: 12,
	})
}

func Economist() Policy {
	return GreedyWith(Weights{
		Kill: 24, Shield: 0.55, Heal: 0.6, Draw: 6.5,
		RoyalInPlay: 0.7, Overkill: 0.3, HandDeep: 0.35, Tavern: 0.15,
		Unlock: 10,
	})
}

func Guardian() Policy {
	return GreedyWith(Weights{
		Kill: 24, Shield: 1.1, Heal: 0.7, Draw: 5.0,
		RoyalInPlay: 0.55, Overkill: 0.15, HandDeep: 0.15, Tavern: 0.06,
		Unlock: 12,
	})
}

func Unlocker() Policy {
	return GreedyWith(Weights{
		Kill: 28, Shield: 0.6, Heal: 0.5, Draw: 5.0,
		RoyalInPlay: 0.4, Overkill: 0.15, HandDeep: 0.1, Tavern: 0.05,
		Unlock: 30,
	})
}

// Human follows the operator's stated strategy: hearts are the weakest cards, so they
// pay first; then clubs; spades are worth keeping for their shield; diamonds are the
// most important cards in the game and an Ace is the last thing spent. It also avoids
// spending hearts as damage, which is the same judgement from the other side.
func humanWeights() Weights {
	w := DefaultWeights()
	w.Draw = 8
	w.HandDeep = 0.2
	w.HeartPlay = 0.6
	w.HealDiamonds = 2.5
	w.Unlock = 15
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.5, 0.9, 1.2, 2.5, 3.0
	w.BlockedPay = true
	return w
}

func Human() Policy { return GreedyWith(humanWeights()) }

// HumanNoCombos is Human plus a preference against multi-card plays. The operator
// rarely plays combos; this measures whether that instinct costs or saves.
func HumanNoCombos() Policy {
	w := DefaultWeights()
	w.Draw = 8
	w.HandDeep = 0.2
	w.HeartPlay = 0.6
	w.HealDiamonds = 2.5
	w.Unlock = 15
	w.ComboPenalty = 4
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.5, 0.9, 1.2, 2.5, 3.0
	return GreedyWith(w)
}

// HumanPay is the payment half of Human alone: hearts first, diamonds and Aces last.
func HumanPay() Policy {
	w := DefaultWeights()
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.5, 0.9, 1.2, 2.5, 3.0
	return GreedyWith(w)
}

// HumanHeart is the "don't spend hearts as damage" half alone.
func HumanHeart() Policy {
	w := DefaultWeights()
	w.HeartPlay = 0.6
	return GreedyWith(w)
}

// HumanDraw is the "diamonds are the most important cards" half alone.
func HumanDraw() Policy {
	w := DefaultWeights()
	w.Draw = 8
	return GreedyWith(w)
}

// HumanTuned leans harder on the three levers the ablation found: diamonds, payment
// order, and not spending hearts as damage.
func humanTunedWeights() Weights {
	w := humanWeights()
	w.Draw = 11
	w.HandDeep = 0.25
	w.HeartPlay = 1.0
	w.Unlock = 18
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.35, 0.9, 1.3, 3.5, 4.5
	return w
}

func HumanTuned() Policy { return GreedyWith(humanTunedWeights()) }

// HumanJoker is HumanTuned that treats a diamond royal as the best Jester target:
// opening diamonds turns the rest of the hand into draws, which is the operator's
// stated use for a Joker.
func HumanJoker() Policy {
	w := humanTunedWeights()
	// Hold the Jester unless the royal is a diamond and the hand can actually draw:
	// a low base keeps it off clubs/spades/hearts, the per-point bonus waits for a
	// diamond hand instead of spending the Jester into an empty draw engine.
	w.Unlock = 10
	w.DiamondUnlock = 4.0
	return GreedyWith(w)
}

// HumanHedge is HumanTuned plus the castle hedge: it avoids ending a turn holding the
// suit that is most likely to be the next royal, and values a hand with power in more
// than one suit.
func HumanHedge() Policy {
	w := humanTunedWeights()
	w.BlockPenalty = 2
	w.Diversity = 1
	return GreedyWith(w)
}

// HumanHedge2 leans harder on the same idea, for the ablation.
func HumanHedge2() Policy {
	w := humanTunedWeights()
	w.BlockPenalty = 4
	w.Diversity = 2
	return GreedyWith(w)
}

// HumanTuned2 leans further into draw and payment protection, to find the edge of the
// human strategy.
func HumanTuned2() Policy {
	w := humanTunedWeights()
	w.Draw = 14
	w.HandDeep = 0.3
	w.HeartPlay = 1.2
	w.Unlock = 20
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.25, 0.85, 1.4, 5.0, 6.0
	return GreedyWith(w)
}

// HumanTuned3 is the conservative edge: less draw, milder payment order.
func HumanTuned3() Policy {
	w := humanTunedWeights()
	w.Draw = 9
	w.HeartPlay = 0.8
	w.PayHeart, w.PayClub, w.PaySpade, w.PayDiamond, w.PayAce = 0.45, 0.95, 1.2, 2.5, 3.5
	return GreedyWith(w)
}

// HumanHearts prices recovering diamonds from the discard more aggressively: hearts
// are the tool that puts the draw engine back under the tavern.
func HumanHearts() Policy {
	w := humanTunedWeights()
	w.HealDiamonds = 12
	return GreedyWith(w)
}

// HumanCapacity replaces the per-diamond-card draw proxy with the cards the diamond
// actually draws, capped by every seat's hand limit: a big diamond into full hands is
// worth less than a small one, and the big one can be saved.
func HumanCapacity() Policy {
	w := humanTunedWeights()
	// The sweep peaked here: 7.62 royals on the tuning seeds, 7.53 on fresh ones.
	w.Draw = 0
	w.DrawCards = 2.3
	w.DrawSelf = 3.2
	return GreedyWith(w)
}

func humanCapacityWeights() Weights {
	w := humanTunedWeights()
	w.Draw = 0
	w.DrawCards = 2.3
	w.DrawSelf = 3.2
	return w
}

// HumanPlan is the best line: capacity-aware draws, an exact-kill price (the royal
// goes on top of the tavern) and a safe-turn delay (a fully shielded royal is a free
// setup turn for draws and hearts).
func HumanPlan() Policy {
	w := humanCapacityWeights()
	w.ExactKill = 22
	w.DelayKill = 45
	return GreedyWith(w)
}

func humanPlanWeights() Weights {
	w := humanCapacityWeights()
	w.ExactKill = 22
	w.DelayKill = 45
	return w
}

// HumanBestOf keeps only the commandments that survive the bench: exact kills, safe
// setup, full enumeration, capacity draws, 5-combos, spades first, parity and the
// diamond-royal burst. It drops the diamond guard, the next-royal hedge and the
// heart-face penalty, which lost or were neutral in isolation.
func humanBestOfWeights() Weights {
	w := humanPlanWeights()
	w.BurstRoyal = 0.5
	w.ComboFive = 6
	w.SpadeEarly = 0.4
	w.Parity = 2
	return w
}

func HumanBestOf() Policy { return GreedyWith(humanBestOfWeights()) }

// BaseWeights exposes a named policy's weights so a tuning tool can start from the
// shipped line and change one knob. The named policies stay the source of truth; this
// is the same construction they use, not a copy.
func BaseWeights(name string) (Weights, bool) {
	switch name {
	case "greedy":
		return DefaultWeights(), true
	case "human":
		return humanWeights(), true
	case "human-tuned":
		return humanTunedWeights(), true
	case "human-capacity":
		return humanCapacityWeights(), true
	case "human-plan":
		return humanPlanWeights(), true
	case "human-bestof":
		return humanBestOfWeights(), true
	case "human-engine":
		return humanEngineWeights(), true
	}
	return Weights{}, false
}

// HumanEngine is the draw-engine line: a joint is a rhythm, not a card, so a diamond
// that draws into the bill is worth more than its damage, and the suit powers resolve
// at the play's total value. The weights below were tuned on the fixed CAPCHECK suite
// and validated on three unseen 2,000-seed prefixes and at 2/3/4 seats.
func humanEngineWeights() Weights {
	w := humanBestOfWeights()
	w.Shield = 0.49
	w.SpadeEarly = 0.62
	w.PaySpade = 1.0
	w.PayHeart = 0.53
	w.PayClub = 0.82
	w.PayOverpay = 1.56
	w.Overkill = 0.68
	w.DrawSelf = 3.73
	w.HandDeep = 0.28
	w.DrawCover = 6
	// Hearts are the only card-return engine once the tavern thins; a modest lift in
	// both the moved-card value and the diamond recovery paid on three unseen prefixes
	// and at 2 seats.
	w.Heal = 0.65
	w.HealDiamonds = 3.5
	w.HeartPlay = 0.8
	return w
}

func HumanEngine() Policy { return GreedyWith(humanEngineWeights()) }

// DefaultPolicy is what a bot seat plays in a real room. It is the bench's best line,
// not the old Greedy baseline: the fixed-seed suite is the reason to change it.
func DefaultPolicy() Policy { return HumanEngine() }

// Policies is the comparison set the bench knows by name. Greedy is the baseline;
// Random is the floor.
func Policies() map[string]Policy {
	return map[string]Policy{
		"greedy":         Greedy,
		"cautious":       Cautious(),
		"economist":      Economist(),
		"guardian":       Guardian(),
		"unlocker":       Unlocker(),
		"human":          Human(),
		"human-nocombos": HumanNoCombos(),
		"human-pay":      HumanPay(),
		"human-heart":    HumanHeart(),
		"human-draw":     HumanDraw(),
		"human-tuned":    HumanTuned(),
		"human-joker":    HumanJoker(),
		"human-hedge":    HumanHedge(),
		"human-hedge2":   HumanHedge2(),
		"human-hearts":   HumanHearts(),
		"human-capacity": HumanCapacity(),
		"human-plan":     HumanPlan(),
		"human-bestof":   HumanBestOf(),
		"human-engine":   HumanEngine(),
		"human-tuned2":   HumanTuned2(),
		"human-tuned3":   HumanTuned3(),
	}
}
