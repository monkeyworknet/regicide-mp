package engine

import (
	"crypto/rand"
	"crypto/sha256"
	mrand "math/rand/v2"
	"strings"
)

// Rand is the engine's only source of randomness. Production uses a seeded
// ChaCha8 stream; tests substitute a scripted implementation so shuffle outcomes
// are assertable (docs/PLAN.md §4.4).
type Rand interface {
	Uint64() uint64
}

// SeededRand wraps math/rand/v2's ChaCha8, which is algorithmically specified and
// therefore stable across Go versions, platforms and machines.
type SeededRand struct{ r *mrand.Rand }

// NewSeededRand derives a deterministic stream from a human seed string.
func NewSeededRand(seed string) *SeededRand {
	return &SeededRand{r: mrand.New(mrand.NewChaCha8(sha256.Sum256([]byte(NormalizeSeed(seed)))))}
}

// Uint64 implements Rand.
func (s *SeededRand) Uint64() uint64 { return s.r.Uint64() }

// NewRandAt rebuilds a stream at a given draw offset. Sessions persist only the
// seed and the number of draws, which is enough to resume the exact same stream
// without serialising ChaCha8's internal state.
func NewRandAt(seed string, skip int) *SeededRand {
	if skip < 0 {
		skip = 0
	}
	r := NewSeededRand(seed)
	for i := 0; i < skip; i++ {
		r.Uint64()
	}
	return r
}

// ScriptedRand returns a fixed sequence and then repeats the last value. Used by
// fixtures to pin shuffle behaviour.
type ScriptedRand struct {
	values []uint64
	i      int
}

// NewScriptedRand builds a scripted stream. An empty script yields zeros.
func NewScriptedRand(values ...uint64) *ScriptedRand { return &ScriptedRand{values: values} }

// Uint64 implements Rand.
func (s *ScriptedRand) Uint64() uint64 {
	if len(s.values) == 0 {
		return 0
	}
	v := s.values[s.i%len(s.values)]
	s.i++
	return v
}

// NormalizeSeed canonicalises a human-entered seed: upper case, no dashes or
// spaces, and the look-alike fixes Crockford Base32 expects (I/L -> 1, O -> 0).
func NormalizeSeed(seed string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(seed)) {
		switch r {
		case '-', ' ', '_', '\t':
			continue
		case 'I', 'L':
			b.WriteRune('1')
		case 'O':
			b.WriteRune('0')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

const seedAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford Base32: no I, L, O, U

// RandomSeed generates a fresh 12-character seed from crypto/rand.
func RandomSeed() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not recoverable in a meaningful way; fall back to
		// a fixed-but-unlikely pattern rather than panicking in a request handler.
		return "000000000000"
	}
	out := make([]byte, 12)
	for i, v := range buf {
		out[i] = seedAlphabet[int(v)%len(seedAlphabet)]
	}
	return string(out)
}

// FormatSeed groups a seed for display: "ABCD-EFGH-JKMN".
func FormatSeed(seed string) string {
	s := NormalizeSeed(seed)
	if len(s) != 12 {
		return s
	}
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}

// ValidSeed reports whether a human-entered seed is usable: after normalisation it
// must be 1..32 letters or digits. The seed is hashed, so short seeds are fine and
// any letter is allowed (Crockford's exclusions only matter for generated seeds).
// This is the gate the API applies; it keeps control characters, slashes and
// very long strings out of session storage.
func ValidSeed(seed string) bool {
	s := NormalizeSeed(seed)
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// shuffleFisherYates is the engine's only shuffle. The direction is pinned
// (i from n-1 down to 1, j = u % (i+1)) so a seed reproduces a game exactly and
// a golden vector detects a wrong algorithm (docs/PLAN.md §4.4).
func shuffleFisherYates(cards []Card, r Rand) {
	for i := len(cards) - 1; i > 0; i-- {
		j := int(r.Uint64() % uint64(i+1))
		cards[i], cards[j] = cards[j], cards[i]
	}
}
