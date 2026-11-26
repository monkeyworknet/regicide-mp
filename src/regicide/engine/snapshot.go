package engine

// CardView is the client-safe projection of a card.
type CardView struct {
	ID    CardID `json:"id"`
	Rank  int    `json:"rank"`
	Suit  string `json:"suit"`
	Value int    `json:"value"`
	// DiscardValue is what the card is worth when paying damage in step 4; it
	// differs from Value for nothing today, but the UI must not assume that.
	DiscardValue int    `json:"discardValue"`
	Label        string `json:"label"`
	// Attack is the royal's base attack (0 for every non-royal card). The board
	// reads it only for the revealed enemy — "what will land / what it started
	// with" — but a recruited royal carries its value too, which is harmless: the
	// UI never reads Attack from a hand card.
	Attack int `json:"attack,omitempty"`
}

// ActionView is a legal action rendered for the client's buttons and previews.
type ActionView struct {
	Type     string   `json:"type"`
	CardIDs  []CardID `json:"cardIds,omitempty"`
	Seat     *int     `json:"seat,omitempty"`
	Attack   int      `json:"attack,omitempty"`
	Legal    bool     `json:"legal"`
	Disabled string   `json:"disabled,omitempty"`
}

// View scopes a snapshot to one seat. Seat is the viewer; OpenHands asks for every
// hand to be serialised (a table sitting in one room), which is off by default.
type View struct {
	Seat      int
	OpenHands bool
}

// SeatView is the public view of one player: how many cards it holds, what it last
// played, and whether it is the one to act.
type SeatView struct {
	Seat      int    `json:"seat"`
	Name      string `json:"name,omitempty"`
	HandCount int    `json:"handCount"`
	// HandLimit is the table's limit (7/6/5 by player count) and HandFull says this
	// seat is at it. A full hand is skipped by the diamond draw, so the rail shows
	// "6/6" and marks it rather than leaving the player to work it out.
	HandLimit int        `json:"handLimit"`
	HandFull  bool       `json:"handFull,omitempty"`
	Hand      []CardView `json:"hand,omitempty"`
	LastPlay  []CardID   `json:"lastPlay,omitempty"`
	Yielded   bool       `json:"yielded,omitempty"`
	Active    bool       `json:"active"`
	Away      bool       `json:"away,omitempty"`
}

// ClientState is everything the browser may know. It never contains undrawn
// tavern or castle card identities, and it hides the seed while a random run is
// in progress (docs/PLAN.md §4.2 contract 4).
type ClientState struct {
	Version    int    `json:"version"`
	Phase      string `json:"phase"`
	LostReason string `json:"lostReason,omitempty"`
	TurnNo     int    `json:"turnNo"`
	Theme      string `json:"theme"`
	// Jokers is the table's two Jester faces, in tray order. Every seat renders the
	// same pair, so a J1 that is spent is the same card at every place setting.
	Jokers     []string    `json:"jokers"`
	Rules      RuleOptions `json:"rules"`
	MaxHand    int         `json:"maxHand"`
	ActiveSeat int         `json:"activeSeat"`
	NumPlayers int         `json:"numPlayers"`

	Hand        []CardView `json:"hand"`
	Enemy       *CardView  `json:"enemy"`
	EnemyAttack int        `json:"enemyAttack"`
	EnemyHealth int        `json:"enemyHealth"`

	Damage int `json:"damage"`
	// Shield is every spade played against this enemy; EffectiveShield is what
	// actually applies (0 while a spades enemy is still immune).
	Shield            int  `json:"shield"`
	EffectiveShield   int  `json:"effectiveShield"`
	ImmunityCancelled bool `json:"immunityCancelled"`
	// Incoming is what the enemy would deal right now (after shield, floored at 0).
	Incoming int `json:"incoming"`

	TavernCount int `json:"tavernCount"`
	CastleCount int `json:"castleCount"`
	// CastleSuits is how many royals of each suit are still in the castle, in the
	// engine's suit order (clubs, diamonds, hearts, spades). The order of the castle is
	// hidden; its composition is public — 3 of each suit, less the ones revealed — and a
	// bot may use it to hedge against the next royal blocking a suit. It is not a peek
	// at the next card.
	CastleSuits [4]int `json:"castleSuits"`
	// NextSuits is the suit composition of the rank that will be revealed next. The
	// castle is a pinned ramp (all Jacks, then Queens, then Kings), so the next royal's
	// suit odds come from this block alone. It reveals no order: only how many of each
	// suit remain in the rank on top.
	NextSuits    [4]int     `json:"nextSuits"`
	DiscardCount int        `json:"discardCount"`
	DiscardTop   []CardView `json:"discardTop"`
	// Discard is the whole pile, oldest first. It is public information in Regicide,
	// and the client's pile viewer needs it: rebuilding the list from the log loses the
	// cards hearts returned to the tavern and the cards the attack area shed on a defeat
	// (playtest 3 campaign, A12: a badge of 5 beside a six-card list).
	Discard    []CardView `json:"discard"`
	AttackArea []CardView `json:"attackArea"`

	JestersLeft int `json:"jestersLeft"`
	JestersUsed int `json:"jestersUsed"`

	Step4Required int `json:"step4Required"`
	Step4Paid     int `json:"step4Paid"`

	// ViewerSeat is the seat this snapshot was projected for; NumPlayers above is
	// the table size.
	ViewerSeat int `json:"viewerSeat"`
	// Players is the public seat rail: counts, last play, whose turn it is. It never
	// carries another seat's hand unless the room opened the hands.
	Players []SeatView `json:"players"`
	// PendingSeatChoice is set while a Jester waits for its player to name the next
	// seat. Only that seat has legal actions.
	PendingSeatChoice *SeatChoiceView `json:"pendingSeatChoice,omitempty"`
	LostSeat          int             `json:"lostSeat,omitempty"`
	OpenHands         bool            `json:"openHands,omitempty"`

	Legal []ActionView `json:"legal"`
	// Recent events, newest last, for animation.
	Events []Event `json:"events"`
	// Log is the full turn-stamped history for the move-history panel. Every
	// event it contains names only cards the player can already see (played,
	// paid, defeated or revealed), so it is safe to hand to the browser.
	Log []Event `json:"log"`

	Seed        string `json:"seed,omitempty"`
	SeedVisible bool   `json:"seedVisible"`
	SeedRandom  bool   `json:"seedRandom"`
	Tier        string `json:"tier,omitempty"`
	Won         bool   `json:"won"`
	Lost        bool   `json:"lost"`
}

