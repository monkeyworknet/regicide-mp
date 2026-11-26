package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- seeds and RNG -------------------------------------------------------

func TestSeedNormalisation(t *testing.T) {
	cases := map[string]string{
		"abcd-efgh-jkmn":   "ABCDEFGHJKMN",
		" ABCD EFGH JKMN ": "ABCDEFGHJKMN",
		"ABCD_OEFG":        "ABCD0EFG", // O -> 0
		"ABILCD":           "AB11CD",   // I and L -> 1
	}
	for in, want := range cases {
		if got := NormalizeSeed(in); got != want {
			t.Errorf("NormalizeSeed(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSeed(t *testing.T) {
	if got, want := FormatSeed("ABCDEFGHJKMN"), "ABCD-EFGH-JKMN"; got != want {
		t.Errorf("FormatSeed = %q, want %q", got, want)
	}
	if got := FormatSeed("short"); got != "SH0RT" { // O normalises to 0
		t.Errorf("FormatSeed(short) = %q", got)
	}
}

func TestRandomSeedShape(t *testing.T) {
	a, b := RandomSeed(), RandomSeed()
	if len(a) != 12 || len(b) != 12 {
		t.Fatalf("seed lengths = %d, %d", len(a), len(b))
	}
	if a == b {
		t.Errorf("two random seeds collided: %s", a)
	}
	for _, r := range a {
		if !strings.ContainsRune(seedAlphabet, r) {
			t.Fatalf("seed %q uses a character outside the alphabet", a)
		}
	}
}

func TestScriptedRandRepeats(t *testing.T) {
	r := NewScriptedRand(1, 2)
	got := []uint64{r.Uint64(), r.Uint64(), r.Uint64(), r.Uint64()}
	want := []uint64{1, 2, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scripted rand = %v, want %v", got, want)
		}
	}
	if NewScriptedRand().Uint64() != 0 {
		t.Error("empty script should yield zeros")
	}
}

func TestSeededRandIsStable(t *testing.T) {
	a, b := NewSeededRand("STABLE"), NewSeededRand("STABLE")
	for i := 0; i < 5; i++ {
		if a.Uint64() != b.Uint64() {
			t.Fatal("same seed produced different streams")
		}
	}
}

// ---- snapshot / information hiding --------------------------------------

func TestSnapshotHidesDrawPiles(t *testing.T) {
	g := baseGame(t)
	g.SeedRandom = true
	hidden := map[CardID]bool{}
	for _, c := range g.Tavern {
		hidden[c.ID] = true
	}
	for _, c := range g.Castle {
		hidden[c.ID] = true
	}

	raw, err := json.Marshal(Snapshot(g))
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	payload := string(raw)
	for id := range hidden {
		if strings.Contains(payload, `"`+string(id)+`"`) {
			t.Fatalf("snapshot leaks hidden card %s", id)
		}
	}
	if strings.Contains(payload, g.Seed) {
		t.Fatal("snapshot leaks the seed of a live random game")
	}
}

func TestSnapshotRevealsSeedAtGameEnd(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.SeedRandom = true
		g.Seed = "ABCD-EFGH-JKMN"
		g.Phase = Won
	})
	snap := Snapshot(g)
	if !snap.SeedVisible || snap.Seed != "ABCD-EFGH-JKMN" {
		t.Fatalf("seed = %q visible = %v, want it revealed after the game", snap.Seed, snap.SeedVisible)
	}
	if snap.Tier != string(TierGold) {
		t.Errorf("tier = %q, want gold", snap.Tier)
	}
}

func TestSnapshotOfHandAndLegalActions(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "2C", "2D")
	})
	snap := Snapshot(g)
	if len(snap.Hand) != 2 {
		t.Fatalf("hand = %d, want 2", len(snap.Hand))
	}
	if snap.Enemy == nil || snap.Enemy.ID != "JS" {
		t.Fatalf("enemy = %v, want JS", snap.Enemy)
	}
	if snap.EnemyHealth != 20 || snap.EnemyAttack != 10 {
		t.Errorf("enemy stats = %d/%d, want attack 10 health 20", snap.EnemyAttack, snap.EnemyHealth)
	}
	if len(snap.Legal) == 0 {
		t.Fatal("legal actions should be present")
	}
	var sawPair bool
	for _, a := range snap.Legal {
		if a.Type == "play" && len(a.CardIDs) == 2 {
			sawPair = true
		}
	}
	if !sawPair {
		t.Fatal("the pair of 2s should be offered")
	}
	if snap.Hand[0].Label != "2♣" {
		t.Errorf("label = %q, want 2♣", snap.Hand[0].Label)
	}
}

