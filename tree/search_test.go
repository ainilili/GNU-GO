package tree

import (
	"testing"
	"time"

	"weiqi-step/engine"
)

func TestSearchDoesNotMutateRoot(t *testing.T) {
	b := engine.New(5, 7.5)
	before := b.String()
	res := Search(b, Config{PerMove: 1, Think: time.Second, Seed: 1})
	if b.String() != before || b.Side != engine.Black {
		t.Fatal("root changed")
	}
	if res.Pass || res.Rounds < 1 || len(res.Moves) == 0 {
		t.Fatalf("%+v", res)
	}
	sum := 0
	for _, m := range res.Moves {
		sum += m.Games
	}
	if sum != res.Games || res.Games != res.Rounds {
		t.Fatalf("games=%d rounds=%d sum=%d", res.Games, res.Rounds, sum)
	}
}

func TestFirstWaveExpandsEveryRootMove(t *testing.T) {
	b := engine.New(2, 100)
	res := Search(b, Config{PerMove: 1, Think: time.Second, Seed: 3})
	if res.Rounds != 4 || res.Pass || len(res.Moves) != 4 {
		t.Fatalf("%+v", res)
	}
	seen := map[int]bool{}
	for _, m := range res.Moves {
		if m.Games != 1 || m.Wins != 0 || seen[m.Pt] {
			t.Fatalf("%+v", m)
		}
		seen[m.Pt] = true
	}
	if res.Moves[0].Pt != res.Pt {
		t.Fatalf("played %d, first row %d", res.Pt, res.Moves[0].Pt)
	}
}

func TestSameSeedSameTree(t *testing.T) {
	cfg := Config{PerMove: 2, Think: time.Second, Seed: 9}
	a := Search(engine.New(2, 7.5), cfg)
	b := Search(engine.New(2, 7.5), cfg)
	if a.Pt != b.Pt || a.Rounds != b.Rounds || len(a.Moves) != len(b.Moves) {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	for i := range a.Moves {
		if a.Moves[i] != b.Moves[i] {
			t.Fatalf("%+v vs %+v", a.Moves[i], b.Moves[i])
		}
	}
}

func TestVisitBetterBeatsHighWinRate(t *testing.T) {
	if !visitBetter(1, 5, 2, 2) {
		t.Fatal("5 visits at 20% must beat 2 visits at 100%")
	}
	if visitBetter(2, 2, 1, 5) {
		t.Fatal("fewer visits must not win")
	}
	if !visitBetter(3, 4, 1, 4) {
		t.Fatal("equal visits use win rate")
	}
}

func TestRateBetterIsNotVisitCount(t *testing.T) {
	if !rateBetter(2, 2, 1, 5) {
		t.Fatal("descent follows win rate")
	}
	if rateBetter(1, 5, 2, 2) {
		t.Fatal("more visits must not override a higher rate")
	}
}

func TestNoLegalMovePasses(t *testing.T) {
	b := engine.New(2, 7.5)
	b.Color[0], b.Color[1] = engine.Black, engine.White
	b.Color[2], b.Color[3] = engine.White, engine.Black
	res := Search(b, Config{PerMove: 2, Think: time.Second, Seed: 1})
	if !res.Pass || res.Rounds != 0 {
		t.Fatalf("%+v", res)
	}
}
