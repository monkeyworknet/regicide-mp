package bot

import (
	"regicide/engine"
)

// FromView builds an observation out of a seat's projection: the same ClientState the
// browser receives. It exists so the bot seat cannot accidentally be given more than the
// browser gets — the projection is the privacy boundary, and the bot goes through it.
//
// A projection for a spectator has no hand; FromView refuses that, because a bot seat is
// a player.
func FromView(st engine.ClientState) (Observation, bool) {
	if st.ViewerSeat < 0 {
		return Observation{}, false
	}
	obs := Observation{
		Seat:              st.ViewerSeat,
		Seats:             st.NumPlayers,
		Health:            st.EnemyHealth - st.Damage,
		Attack:            st.EnemyAttack,
		Damage:            st.Damage,
		Shield:            st.EffectiveShield,
		Phase:             phaseOf(st.Phase),
		Castle:            st.CastleCount,
		CastleSuits:       st.CastleSuits,
		NextSuits:         st.NextSuits,
		Tavern:            st.TavernCount,
		Discard:           st.DiscardCount,
		JestersLeft:       st.JestersLeft,
		MaxHand:           st.MaxHand,
		Required:          st.Incoming,
		Paid:              st.Step4Paid,
		LeastCard:         len(st.Hand) <= 1,
		ImmunityCancelled: st.ImmunityCancelled,
	}
	if st.Phase == engine.Step4Pre.String() || st.Phase == engine.Step4Discard.String() {
		obs.Required = st.Step4Required - st.Step4Paid
	}
	for _, c := range st.Discard {
		if suitOf(c.Suit) == engine.Diamonds {
			obs.DiscardDiamonds++
		}
	}
	for _, c := range st.Hand {
		obs.Hand = append(obs.Hand, engine.Card{ID: c.ID, Rank: engine.Rank(c.Rank), Suit: suitOf(c.Suit)})
	}
	if st.Enemy != nil {
		obs.Enemy = engine.Card{ID: st.Enemy.ID, Rank: engine.Rank(st.Enemy.Rank), Suit: suitOf(st.Enemy.Suit)}
	}
	obs.HandCounts = make([]int, len(st.Players))
	for i, p := range st.Players {
		obs.HandCounts[i] = p.HandCount
	}
	if len(st.Players) == 0 {
		// A solo snapshot carries no seat rail; the bench's Observe still reports the
		// one hand it has. Keep the two constructors identical for the policy.
		obs.HandCounts = []int{len(st.Hand)}
	}
	if st.PendingSeatChoice != nil {
		obs.Choices = append([]int(nil), st.PendingSeatChoice.Next...)
	}
	// What the projection says is on offer is what is on offer.
	for _, a := range st.Legal {
		switch a.Type {
		case "yield":
			obs.YieldLegal = true
		case "flip_jester":
			obs.FlipLegal = true
		}
	}
	return obs, true
}

func phaseOf(name string) engine.Phase {
	for _, p := range []engine.Phase{engine.Step1Await, engine.Step4Pre, engine.Step4Discard, engine.Won, engine.Lost} {
		if p.String() == name {
			return p
		}
	}
	return engine.Step1Await
}

func suitOf(name string) engine.Suit {
	switch name {
	case "clubs":
		return engine.Clubs
	case "diamonds":
		return engine.Diamonds
	case "hearts":
		return engine.Hearts
	case "spades":
		return engine.Spades
	}
	return engine.NoSuit
}
