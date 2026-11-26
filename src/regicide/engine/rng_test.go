package engine

import (
	"strings"
	"testing"
)

// TestValidSeed pins the seed gate the API applies.
func TestValidSeed(t *testing.T) {
	good := []string{"A", "abc", "FLOW-SEED-01", "SMOKE-SEED-1", "RESUME-SEED1", strings.Repeat("A", 32)}
	bad := []string{"", "   ", "-------", "se/ed", "seed\x00x", strings.Repeat("A", 33), "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}
	for _, s := range good {
		if !ValidSeed(s) {
			t.Errorf("ValidSeed(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidSeed(s) {
			t.Errorf("ValidSeed(%q) = true, want false", s)
		}
	}
}
