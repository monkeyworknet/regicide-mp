package server

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"regicide/engine"
)

// The two Jester faces are table state, not a browser preference: every seat and
// the replay must agree on which card J1 is. The deal picks the pair here, from
// the registry that ships with the binary, so a set can change its faces without
// touching Go.
//
// The rule the operator set: a generated pair never borrows another set's faces.
// Simple and Standard draw from the shared sheet only; Official and Pokémon bring
// their own pair. A pair the player chose by hand is kept as-is, even across sets.
type jokerCatalog struct {
	all   map[string]bool
	fixed map[string][]string
	pool  []string
}

var (
	jokerCatalogOnce sync.Once
	jokerCatalogVal  jokerCatalog
)

// embeddedJokerCatalog reads web/data/themes.json from the binary. The live server
// serves this same file; a -web dev run may edit the on-disk copy for the UI, but
// the deal logic still uses the registry the binary carries.
func embeddedJokerCatalog() jokerCatalog {
	jokerCatalogOnce.Do(func() {
		raw, err := fs.ReadFile(webFS, "web/data/themes.json")
		if err != nil {
			return
		}
		if c, ok := parseJokerCatalog(raw); ok {
			jokerCatalogVal = c
		}
	})
	return jokerCatalogVal
}

// parseJokerCatalog pulls the ids, the per-set pairs and the shared sheet pool out
// of a themes.json document. The pool is every face no set claims: adding a face to
// the catalogue therefore adds it to Simple and Standard, and adding a set pair
// removes those faces from the pool.
func parseJokerCatalog(raw []byte) (jokerCatalog, bool) {
	var doc struct {
		DefaultJoker string `json:"defaultJoker"`
		Themes       []struct {
			ID            string   `json:"id"`
			DefaultJokers []string `json:"defaultJokers"`
		} `json:"themes"`
		Jokers []struct {
			ID string `json:"id"`
		} `json:"jokers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return jokerCatalog{}, false
	}
	c := jokerCatalog{all: map[string]bool{}, fixed: map[string][]string{}}
	for _, j := range doc.Jokers {
		if j.ID != "" {
			c.all[j.ID] = true
		}
	}
	if len(c.all) == 0 {
		return jokerCatalog{}, false
	}
	claimed := map[string]bool{}
	if doc.DefaultJoker != "" {
		claimed[doc.DefaultJoker] = true
	}
	for _, t := range doc.Themes {
		var pair []string
		for _, id := range t.DefaultJokers {
			if c.all[id] {
				pair = append(pair, id)
				claimed[id] = true
			}
		}
		if len(pair) > 0 {
			c.fixed[t.ID] = pair
		}
	}
	for _, j := range doc.Jokers {
		if j.ID != "" && !claimed[j.ID] {
			c.pool = append(c.pool, j.ID)
		}
	}
	return c, true
}

// dealsJokers reports whether the catalogue loaded at all. A broken registry must
// not stop a game; the client then keeps its own pair.
func (c jokerCatalog) ready() bool { return len(c.all) > 0 }

// cleanPair normalises a browser-supplied pair to exactly two catalogue ids. A
// single id is used twice, matching the Settings picker.
func (c jokerCatalog) cleanPair(in []string) []string {
	var out []string
	for _, id := range in {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || !c.all[id] {
			return nil
		}
		out = append(out, id)
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return []string{out[0], out[0]}
	}
	return []string{out[0], out[1]}
}

// allowed reports whether a face belongs to a set's own pool of choices.
func (c jokerCatalog) allowed(theme, id string) bool {
	if fixed, ok := c.fixed[theme]; ok {
		return slices.Contains(fixed, id)
	}
	return slices.Contains(c.pool, id)
}

// dealJokers resolves the pair a deal will show.
//
//   - A pair the player chose by hand (custom) wins, as long as both ids exist.
//   - Otherwise a pair that fits the set is kept, so a seeded rerun is stable.
//   - Otherwise the set's own fixed pair, or a pair drawn from the shared sheet,
//     seeded by the game seed so every client and the replay agree.
func dealJokers(theme string, requested []string, custom bool, seed string) []string {
	c := embeddedJokerCatalog()
	if !c.ready() {
		return c.cleanPair(requested)
	}
	theme = strings.ToLower(strings.TrimSpace(theme))
	if pair := c.cleanPair(requested); pair != nil {
		if custom {
			return pair
		}
		if c.allowed(theme, pair[0]) && c.allowed(theme, pair[1]) {
			return pair
		}
	}
	if fixed, ok := c.fixed[theme]; ok && len(fixed) > 0 {
		if len(fixed) == 1 {
			return []string{fixed[0], fixed[0]}
		}
		return []string{fixed[0], fixed[1]}
	}
	if len(c.pool) >= 2 {
		r := engine.NewSeededRand(seed + "|jokers")
		first := int(r.Uint64() % uint64(len(c.pool)))
		second := int(r.Uint64() % uint64(len(c.pool)-1))
		if second >= first {
			second++
		}
		return []string{c.pool[first], c.pool[second]}
	}
	return c.cleanPair(requested)
}