func TestSnapshotDiscardTopIsCapped(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Discard = cards(t, "2C", "3C", "4C", "5C", "6C")
	})
	snap := Snapshot(g)
	if len(snap.DiscardTop) != 3 {
		t.Fatalf("discard top = %d, want 3", len(snap.DiscardTop))
	}
	if snap.DiscardCount != 5 {
		t.Errorf("discard count = %d, want 5", snap.DiscardCount)
	}
	if snap.DiscardTop[2].ID != "6C" {
		t.Errorf("newest discard = %s, want 6C", snap.DiscardTop[2].ID)
	}
	// The viewer needs the whole pile, not a rebound log: the count and the list must
	// agree, and the newest card must be last.
	if len(snap.Discard) != snap.DiscardCount {
		t.Fatalf("discard list = %d, count = %d", len(snap.Discard), snap.DiscardCount)
	}
	if snap.Discard[0].ID != "2C" || snap.Discard[len(snap.Discard)-1].ID != "6C" {
		t.Errorf("discard order = %s..%s, want 2C..6C", snap.Discard[0].ID, snap.Discard[len(snap.Discard)-1].ID)
	}
}

// ---- step 4 options -----------------------------------------------------

func TestOverpayRequiresEndPayment(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Rules.AllowOverpayDiscard = true
		setEnemy(g, t, "JC") // attack 10
		g.JestersLeft = 0
		g.Players[0].Hand = cards(t, "5C", "5D", "2C", "3C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("2C")})
	if !g.inStep4() {
		t.Fatalf("phase = %s, want step 4", g.Phase)
	}
	g, _ = apply(t, g, PayDamage{CardID: "5C"})
	if g.Phase != Step4Discard {
		t.Fatalf("phase = %s, want payment to stay open with over-pay enabled", g.Phase)
	}
	g, _ = apply(t, g, PayDamage{CardID: "5D"}) // 10 total, now over-paying is allowed
	if g.Phase != Step4Discard {
		t.Fatalf("phase = %s, want the turn to wait for EndPayment", g.Phase)
	}
	g, _ = apply(t, g, EndPayment{})
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want the turn to end after EndPayment", g.Phase)
	}
}

func TestEndPaymentRejectedByDefaultAndWhenShort(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JC")
		g.JestersLeft = 0
		g.Players[0].Hand = cards(t, "5C", "5D")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("5C")})
	if err := applyErr(t, g, EndPayment{}); err == nil {
		t.Fatal("EndPayment must be rejected when over-paying is disabled")
	}

	g.Rules.AllowOverpayDiscard = true
	if err := applyErr(t, g, EndPayment{}); err == nil {
		t.Fatal("EndPayment must be rejected before the attack is covered")
	}
}

func TestYieldWhenUnaffordableLoses(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Rules.AllowSoloYield = true
		setEnemy(g, t, "KC") // attack 20
		g.JestersLeft = 0
		g.Players[0].Hand = cards(t, "2C")
	})
	g, evs := apply(t, g, Yield{})
	if g.Phase != Lost || g.LostReason != LostCannotDefend {
		t.Fatalf("phase = %s reason = %s, want lost/cannot_defend", g.Phase, g.LostReason)
	}
	if !containsEvent(evs, EvLose) {
		t.Fatal("a lose event should be emitted")
	}
}

func TestYieldIllegalAtZeroDamage(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Rules.AllowSoloYield = true
		setEnemy(g, t, "JC")
		g.Shield = 20
		g.Players[0].Hand = cards(t, "2C")
	})
	if err := applyErr(t, g, Yield{}); err == nil {
		t.Fatal("yielding when the enemy deals nothing must be illegal")
	}
}

// ---- multiplayer-shaped paths (already modelled, unused in v0) ----------

func TestJesterPlayedAloneMultiplayerPath(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JD")
		g.Players[0].Hand = cards(t, "J1", "2C")
	})
	g, _ = apply(t, g, Play{CardIDs: ids("J1")})
	if !g.ImmunityCancelled {
		t.Fatal("a played Jester must cancel the enemy's immunity")
	}
	if g.Phase != Step1Await {
		t.Fatalf("phase = %s, want a new turn (steps 3 and 4 skipped)", g.Phase)
	}
	for _, c := range g.Discard {
		if c.ID == "J1" {
			return
		}
	}
	t.Fatal("the played Jester should be in the discard pile")
}

