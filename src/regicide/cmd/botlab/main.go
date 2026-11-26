// botlab runs a list of weight variants over one fixed seed suite, so a tuning round
// is a table instead of an opinion. It never changes the shipped policy: it builds
// the same greedy search with `bot.GreedyWith` and the base named in the config.
//
//	go run regicide/cmd/botlab -n 2000 -seats 3 -seed CAPCHECK -configs round.json
//	go run regicide/cmd/botlab -base human-bestof -dump
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"regicide/bot"
)

type variant struct {
	Name      string             `json:"name"`
	Overrides map[string]float64 `json:"overrides"`
}

type scored struct {
	name    string
	wins    float64
	castles float64
	games   int
}

func main() {
	n := flag.Int("n", 2000, "games per variant")
	seats := flag.Int("seats", 3, "players at the table")
	seedBase := flag.String("seed", "CAPCHECK", "seed prefix")
	baseName := flag.String("base", "human-bestof", "weights to start from")
	configs := flag.String("configs", "", "JSON file: [{name, overrides}]")
	dump := flag.Bool("dump", false, "print the base weights as JSON overrides and exit")
	flag.Parse()

	base, ok := bot.BaseWeights(*baseName)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown base %q\n", *baseName)
		os.Exit(2)
	}
	if *dump {
		out := map[string]float64{}
		v := reflect.ValueOf(base)
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() == reflect.Float64 {
				out[t.Field(i).Name] = v.Field(i).Float()
			}
		}
		raw, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(raw))
		return
	}
	if *configs == "" {
		fmt.Fprintln(os.Stderr, "-configs is required (or use -dump)")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*configs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var variants []variant
	if err := json.Unmarshal(raw, &variants); err != nil {
		fmt.Fprintf(os.Stderr, "configs: %v\n", err)
		os.Exit(1)
	}

	start := time.Now()
	results := make([]scored, 0, len(variants))
	for _, v := range variants {
		w := base
		if err := applyOverrides(&w, v.Overrides); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", v.Name, err)
			os.Exit(1)
		}
		policy := bot.GreedyWith(w)
		tally := bot.Tally{}
		for i := 0; i < *n; i++ {
			seed := fmt.Sprintf("%s-%d", *seedBase, i)
			res, subs, err := bot.PlayStrict(seed, *seats, policy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s seed %s: %v\n", v.Name, seed, err)
				os.Exit(1)
			}
			if subs > 0 {
				fmt.Fprintf(os.Stderr, "%s seed %s: %d substitutions\n", v.Name, seed, subs)
			}
			tally.Add(res)
		}
		results = append(results, scored{v.Name, tally.WinRate() * 100, tally.CastlesPerGame(), tally.Games})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].wins != results[j].wins {
			return results[i].wins > results[j].wins
		}
		return results[i].castles > results[j].castles
	})
	fmt.Printf("base %s, %d seats, %d games/variant, seeds %s-0..%d, %s\n",
		*baseName, *seats, *n, *seedBase, *n-1, time.Since(start).Round(time.Millisecond))
	fmt.Printf("%-28s %7s %8s\n", "variant", "wins", "royals")
	for _, r := range results {
		fmt.Printf("%-28s %6.1f%% %8.2f\n", r.name, r.wins, r.castles)
	}
}

func applyOverrides(w *bot.Weights, o map[string]float64) error {
	v := reflect.ValueOf(w).Elem()
	t := v.Type()
	fields := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Float64 {
			fields[strings.ToLower(t.Field(i).Name)] = i
		}
	}
	for name, value := range o {
		i, ok := fields[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("unknown weight %q", name)
		}
		v.Field(i).SetFloat(value)
	}
	return nil
}
