package engine

import (
	"errors"
	"fmt"
)

// Phase is the externally observable turn state. Turn start resolves inside a
// transition, so it is never a resting phase (docs/PLAN.md §4.2).
type Phase uint8

const (
	// Step1Await waits for the player to play a card, flip a Jester or yield.
	Step1Await Phase = iota
	// Step4Pre waits for a Jester flip before damage is assigned.
	Step4Pre
	// Step4Discard waits for cards to be discarded to pay the enemy attack.
	Step4Discard
	// Won and Lost are terminal.
	Won
	Lost
)

func (p Phase) String() string {
	switch p {
	case Step1Await:
		return "step1"
	case Step4Pre:
		return "step4_pre"
	case Step4Discard:
		return "step4_discard"
	case Won:
		return "won"
	case Lost:
		return "lost"
	}
	return "unknown"
}

// LostReason distinguishes the two ways to lose the game.
type LostReason uint8

const (
	LostNone LostReason = iota
	// LostCannotAct means the player started a turn with no card and no Jester.
	LostCannotAct
	// LostCannotDefend means the hand could not cover the enemy's attack.
	LostCannotDefend
)

func (l LostReason) String() string {
	switch l {
	case LostCannotAct:
		return "cannot_act"
	case LostCannotDefend:
		return "cannot_defend"
	}
	return "none"
}

// RuleOptions are the settings that change rules behaviour. They are frozen for a
// session: changing them requires a restart.
type RuleOptions struct {
	// SoloJester enables the solo Jester flip power (discard hand, refill).
	SoloJester bool `json:"soloJester"`
	// ExactDamage returns an exactly-killed enemy to the top of the tavern deck.
	ExactDamage bool `json:"exactDamage"`
	// AllowSoloYield permits yielding in single-player games.
	AllowSoloYield bool `json:"allowSoloYield"`
	// AllowOverpayDiscard lets the player keep discarding past the threshold in
	// step 4, ending payment with an explicit EndPayment action.
	AllowOverpayDiscard bool `json:"allowOverpayDiscard"`
	// Chaos shuffles all twelve royals into one castle deck instead of the
	// J->Q->K difficulty ramp. Draw order is still deterministic for a seed.
	Chaos bool `json:"chaos"`
	// PassOnDefeat hands the next turn to the following seat when a royal falls,
	// instead of the rulebook's "the player who struck the killing blow faces the
	// next royal". It is an easier co-op variant and is off by default.
	PassOnDefeat bool `json:"passOnDefeat"`
}

// DefaultRules is the rulebook behaviour.
func DefaultRules() RuleOptions {
	return RuleOptions{SoloJester: true, ExactDamage: true}
}

// Player is one seat. Solo deals one; co-op deals 2-4, and the rules never special-case
// the count.
type Player struct {
	Hand []Card `json:"hand"`
	// YieldedLastTurn records that this seat's last turn was a yield. Co-op yield
	// legality is per seat (R-044) and a running counter cannot express it: in
	// "A yields, B plays, C yields, A yields" B's last turn was a play, so B may
	// still act even though three yields happened in a row.
	YieldedLastTurn bool `json:"yieldedLastTurn,omitempty"`
}

// SeatChoice is a pending "who goes next" decision, created by a Jester play in a
// multi-seat game. While it is set the turn has not advanced and only the seat that
// played the Jester may act, and only to name the next seat.
type SeatChoice struct {
	By   int   `json:"by"`
	Next []int `json:"next"`
}

// Tier is the medal earned by a win, derived from Jesters used.
type Tier string

const (
	TierGold   Tier = "gold"
	TierSilver Tier = "silver"
	TierBronze Tier = "bronze"
	TierNone   Tier = ""
)

// Tier returns the medal for a win with the given Jester use count.
func TierFor(jestersUsed int) Tier {
	switch {
	case jestersUsed <= 0:
		return TierGold
	case jestersUsed == 1:
		return TierSilver
	default:
		return TierBronze
	}
}

// Event types emitted by transitions. The client animates from these and never
// re-derives rules.
const (
	EvSetup       = "setup"
	EvEnemyReveal = "enemy_reveal"
	EvPlay        = "play"
	EvHearts      = "hearts"
	EvDraw        = "draw"
	EvDamage      = "damage"
	EvShield      = "shield"
	EvDefeat      = "defeat"
	EvRecruit     = "recruit"
	EvSuffer      = "suffer"
	EvEnemyAttack = "enemy_attack"
	EvTurnEnd     = "turn_end"
	EvJesterFlip  = "jester_flip"
	EvYield       = "yield"
	// EvChooseNextSeat is the Jester's "who goes next" answer. format.js has had a
	// label for it all along; nothing emitted it until the client could ask.
	EvChooseNextSeat = "choose_next_seat"
	// EvHostChange records the crown moving between seats (D-60). Seat is the previous
	// host and Amount the new one, so a replay can show who was holding it.
	EvHostChange = "host_change"
	EvTier       = "tier"
	EvWin        = "win"
	EvLose       = "lose"
)