func TestChooseNextSeatRejectedInSolo(t *testing.T) {
	g := baseGame(t)
	if err := applyErr(t, g, ChooseNextSeat{Seat: 0}); err == nil {
		t.Fatal("choosing the next seat must be rejected in this mode")
	}
}

func TestMultiplayerTableSetup(t *testing.T) {
	for _, tc := range []struct{ players, hand, jesters int }{{3, 6, 1}, {4, 5, 2}, {2, 7, 0}} {
		g := NewGame(SetupOptions{Seed: "MP", Rules: DefaultRules(), NumPlayers: tc.players}, NewScriptedRand())
		if g.MaxHand != tc.hand {
			t.Errorf("%d players: maxHand = %d, want %d", tc.players, g.MaxHand, tc.hand)
		}
		for i, p := range g.Players {
			if len(p.Hand) != tc.hand {
				t.Errorf("%d players: seat %d hand = %d, want %d", tc.players, i, len(p.Hand), tc.hand)
			}
		}
		// Jesters live in the tavern for 3-4 players and are counted there.
		found := 0
		for _, c := range g.Tavern {
			if c.IsJoker() {
				found++
			}
		}
		handJokers := 0
		for _, p := range g.Players {
			for _, c := range p.Hand {
				if c.IsJoker() {
					handJokers++
				}
			}
		}
		if found+handJokers != tc.jesters {
			t.Errorf("%d players: jesters in tavern = %d (hand %d), want %d", tc.players, found, handJokers, tc.jesters)
		}
		if g.JestersLeft != 0 {
			t.Errorf("%d players: side jesters = %d, want 0", tc.players, g.JestersLeft)
		}
	}
}

func TestMultiplayerDiamondsRoundRobin(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "RR", Rules: DefaultRules(), NumPlayers: 3}, NewScriptedRand())
	g.Phase = Step1Await
	g.Players[0].Hand = cards(t, "5D")
	g.Players[1].Hand = cards(t, "2C")
	g.Players[2].Hand = cards(t, "3C")
	g.Tavern = cards(t, "2D", "3D", "4D", "6D", "7D", "8D")
	g.Discard = nil
	g.AttackArea = nil
	setEnemy(&g, t, "KC")

	g, _ = apply(t, g, Play{CardIDs: ids("5D")})
	// Five draws starting at the active seat: active, s1, s2, active, s1.
	if got := len(g.Players[0].Hand); got != 2 {
		t.Errorf("active seat hand = %d, want 2", got)
	}
	if got := len(g.Players[1].Hand); got != 3 {
		t.Errorf("seat 1 hand = %d, want 3", got)
	}
	if got := len(g.Players[2].Hand); got != 2 {
		t.Errorf("seat 2 hand = %d, want 2", got)
	}
	if got := len(g.Tavern); got != 1 {
		t.Errorf("tavern = %d, want 1 after five draws", got)
	}
}

func TestTransitionRejectsAfterGameOver(t *testing.T) {
	g := scenario(t, func(g *Game) {
		g.Phase = Lost
		g.Players[0].Hand = cards(t, "2C")
	})
	if err := applyErr(t, g, Play{CardIDs: ids("2C")}); err == nil {
		t.Fatal("actions after the game is over must be rejected")
	}
}

func TestTransitionDoesNotMutateInput(t *testing.T) {
	g := scenario(t, func(g *Game) {
		setEnemy(g, t, "JS")
		g.Players[0].Hand = cards(t, "2C", "3C")
	})
	before := len(g.Players[0].Hand)
	_, _, err := Transition(g, Play{CardIDs: ids("2C")}, NewScriptedRand())
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	if len(g.Players[0].Hand) != before {
		t.Fatal("Transition mutated the caller's state")
	}
}

// ---- card model ---------------------------------------------------------

