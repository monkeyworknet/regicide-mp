// coopbench measures the co-op bot against seeds, offline and fast.
//
//	go run regicide/cmd/coopbench -n 200 -seats 3 -policy greedy
//
// It reports the numbers the plan asks for — win rate, royals cleared, turns, and the
// reason a game was lost — so a policy change is a measurement instead of an opinion.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"regicide/bot"
	"regicide/engine"
)

func main() {
	n := flag.Int("n", 200, "games to play")
	seats := flag.Int("seats", 3, "players at the table")
	policyName := flag.String("policy", "greedy", "greedy | random")
	seedBase := flag.String("seed", "BENCH", "seed prefix")
	verbose := flag.Bool("v", false, "print every game")
	// Corpus generation: one verified replay per game, and optionally one combined
	// training file (state + legal + chosen + outcome per decision). See
	// docs/REPLAY-FORMAT.md.
	corpusDir := flag.String("corpus", "", "write one verified replay per game to this directory")
	trainingFile := flag.String("training", "", "write a combined training corpus (JSONL) to this file")
	flag.Parse()

	var policy bot.Policy
	if *policyName == "random" {
		policy = bot.Random(rand.New(rand.NewSource(1)))
	} else if p, ok := bot.Policies()[*policyName]; ok {
		policy = p
	} else {
		names := []string{"random"}
		for name := range bot.Policies() {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "unknown policy %q (choose %s)\n", *policyName, strings.Join(names, ", "))
		os.Exit(2)
	}

	// The training file is opened once; each game's verified decisions are appended.
	var training *os.File
	var enc *json.Encoder
	decisions := 0
	if *trainingFile != "" {
		if err := os.MkdirAll(filepathDir(*trainingFile), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var err error
		training, err = os.Create(*trainingFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer training.Close()
		enc = json.NewEncoder(training)
	}

	start := time.Now()
	tally := bot.Tally{}
	castleSpread := make([]int, 0, *n)
	failures := 0
	recorded := 0
	for i := 0; i < *n; i++ {
		seed := fmt.Sprintf("%s-%d", *seedBase, i)
		var actions []engine.Action
		onStep := func(_ engine.Game, act engine.Action) { actions = append(actions, act) }
		res, subs, err := bot.PlayStrictWith(seed, *seats, policy, onStep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", seed, err)
			failures++
			continue
		}
		if subs > 0 {
			fmt.Fprintf(os.Stderr, "%s: %d illegal actions were substituted\n", seed, subs)
		}
		if *corpusDir != "" || *trainingFile != "" {
			dir := *corpusDir
			if dir == "" {
				dir = os.TempDir()
			}
			path, recs, err := recordGame(dir, seed, *seats, actions)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: corpus: %v\n", seed, err)
				failures++
				continue
			}
			recorded++
			if enc != nil {
				for j := range recs {
					if err := enc.Encode(&recs[j]); err != nil {
						fmt.Fprintf(os.Stderr, "%s: training: %v\n", seed, err)
						failures++
						break
					}
					decisions++
				}
			}
			if *verbose && path != "" {
				fmt.Printf("%-16s corpus %s (%d steps)\n", seed, path, len(actions))
			}
		}
		tally.Add(res)
		castleSpread = append(castleSpread, res.Castles)
		if *verbose {
			fmt.Printf("%-16s castles %2d turns %3d won %v lost %s\n", seed, res.Castles, res.Turns, res.Won, res.LostFor)
		}
	}
	if failures > 0 {
		os.Exit(1)
	}
	sort.Ints(castleSpread)
	median := 0
	if len(castleSpread) > 0 {
		median = castleSpread[len(castleSpread)/2]
	}
	fmt.Printf("policy %s, %d seats, %d games in %s\n", *policyName, *seats, tally.Games, time.Since(start).Round(time.Millisecond))
	fmt.Printf("  wins            %6.1f %%\n", tally.WinRate()*100)
	fmt.Printf("  royals cleared  %6.2f / 12 (median %d)\n", tally.CastlesPerGame(), median)
	fmt.Printf("  turns           %6.1f (max %d)\n", tally.TurnsPerGame(), tally.MaxTurns)
	if *corpusDir != "" || *trainingFile != "" {
		fmt.Printf("  corpus          %6d game(s), %d decisions\n", recorded, decisions)
		if *corpusDir != "" {
			fmt.Printf("  replay dir      %s\n", *corpusDir)
		}
		if *trainingFile != "" {
			fmt.Printf("  training        %s\n", *trainingFile)
		}
	}
	for _, reason := range sortedKeys(tally.LostFor) {
		fmt.Printf("  lost: %-14s %4d\n", reason, tally.LostFor[reason])
	}
}

func filepathDir(p string) string {
	d := filepath.Dir(p)
	if d == "." {
		return "."
	}
	return d
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
