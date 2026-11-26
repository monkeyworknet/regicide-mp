package names

import (
	"strings"
	"testing"
)

func TestPoolIsWeightedEnglishFrenchGerman(t *testing.T) {
	// D-38: the table should read mostly English and French, with only the plain
	// German names. If someone rebalances the pool, they must do it on purpose.
	if len(English) <= len(French) {
		t.Errorf("English (%d) should outnumber French (%d)", len(English), len(French))
	}
	if len(French) <= len(German) {
		t.Errorf("French (%d) should outnumber German (%d)", len(French), len(German))
	}
	if len(German) > 16 {
		t.Errorf("German list grew to %d; it is meant to stay the short, plain names", len(German))
	}
	if len(Pool) != len(English)+len(French)+len(German) {
		t.Errorf("pool = %d, parts = %d", len(Pool), len(English)+len(French)+len(German))
	}
	for _, awkward := range []string{"Sigismund", "Ruprecht", "Kunigunde", "Wenzel", "Maximilian"} {
		if Valid(awkward) {
			t.Errorf("%q is back in the pool; it does not read on a seat rail", awkward)
		}
	}
}

func TestPoolIsClean(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range Pool {
		if name == "" {
			t.Error("an empty name is in the pool")
		}
		if len(name) > MaxLen {
			t.Errorf("%s is %d chars, the seat rail shows %d", name, len(name), MaxLen)
		}
		if strings.TrimSpace(name) != name || strings.Contains(name, " ") {
			t.Errorf("%q has stray whitespace", name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			t.Errorf("%q appears twice", name)
		}
		seen[key] = true
		if !Valid(name) {
			t.Errorf("%q is not valid to itself", name)
		}
	}
	if len(Pool) < 60 {
		t.Errorf("pool has %d names; a table of four should never feel repetitive", len(Pool))
	}
}

func TestAssignDistinct(t *testing.T) {
	for _, seats := range []int{2, 3, 4} {
		got := Assign("ROOM-SEED", seats)
		if len(got) != seats {
			t.Fatalf("%d seats: got %d names", seats, len(got))
		}
		seen := map[string]bool{}
		for _, name := range got {
			if seen[name] {
				t.Errorf("%d seats: %q assigned twice", seats, name)
			}
			seen[name] = true
			if !Valid(name) {
				t.Errorf("%d seats: %q is not in the pool", seats, name)
			}
		}
	}
}

func TestAssignDeterministic(t *testing.T) {
	first := Assign("SAME-SEED", 4)
	for i := 0; i < 20; i++ {
		again := Assign("SAME-SEED", 4)
		if strings.Join(first, ",") != strings.Join(again, ",") {
			t.Fatalf("the same seed gave %v then %v", first, again)
		}
	}
}

func TestAssignVariesBySeed(t *testing.T) {
	// Not a statistical claim, just "a new room is not the last room": over a
	// handful of seeds at least one table must differ, and a repeated table must
	// not be the norm.
	base := strings.Join(Assign("SEED-1", 4), ",")
	differed := 0
	for i := 2; i < 12; i++ {
		if strings.Join(Assign("SEED-"+itoa(i), 4), ",") != base {
			differed++
		}
	}
	if differed < 5 {
		t.Errorf("only %d of 10 seeds differed from the first table", differed)
	}
}

func TestAssignNeverOverrunsThePool(t *testing.T) {
	// The pool is deduplicated on assignment, so the ceiling is its unique size.
	got := Assign("HUGE", len(Pool)+50)
	if len(got) != len(Pool) {
		t.Fatalf("got %d names, want the whole pool of %d", len(got), len(Pool))
	}
	if got := Assign("HUGE", 0); got != nil {
		t.Errorf("zero seats returned %v", got)
	}
	if got := Assign("HUGE", -3); got != nil {
		t.Errorf("negative seats returned %v", got)
	}
}

func TestValidRejectsTypedNames(t *testing.T) {
	for _, name := range []string{"", "admin", "Player 1", "ALFRED", "alfred"} {
		if Valid(name) {
			t.Errorf("%q was accepted; the server assigns names, clients never send them", name)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