// Event is one observable thing that happened. Amount means different things per
// type (damage dealt, cards drawn, attack absorbed) and is documented in the UI.
type Event struct {
	// Turn is the turn the event happened in (0 during setup). Stamped by log().
	Turn   int      `json:"turn,omitempty"`
	Type   string   `json:"type"`
	Cards  []CardID `json:"cards,omitempty"`
	Amount int      `json:"amount,omitempty"`
	// Seat is the seat that acted. No omitempty: seat 0 is the host, and dropping it
	// from the wire made the client attribute every host event to whoever acted
	// before (the bubble and the played-card border credited the wrong player).
	Seat int    `json:"seat"`
	Note string `json:"note,omitempty"`
}

// Game is the complete authoritative state. It is a plain value: transitions
// return a copy, so callers can snapshot, replay and test without aliasing.
type Game struct {
	Version int         `json:"version"`
	Rules   RuleOptions `json:"rules"`
	Seed    string      `json:"seed"`
	// SeedRandom is true when the server chose the seed; it gates leaderboard
	// eligibility and keeps the seed hidden until the game ends.
	SeedRandom bool   `json:"seedRandom"`
	Theme      string `json:"theme"`
	// Jokers is the table's two Jester faces, in tray order: J1 then J2. It is
	// chosen at the deal and is public, like the theme. It is deliberately left
	// out of StateHash so adding it did not invalidate every stored replay.
	Jokers []string `json:"jokers,omitempty"`

	Players    []Player `json:"players"`
	ActiveSeat int      `json:"activeSeat"`
	// SeatChoice is non-nil while a Jester is waiting to name the next seat.
	SeatChoice *SeatChoice `json:"seatChoice,omitempty"`

	Castle []Card `json:"castle"`
	Enemy  Card   `json:"enemy"`

	Damage int `json:"damage"`
	Shield int `json:"shield"`
	// ImmunityCancelled is set by a multiplayer Jester play. Always false in solo.
	ImmunityCancelled bool `json:"immunityCancelled"`

	Tavern  []Card `json:"tavern"`
	Discard []Card `json:"discard"`
	// AttackArea holds cards played against the current enemy; they only reach
	// the discard pile when that enemy is defeated.
	AttackArea []Card `json:"attackArea"`

	MaxHand     int `json:"maxHand"`
	JestersLeft int `json:"jestersLeft"`
	JestersUsed int `json:"jestersUsed"`

	Step4Required int `json:"step4Required"`
	Step4Paid     int `json:"step4Paid"`

	Phase      Phase      `json:"phase"`
	LostReason LostReason `json:"lostReason"`
	// LostSeat is the seat whose turn it was when the table lost. Win and loss stay
	// table-wide; this only names the player to blame in the UI.
	LostSeat int     `json:"lostSeat,omitempty"`
	TurnNo   int     `json:"turnNo"`
	RGNDraws int     `json:"rngDraws"`
	Log      []Event `json:"log"`
}

// Version identifies the rules+shuffle contract. Bump it whenever a change would
// make an old session replay differently; persisted sessions with an older
// version are discarded (the server returns 404 and the client starts fresh).
//
// v2: the Jester asks which seat goes next, and co-op yield legality is tracked
// per seat instead of by a counter.
const Version = 2

// SetupOptions configures NewGame.
type SetupOptions struct {
	Seed       string
	SeedRandom bool
	Theme      string
	Rules      RuleOptions
	// Jokers is the pair of Jester faces the table shows, in tray order. It is
	// art, not rules: the engine carries it so every seat and the replay read one
	// answer. The deal picks it (server/dealJokers), never the rules engine.
	Jokers []string
	// NumPlayers is 1 for v0. Multiplayer rules (Jesters in the tavern, hand
	// sizes from the setup table) are already modelled.
	NumPlayers int
}

// JestersInTavern returns the number of Jesters shuffled into the tavern deck for
// a given player count, per the official setup table.
func JestersInTavern(numPlayers int) int {
	switch numPlayers {
	case 1:
		return 0
	case 2:
		return 0
	case 3:
		return 1
	case 4:
		return 2
	}
	return 0
}

// MaxHandSize returns the hand limit for a player count.
func MaxHandSize(numPlayers int) int {
	switch numPlayers {
	case 1:
		return 8
	case 2:
		return 7
	case 3:
		return 6
	case 4:
		return 5
	}
	return 8
}

// soloJesters is the number of Jesters set aside for solo play.
const soloJesters = 2

// ErrIllegal marks a rejected action. Callers turn it into HTTP 400.
var ErrIllegal = errors.New("illegal action")

func illegal(format string, args ...any) error {
	if len(args) == 0 {
		return errors.Join(ErrIllegal, errors.New(format))
	}
	return errors.Join(ErrIllegal, fmt.Errorf(format, args...))
}
