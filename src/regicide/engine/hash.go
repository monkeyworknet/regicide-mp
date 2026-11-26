package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// StateHash is a short, canonical fingerprint of the authoritative state.
//
// A replay file stores one of these after every action. Playback rebuilds the game
// from the seed, applies the same actions, and compares: if every hash matches, the
// recorded game and the recorded outcome are exactly the game the server played. The
// history log is deliberately excluded — it contains no information the other fields
// do not, and it would dominate the cost.
func StateHash(g Game) string {
	payload := struct {
		Version           int         `json:"version"`
		Rules             RuleOptions `json:"rules"`
		Seed              string      `json:"seed"`
		SeedRandom        bool        `json:"seedRandom"`
		Theme             string      `json:"theme"`
		Players           []Player    `json:"players"`
		ActiveSeat        int         `json:"activeSeat"`
		SeatChoice        *SeatChoice `json:"seatChoice,omitempty"`
		Castle            []Card      `json:"castle"`
		Enemy             Card        `json:"enemy"`
		Damage            int         `json:"damage"`
		Shield            int         `json:"shield"`
		ImmunityCancelled bool        `json:"immunityCancelled"`
		Tavern            []Card      `json:"tavern"`
		Discard           []Card      `json:"discard"`
		AttackArea        []Card      `json:"attackArea"`
		MaxHand           int         `json:"maxHand"`
		JestersLeft       int         `json:"jestersLeft"`
		JestersUsed       int         `json:"jestersUsed"`
		Step4Required     int         `json:"step4Required"`
		Step4Paid         int         `json:"step4Paid"`
		Phase             Phase       `json:"phase"`
		LostReason        LostReason  `json:"lostReason"`
		LostSeat          int         `json:"lostSeat"`
		TurnNo            int         `json:"turnNo"`
		RGNDraws          int         `json:"rngDraws"`
	}{
		Version: g.Version, Rules: g.Rules, Seed: g.Seed, SeedRandom: g.SeedRandom,
		Theme: g.Theme, Players: g.Players, ActiveSeat: g.ActiveSeat,
		SeatChoice: g.SeatChoice, Castle: g.Castle, Enemy: g.Enemy,
		Damage: g.Damage, Shield: g.Shield, ImmunityCancelled: g.ImmunityCancelled,
		Tavern: g.Tavern, Discard: g.Discard, AttackArea: g.AttackArea,
		MaxHand: g.MaxHand, JestersLeft: g.JestersLeft, JestersUsed: g.JestersUsed,
		Step4Required: g.Step4Required, Step4Paid: g.Step4Paid,
		Phase: g.Phase, LostReason: g.LostReason, LostSeat: g.LostSeat,
		TurnNo: g.TurnNo, RGNDraws: g.RGNDraws,
	}
	// json.Marshal is deterministic for structs and slices, which is all this is.
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// HandSizes is the public card count per seat, recorded with every step so an
// analysis tool can plot the table's economy without rebuilding the game.
func HandSizes(g Game) []int {
	out := make([]int, len(g.Players))
	for i, p := range g.Players {
		out[i] = len(p.Hand)
	}
	return out
}