func TestCardModel(t *testing.T) {
	cases := []struct {
		id    string
		value int
		royal bool
		comp  bool
		joker bool
	}{
		{"AS", 1, false, true, false},
		{"10D", 10, false, false, false},
		{"JS", 10, true, false, false},
		{"QH", 15, true, false, false},
		{"KC", 20, true, false, false},
		{"J1", 0, false, false, true},
	}
	for _, tc := range cases {
		c := parseCard(t, tc.id)
		if c.PlayValue() != tc.value || c.DiscardValue() != tc.value {
			t.Errorf("%s value = %d/%d, want %d", tc.id, c.PlayValue(), c.DiscardValue(), tc.value)
		}
		if c.IsRoyal() != tc.royal || c.IsCompanion() != tc.comp || c.IsJoker() != tc.joker {
			t.Errorf("%s classification wrong: royal=%v comp=%v joker=%v", tc.id, c.IsRoyal(), c.IsCompanion(), c.IsJoker())
		}
		if c.String() != tc.id {
			t.Errorf("String() = %q, want %q", c.String(), tc.id)
		}
	}

	for _, tc := range []struct {
		rank       Rank
		attack, hp int
	}{{Jack, 10, 20}, {Queen, 15, 30}, {King, 20, 40}, {Nine, 0, 0}} {
		a, h := EnemyStats(tc.rank)
		if a != tc.attack || h != tc.hp {
			t.Errorf("EnemyStats(%v) = %d/%d, want %d/%d", tc.rank, a, h, tc.attack, tc.hp)
		}
	}

	if NewCard(King, Spades).ID != "KS" {
		t.Errorf("NewCard(King, Spades) = %s", NewCard(King, Spades).ID)
	}
	if !(CardID("")).Empty() || CardID("KS").Empty() {
		t.Error("Empty() is wrong")
	}
}

func TestActionNames(t *testing.T) {
	cases := map[string]Action{
		"play":             Play{},
		"flip_jester":      FlipJester{},
		"yield":            Yield{},
		"pay_damage":       PayDamage{},
		"end_payment":      EndPayment{},
		"choose_next_seat": ChooseNextSeat{},
	}
	for want, a := range cases {
		if got := ActionName(a); got != want {
			t.Errorf("ActionName(%T) = %q, want %q", a, got, want)
		}
	}
}

func TestPhaseAndLostReasonStrings(t *testing.T) {
	if Step1Await.String() != "step1" || Step4Pre.String() != "step4_pre" ||
		Step4Discard.String() != "step4_discard" || Won.String() != "won" || Lost.String() != "lost" {
		t.Error("phase strings changed")
	}
	if LostCannotAct.String() != "cannot_act" || LostCannotDefend.String() != "cannot_defend" || LostNone.String() != "none" {
		t.Error("lost reason strings changed")
	}
	if Phase(99).String() != "unknown" {
		t.Error("unknown phase should stringify as unknown")
	}
	if LostReason(99).String() != "none" {
		t.Error("unknown lost reason should stringify as none")
	}
	if NoSuit.String() != "none" || Spades.String() != "spades" {
		t.Error("suit strings changed")
	}
}

func TestNewGameDefaults(t *testing.T) {
	g := NewGame(SetupOptions{Seed: "DEF"}, NewScriptedRand())
	if g.Theme != "official" {
		t.Errorf("theme = %q, want modern", g.Theme)
	}
	if len(g.Players) != 1 {
		t.Errorf("players = %d, want 1", len(g.Players))
	}
	if g.Version != Version {
		t.Errorf("version = %d, want %d", g.Version, Version)
	}
	if len(g.Log) == 0 {
		t.Error("setup should log events")
	}
}

// TestSnapshotCastleSuitsArePublicComposition: the castle's order is hidden, but the
// rank block on top and its suit counts are public. The bot uses them to price the
// next royal's block; they must never expose which card is next.
func TestSnapshotCastleSuitsArePublicComposition(t *testing.T) {
	g := NewGame(SetupOptions{
		Seed: "SUITS-1", Theme: "simple", Rules: DefaultRules(), NumPlayers: 1,
	}, NewSeededRand("SUITS-1"))
	snap := Snapshot(g)
	total := 0
	for _, n := range snap.CastleSuits {
		total += n
	}
	if total != snap.CastleCount {
		t.Errorf("castle suits sum = %d, count = %d", total, snap.CastleCount)
	}
	// The castle is a pinned ramp: one jack is on the table, so the next block is the
	// three remaining jacks, one per suit.
	next := 0
	for _, n := range snap.NextSuits {
		if n > 1 {
			t.Errorf("next-suit count = %d, want at most 1 for the jacks", n)
		}
		next += n
	}
	if next != 3 {
		t.Errorf("next-suit total = %d, want 3 jacks", next)
	}
}