// SeatChoiceView is a pending Jester choice, projected for the client.
type SeatChoiceView struct {
	By   int   `json:"by"`
	Next []int `json:"next"`
}

// view renders a card for the client.
func view(c Card) CardView {
	suit := c.Suit.String()
	if c.Suit == NoSuit {
		suit = "none"
	}
	attack := 0
	if c.IsRoyal() {
		attack, _ = EnemyStats(c.Rank)
	}
	return CardView{
		ID:           c.ID,
		Rank:         int(c.Rank),
		Suit:         suit,
		Value:        c.PlayValue(),
		DiscardValue: c.DiscardValue(),
		Label:        rankLabel(c.Rank) + suitSymbol(c.Suit),
		Attack:       attack,
	}
}

func suitSymbol(s Suit) string {
	switch s {
	case Clubs:
		return "♣"
	case Diamonds:
		return "♦"
	case Hearts:
		return "♥"
	case Spades:
		return "♠"
	}
	return ""
}

// Snapshot projects the game for the seat that is to act, which is exactly what the
// solo client needs. Co-op rooms call SnapshotFor with the viewer's seat.
func Snapshot(g Game) ClientState {
	return SnapshotFor(g, View{Seat: g.ActiveSeat})
}

// SnapshotFor projects the game for one viewer. The projection is deliberately
// lossy: counts replace piles, other seats' hands are counts (never cards), and
// secrets stay server-side. Legal actions appear only for the seat that may act,
// because a foreign legal list would offer buttons that the room layer rejects.
func SnapshotFor(g Game, v View) ClientState {
	// Slices are always initialised: the JSON contract uses [] rather than null so
	// the client never has to null-guard a list.
	out := ClientState{
		Hand:       []CardView{},
		AttackArea: []CardView{},
		DiscardTop: []CardView{},
		Discard:    []CardView{},
		Players:    []SeatView{},
		Legal:      []ActionView{},
		Events:     []Event{},
		Log:        []Event{},
		Version:    g.Version,
		Phase:      g.Phase.String(),
		TurnNo:     g.TurnNo,
		Theme:      g.Theme,
		Jokers:     append([]string{}, g.Jokers...),
		Rules:      g.Rules,
		MaxHand:    g.MaxHand,
		ActiveSeat: g.ActiveSeat,
		NumPlayers: len(g.Players),
		Damage:     g.Damage,
		Shield:     g.Shield,
		// EffectiveShield was declared and never filled, so every consumer (the bot
		// seat, the client's shield note) read zero while a royal was shielded.
		EffectiveShield: g.effectiveShield(),
		// ImmunityCancelled was the same kind of hole: a public fact the client and the
		// bot read, never filled by the snapshot.
		ImmunityCancelled: g.ImmunityCancelled,
		Incoming:          g.incomingDamage(),
		TavernCount:       len(g.Tavern),
		CastleCount:       len(g.Castle),
		DiscardCount:      len(g.Discard),
		JestersLeft:       g.JestersLeft,
		JestersUsed:       g.JestersUsed,
		Step4Required:     g.Step4Required,
		Step4Paid:         g.Step4Paid,
		SeedRandom:        g.SeedRandom,
		Won:               g.Phase == Won,
		Lost:              g.Phase == Lost,
	}
	if g.Phase == Lost {
		out.LostReason = g.LostReason.String()
	}
	if g.Phase == Won {
		out.Tier = string(TierFor(g.JestersUsed))
	}
	if !g.Enemy.ID.Empty() {
		v := view(g.Enemy)
		out.Enemy = &v
		out.EnemyAttack = g.EnemyAttack()
		out.EnemyHealth = g.EnemyHealth()
	}
	out.ViewerSeat = v.Seat
	out.OpenHands = v.OpenHands
	if v.Seat >= 0 && v.Seat < len(g.Players) {
		for _, c := range g.Players[v.Seat].Hand {
			out.Hand = append(out.Hand, view(c))
		}
	}
	if g.SeatChoice != nil {
		out.PendingSeatChoice = &SeatChoiceView{By: g.SeatChoice.By, Next: append([]int(nil), g.SeatChoice.Next...)}
	}
	if g.Phase == Lost {
		out.LostSeat = g.LostSeat
	}
	if len(g.Players) > 1 {
		lastPlay := lastPlays(g)
		for i, p := range g.Players {
			sv := SeatView{
				Seat:      i,
				HandCount: len(p.Hand),
				HandLimit: g.MaxHand,
				HandFull:  len(p.Hand) >= g.MaxHand,
				Yielded:   p.YieldedLastTurn,
				Active:    i == g.ActiveSeat,
				LastPlay:  lastPlay[i],
			}
			if v.OpenHands || i == v.Seat {
				for _, c := range p.Hand {
					sv.Hand = append(sv.Hand, view(c))
				}
			}
			out.Players = append(out.Players, sv)
		}
	}
	for _, c := range g.AttackArea {
		out.AttackArea = append(out.AttackArea, view(c))
	}
	for _, c := range g.Castle {
		if int(c.Suit) < len(out.CastleSuits) {
			out.CastleSuits[c.Suit]++
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
			if c.Rank == lowest && int(c.Suit) < len(out.NextSuits) {
				out.NextSuits[c.Suit]++
			}
		}
	}
	for _, c := range g.Discard {
		out.Discard = append(out.Discard, view(c))
	}
	// The pile's visual fan only needs the newest few; the full list is for the viewer.
	top := g.Discard
	if len(top) > 3 {
		top = top[len(top)-3:]
	}
	for _, c := range top {
		out.DiscardTop = append(out.DiscardTop, view(c))
	}
	actor := v.Seat == g.ActiveSeat
	if g.SeatChoice != nil {
		actor = v.Seat == g.SeatChoice.By
	}
	if actor {
		for _, a := range LegalActions(&g) {
			out.Legal = append(out.Legal, actionView(&g, a))
		}
	}
	// One copy for both windows: Events is the suffix of Log the reveal reads, so a
	// second slice and a second copy per projection would buy nothing. Neither field is
	// appended to after the snapshot is built.
	out.Log = append(out.Log, g.Log...)
	if len(out.Log) > 40 {
		out.Events = out.Log[len(out.Log)-40:]
	} else {
		out.Events = out.Log
	}
	// The seed is a secret until a random run finishes.
	if !g.SeedRandom || g.gameOver() {
		out.Seed = FormatSeed(g.Seed)
		out.SeedVisible = true
	}
	return out
}

