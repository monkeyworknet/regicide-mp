package server

import (
	"reflect"
	"sort"
	"testing"

	"regicide/engine"
)

// The theme whitelist used to be a hand-written map that had to be kept in sync
// with the generated registry; a set added to themes.json but not to the map
// silently fell back to the default theme. It is now read from the embedded registry.
func TestThemeWhitelistComesFromRegistry(t *testing.T) {
	srv := newTestServer(t)
	ids := srv.registryThemeIDs()
	if len(ids) == 0 {
		t.Fatal("registry produced no theme ids")
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(sorted, []string{"8bit", "official", "pokemon", "simple"}) {
		t.Fatalf("theme ids = %v, want the four shipped sets", sorted)
	}
	for _, id := range ids {
		if got := srv.themeOrDefault(id); got != id {
			t.Fatalf("themeOrDefault(%q) = %q, want it accepted", id, got)
		}
	}
	// Unknown, empty and hostile values fall back to a real theme.
	for _, bad := range []string{"", "jorel", "classic", "../../etc/passwd", "MODERN"} {
		got := srv.themeOrDefault(bad)
		if got != "official" {
			t.Fatalf("themeOrDefault(%q) = %q, want official", bad, got)
		}
	}
	// Case and whitespace are normalised before the lookup.
	if got := srv.themeOrDefault("  Pokemon "); got != "pokemon" {
		t.Fatalf("themeOrDefault(%q) = %q, want pokemon", "  Pokemon ", got)
	}
}

// A new theme must be accepted by the API without touching Go code.
func TestThemeRegistryDrivesSessionTheme(t *testing.T) {
	srv := newTestServer(t)
	for _, id := range srv.registryThemeIDs() {
		sess, err := srv.store.Create("", id, engine.DefaultRules())
		if err != nil {
			t.Fatalf("create with theme %q: %v", id, err)
		}
		if sess.Game.Theme != id {
			t.Fatalf("session theme = %q, want %q", sess.Game.Theme, id)
		}
	}
}
