// Command replay verifies, summarises and exports a recorded Regicide game.
//
//	go run regicide/cmd/replay -verify game.jsonl
//	go run regicide/cmd/replay -summary game.jsonl
//	go run regicide/cmd/replay -training corpus.jsonl game.jsonl
//
// A replay is self-proving: the tool rebuilds the game from the header's seed, applies
// every recorded action, and compares the state hash after each one. If anything in
// the file was edited, it says which step and stops.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"regicide/replay"
)

func main() {
	verify := flag.Bool("verify", false, "replay the game and check every state hash")
	summary := flag.Bool("summary", false, "print the after-action numbers")
	training := flag.String("training", "", "write a training corpus (JSONL) to this file")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: replay [-verify] [-summary] [-training out.jsonl] <file.jsonl>")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	rep, err := replay.Read(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", rep)

	// Verification is also implied by the other modes: a summary of a file that does
	// not replay would be a summary of nothing.
	game, err := rep.Verify()
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAILED:", err)
		os.Exit(1)
	}
	if *verify {
		fmt.Printf("verified: %d steps, final phase %s, castle %d left, turn %d\n",
			len(rep.Steps), game.Phase, len(game.Castle), game.TurnNo)
	}
	if *summary {
		sum, err := rep.Summarise()
		if err != nil {
			fmt.Fprintln(os.Stderr, "summary:", err)
			os.Exit(1)
		}
		fmt.Printf("\n%s · %s · seed %s · %d players · %d steps\n",
			orDefault(sum.Mode, "solo"), sum.Theme, sum.Seed, len(sum.Players), sum.Steps)
		fmt.Printf("outcome: %s", sum.Phase)
		if sum.Won {
			fmt.Printf(" (won, %d jesters used)", game.JestersUsed)
		} else if sum.LostReason != "" {
			fmt.Printf(" (%s, seat %d)", sum.LostReason, sum.LostSeat)
		}
		fmt.Printf(" · turn %d · castle %d left\n", sum.Turns, sum.CastleLeft)
		for i, p := range sum.Players {
			actions, damage := 0, 0
			if i < len(sum.ActionsBySeat) {
				actions = sum.ActionsBySeat[i]
			}
			if i < len(sum.DamageBySeat) {
				damage = sum.DamageBySeat[i]
			}
			fmt.Printf("  seat %d %-12s %2d actions, %3d damage dealt\n", p.Seat, p.Name, actions, damage)
		}
	}
	if *training != "" {
		recs, err := rep.Training()
		if err != nil {
			fmt.Fprintln(os.Stderr, "training:", err)
			os.Exit(1)
		}
		out, err := os.Create(*training)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer out.Close()
		enc := json.NewEncoder(out)
		for _, rec := range recs {
			if err := enc.Encode(rec); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		fmt.Printf("training corpus: %d decisions -> %s\n", len(recs), *training)
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