// lastPlays reports, per seat, the most recent play that seat made against the
// current enemy. It reads the history backwards and stops at the reveal (or the
// defeat) that started this enemy, so a new enemy clears the table.
func lastPlays(g Game) [][]CardID {
	out := make([][]CardID, len(g.Players))
	for i := len(g.Log) - 1; i >= 0; i-- {
		ev := g.Log[i]
		switch ev.Type {
		case EvEnemyReveal, EvDefeat:
			return out
		case EvPlay:
			if ev.Seat >= 0 && ev.Seat < len(out) && out[ev.Seat] == nil && len(ev.Cards) > 0 {
				out[ev.Seat] = append([]CardID(nil), ev.Cards...)
			}
		}
		filled := true
		for _, ids := range out {
			if ids == nil {
				filled = false
				break
			}
		}
		if filled {
			return out
		}
	}
	return out
}

func actionView(g *Game, a Action) ActionView {
	switch act := a.(type) {
	case Play:
		// Attack is the damage this play would deal, suit powers included. The
		// browser renders it instead of re-implementing the clubs rule.
		return ActionView{Type: act.actionName(), CardIDs: act.CardIDs, Attack: g.previewAttack(act), Legal: true}
	case PayDamage:
		return ActionView{Type: act.actionName(), CardIDs: act.IDs(), Legal: true}
	case FlipJester:
		return ActionView{Type: act.actionName(), Legal: true}
	case Yield:
		return ActionView{Type: act.actionName(), Legal: true}
	case EndPayment:
		return ActionView{Type: act.actionName(), Legal: true}
	case ChooseNextSeat:
		seat := act.Seat
		return ActionView{Type: act.actionName(), Seat: &seat, Legal: true}
	}
	return ActionView{}
}

// Empty reports whether the id is unset. It exists so a zero Card can stand in
// for "no enemy" without a nil check at every call site.
func (id CardID) Empty() bool { return id == "" }
