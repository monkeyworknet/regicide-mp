package server

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"regicide/engine"
	"regicide/replay"
)

// The shared sheet is every face no set claims: an Official or Pokémon face must
// never be generated for Simple or Standard.
func TestJokerPoolIsTheSharedSheet(t *testing.T) {
	c := embeddedJokerCatalog()
	if !c.ready() {
		t.Fatal("the embedded registry produced no joker catalogue")
	}
	got := append([]string(nil), c.pool...)
	sort.Strings(got)
	want := []string{"banner", "bower", "card-table", "crown", "puppet", "ribbon", "shadow", "staff"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pool = %v, want the eight shared faces %v", got, want)
	}
	for _, id := range []string{"official", "ditto", "porygon", "jester"} {
		for _, p := range c.pool {
			if p == id {
				t.Fatalf("%q is not a Simple/Standard default but sits in the pool", id)
			}
		}
	}
	if !reflect.DeepEqual(c.fixed["official"], []string{"official", "official"}) {
		t.Fatalf("official pair = %v", c.fixed["official"])
	}
	if !reflect.DeepEqual(c.fixed["pokemon"], []string{"ditto", "porygon"}) {
		t.Fatalf("pokemon pair = %v", c.fixed["pokemon"])
	}
}

func TestDealJokersFollowsTheSet(t *testing.T) {
	if got := dealJokers("official", nil, false, "SEED"); !reflect.DeepEqual(got, []string{"official", "official"}) {
		t.Fatalf("official dealt %v", got)
	}
	if got := dealJokers("pokemon", nil, false, "SEED"); !reflect.DeepEqual(got, []string{"ditto", "porygon"}) {
		t.Fatalf("pokemon dealt %v", got)
	}
	// A stale Official default must not survive a Simple deal.
	if got := dealJokers("simple", []string{"official", "official"}, false, "SEED"); !fitsPool(got) {
		t.Fatalf("simple kept an Official pair: %v", got)
	}
	// A stale pair from the shared sheet does fit and is kept.
	if got := dealJokers("8bit", []string{"shadow", "banner"}, false, "SEED"); !reflect.DeepEqual(got, []string{"shadow", "banner"}) {
		t.Fatalf("8bit dropped a fitting pair: %v", got)
	}
	// The random pair is deterministic per seed and never repeats a face.
	a := dealJokers("simple", nil, false, "SEED-1")
	b := dealJokers("simple", nil, false, "SEED-1")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("the same seed dealt %v then %v", a, b)
	}
	if !fitsPool(a) || a[0] == a[1] {
		t.Fatalf("simple dealt %v, want two different shared faces", a)
	}
}

// A pair the player chose by hand outranks the set's rule; that is the operator's
// exception, not an accident.
func TestDealJokersKeepsAHandPick(t *testing.T) {
	got := dealJokers("simple", []string{"official", "shadow"}, true, "SEED")
	if !reflect.DeepEqual(got, []string{"official", "shadow"}) {
		t.Fatalf("hand pick was rewritten to %v", got)
	}
	// An unknown id is not a pick; it falls back to the set's pair.
	got = dealJokers("official", []string{"no-such-face", "shadow"}, true, "SEED")
	if !reflect.DeepEqual(got, []string{"official", "official"}) {
		t.Fatalf("unknown id produced %v", got)
	}
}

func fitsPool(pair []string) bool {
	if len(pair) != 2 {
		return false
	}
	c := embeddedJokerCatalog()
	for _, id := range pair {
		found := false
		for _, p := range c.pool {
			if p == id {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// A solo deal records the pair it showed, and the replay header keeps it.
func TestSoloDealRecordsJokers(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sess, err := store.CreateWith("JOKER-SEED-1", "simple", engine.DefaultRules(), JokerPick{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !fitsPool(sess.Game.Jokers) {
		t.Fatalf("solo deal = %v, want two shared faces", sess.Game.Jokers)
	}
	raw, err := os.ReadFile(store.replayPath(sess.ID))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	var first struct {
		Header replay.Header `json:"header"`
	}
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("header: %v", err)
	}
	if !reflect.DeepEqual(first.Header.Jokers, sess.Game.Jokers) {
		t.Fatalf("replay jokers = %v, game = %v", first.Header.Jokers, sess.Game.Jokers)
	}
}

// Every seat reads one pair from the deal, and the room's own view carries it.
func TestRoomDealSharesOneJokerPair(t *testing.T) {
	host := newRoomClient(t)
	host.createRoom("simple")
	guest := *host
	guest.join()
	if resp, out := host.start(); resp.StatusCode != 200 {
		t.Fatalf("start: %d %v", resp.StatusCode, out)
	}
	sess := mustSession(t, host)
	pair := sess.Game.Jokers
	if !fitsPool(pair) {
		t.Fatalf("room deal = %v, want two shared faces", pair)
	}
	for _, c := range []*roomClient{host, &guest} {
		body, ok := c.lastRoom()
		if !ok {
			t.Fatalf("seat %d could not read the room", c.seat)
		}
		state, _ := body["state"].(map[string]any)
		if state == nil {
			t.Fatalf("seat %d got no state", c.seat)
		}
		raw, _ := json.Marshal(state["jokers"])
		var got []string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("seat %d jokers: %v", c.seat, err)
		}
		if !reflect.DeepEqual(got, pair) {
			t.Fatalf("seat %d sees %v, the table dealt %v", c.seat, got, pair)
		}
	}
}
