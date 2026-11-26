// Package names hands out the display names used on a co-op seat rail.
//
// Co-op has no accounts and no leaderboard, so a player never types a name: the
// server assigns one when the seat is created or joined. The pool is historical
// European royalty, weighted the way the table reads best (D-38): most names are
// English or French, and only the short, easy German ones survive — "Otto" belongs
// on a seat rail, "Sigismund" does not.
package names

import "regicide/engine"

// English holds English and British royal names.
var English = []string{
	"Alfred", "Anne", "Arthur", "Athelstan", "Charlotte", "Cnut", "Edith",
	"Edmund", "Edward", "Egbert", "Eleanor", "Elizabeth", "George", "Harold",
	"Henry", "Isabella", "James", "Jane", "Joan", "John", "Margaret", "Mary",
	"Matilda", "Maud", "Offa", "Philippa", "Richard", "Robert", "Stephen",
	"Victoria", "William",
}

// French holds French royal names. Spelling is modern English so nothing needs a
// diacritic and every name is one word on a phone.
var French = []string{
	"Adele", "Alix", "Berthe", "Blanche", "Catherine", "Charles", "Clovis",
	"Dagobert", "Francois", "Henri", "Isabeau", "Isabelle", "Jeanne", "Louis",
	"Marguerite", "Marie", "Pepin", "Philippe", "Raoul", "Sibylle",
}

// German holds only the short, plain German names: a seat rail is read at a glance
// and shouted across a table.
var German = []string{
	"Agnes", "Albrecht", "Beatrix", "Friedrich", "Gisela", "Hedwig", "Heinrich",
	"Karl", "Konrad", "Leopold", "Ludwig", "Otto", "Sophie", "Wilhelm",
}

// Pool is every assignable name.
var Pool = func() []string {
	out := make([]string, 0, len(English)+len(French)+len(German))
	out = append(out, English...)
	out = append(out, French...)
	out = append(out, German...)
	return out
}()

// MaxLen is the longest name the seat rail is designed to show.
const MaxLen = 11

// Assign returns n distinct names for a room, deterministically from seed. A room
// created with the same seed always gets the same table, which keeps a replayed
// session (and its tests) reproducible.
func Assign(seed string, n int) []string {
	if n <= 0 {
		return nil
	}
	pool := append([]string(nil), Pool...)

	// A deterministic shuffle from the room seed, reusing the engine's stream so
	// there is one random source in the program.
	r := engine.NewSeededRand(seed + "-royal-names")
	for i := len(pool) - 1; i > 0; i-- {
		j := int(r.Uint64() % uint64(i+1))
		pool[i], pool[j] = pool[j], pool[i]
	}

	if n > len(pool) {
		n = len(pool)
	}
	return append([]string(nil), pool[:n]...)
}

// Valid reports whether name is a member of the pool, so a stored room or a test
// cannot smuggle in a name the table never assigned.
func Valid(name string) bool {
	for _, candidate := range Pool {
		if candidate == name {
			return true
		}
	}
	return false
}
