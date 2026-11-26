// Command rngref is an independent reference implementation of the Regicide
// setup shuffle. It exists to generate golden vectors: if the engine's deal ever
// diverges from this file, either the shuffle contract changed (bump
// engine.Version and regenerate) or a bug was introduced.
//
// It is written from docs/PLAN.md §4.4 and the official setup rules, not by
// calling into the engine: deck construction, shuffle direction and stream order
// are duplicated on purpose.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

type card struct {
	ID   string `json:"id"`
	Rank string `json:"rank"`
	Suit string `json:"suit"`
}

type golden struct {
	Seed     string   `json:"seed"`
	Note     string   `json:"note"`
	Hand     []string `json:"hand"`
	Tavern   []string `json:"tavern"`
	Enemy    string   `json:"enemy"`
	Castle   []string `json:"castle"`
	RGNDraws int      `json:"rngDraws"`
}

var alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func normalize(seed string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(seed)) {
		switch r {
		case '-', ' ', '_', '\t':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// shuffle is Fisher-Yates, i from n-1 down to 1, j = u % (i+1).
type counter struct {
	r *rand.Rand
	n int
}

func (c *counter) u() uint64 {
	c.n++
	return c.r.Uint64()
}

func shuffle(cards []card, u func() uint64) {
	for i := len(cards) - 1; i > 0; i-- {
		j := int(u() % uint64(i+1))
		cards[i], cards[j] = cards[j], cards[i]
	}
}

func main() {
	seed := "AAAA-AAAA-AAAA"
	if len(os.Args) > 1 {
		seed = os.Args[1]
	}
	sum := sha256.Sum256([]byte(normalize(seed)))
	c := &counter{r: rand.New(rand.NewChaCha8(sum))}

	suits := []string{"C", "D", "H", "S"}
	build := func(rank string, value int) []card {
		out := make([]card, 0, 4)
		for _, s := range suits {
			out = append(out, card{ID: rank + s, Rank: rank, Suit: s})
		}
		return out
	}

	kings := build("K", 20)
	queens := build("Q", 15)
	jacks := build("J", 10)
	// Pinned order: Kings, Queens, Jacks, then the tavern deck.
	shuffle(kings, c.u)
	shuffle(queens, c.u)
	shuffle(jacks, c.u)

	tavern := make([]card, 0, 40)
	for _, rank := range []string{"2", "3", "4", "5", "6", "7", "8", "9", "10"} {
		tavern = append(tavern, build(rank, 0)...)
	}
	tavern = append(tavern, build("A", 1)...)
	shuffle(tavern, c.u)

	castle := append([]card{}, jacks...)
	castle = append(castle, queens...)
	castle = append(castle, kings...)

	enemy := castle[0]
	castle = castle[1:]
	hand := append([]string{}, idsOf(tavern[:8])...)
	tavern = tavern[8:]

	out := golden{
		Seed:     seed,
		Note:     "independent reference; regenerate with: GO111MODULE=off GOPATH=$PWD go run regicide/tools/rngref",
		Hand:     hand,
		Tavern:   idsOf(tavern),
		Enemy:    enemy.ID,
		Castle:   idsOf(castle),
		RGNDraws: c.n,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func idsOf(cards []card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}
