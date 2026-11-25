package rave

import (
	"math/rand"
	"testing"
	"time"

	"weiqi-step/engine"
	"weiqi-step/rollout"
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
}

func TestSameSeedSameResult(t *testing.T) {
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

func TestNoLegalMovePasses(t *testing.T) {
	b := engine.New(2, 7.5)
	b.Color[0], b.Color[1] = engine.Black, engine.White
	b.Color[2], b.Color[3] = engine.White, engine.Black
	res := Search(b, Config{PerMove: 2, Think: time.Second, Seed: 1})
	if !res.Pass || res.Rounds != 0 || res.Pt != -1 {
		t.Fatalf("%+v", res)
	}
}

func TestOverPasses(t *testing.T) {
	b := engine.New(2, 7.5)
	b.Passes = 2
	res := Search(b, Config{PerMove: 2, Think: time.Second, Seed: 1})
	if !res.Pass || res.Rounds != 0 {
		t.Fatalf("%+v", res)
	}
}

// TestUCTVisitsEveryRootMove 确认探索项会让每个根上的点都被下到，而不是把盘数堆给第一支。
func TestUCTVisitsEveryRootMove(t *testing.T) {
	b := engine.New(2, 100)
	res := Search(b, Config{PerMove: 8, Think: time.Second, Seed: 4})
	if res.Pass || res.Rounds != 32 || len(res.Moves) != 4 {
		t.Fatalf("%+v", res)
	}
	maxGames := 0
	for _, m := range res.Moves {
		if m.Games < 2 || m.RaveN < m.Games {
			t.Fatalf("%+v", m)
		}
		if m.Games > maxGames {
			maxGames = m.Games
		}
	}
	if maxGames >= res.Rounds {
		t.Fatalf("one move took every simulation: %+v", res.Moves)
	}
}

// TestPicksWinningPoint 确认必胜的点访问次数更多。2 路、贴 0.5 目，只有一个点能赢。
func TestPicksWinningPoint(t *testing.T) {
	cases := []struct {
		side engine.Color
		want int
	}{
		{engine.Black, 1},
		{engine.White, 0},
	}
	for _, tc := range cases {
		b := engine.New(2, 0.5)
		b.Color[2] = engine.Black
		b.Color[3] = engine.White
		b.Side = tc.side
		res := Search(b, Config{PerMove: 8, Think: time.Second, Seed: 5})
		if res.Pass || res.Pt != tc.want || len(res.Moves) == 0 || res.Moves[0].Pt != tc.want || res.Moves[0].WinRate != 1 {
			t.Fatalf("side %s: %+v", tc.side.Letter(), res)
		}
		if len(res.Moves) > 1 && (res.Moves[0].Games <= res.Moves[1].Games || res.Moves[0].WinRate <= res.Moves[1].WinRate) {
			t.Fatalf("side %s moves: %+v", tc.side.Letter(), res.Moves)
		}
	}
}

func TestUntriedScoresAboveSaturated(t *testing.T) {
	untried := score(20, 0, 0, 0, 0, 0.1, 1000)
	played := score(20, 20, 10, 0, 0, 0.1, 1000)
	if untried <= played {
		t.Fatalf("untried %.4f played %.4f", untried, played)
	}
}

func TestTreeSkipsOwnEye(t *testing.T) {
	b := engine.New(5, 7.5)
	b.Color[b.Index(1, 0)] = engine.Black
	b.Color[b.Index(0, 1)] = engine.Black
	eye := b.Index(0, 0)
	s := engine.NewScratch(b.N)
	if !b.ProperSmallEye(eye, engine.Black) {
		t.Fatal("fixture is not an eye")
	}
	legal := false
	for _, p := range rollout.Legal(b, s) {
		if p == eye {
			legal = true
		}
	}
	if !legal {
		t.Fatal("eye is illegal")
	}
	res := Search(b, Config{PerMove: 4, Think: time.Second, Seed: 1})
	if res.Pass || res.Pt == eye {
		t.Fatalf("%+v", res)
	}
	for _, m := range res.Moves {
		if m.Pt == eye {
			t.Fatalf("eye was expanded: %+v", res.Moves)
		}
	}
}

func TestSamePositionSharesNode(t *testing.T) {
	b := engine.New(3, 7.5)
	s := engine.NewScratch(b.N)
	rng := rand.New(rand.NewSource(1))
	root := &node{}
	root.ensure(b, s, rng)
	seen := map[string]*node{}
	first := follow(t, b, root, seen, s, rng, []int{0, 1, 2})
	second := follow(t, b, root, seen, s, rng, []int{2, 1, 0})
	if first != second {
		t.Fatal("two orders did not share a node")
	}
	first.visits = 7
	first.wins = 4
	if second.visits != 7 || second.wins != 4 {
		t.Fatal("shared node did not keep one account")
	}
}

func TestPositionKeySplitsSideAndKo(t *testing.T) {
	b := engine.New(3, 7.5)
	b.Color[0] = engine.Black
	base := positionKey(b)
	b.Side = engine.White
	flipped := positionKey(b)
	b.Side = engine.Black
	b.Ko = 4
	ko := positionKey(b)
	b.Ko = -1
	b.Passes = 1
	passed := positionKey(b)
	if base == flipped || base == ko || base == passed || flipped == ko || flipped == passed || ko == passed {
		t.Fatal("different positions collapsed")
	}
}

func follow(t *testing.T, root *engine.Board, n *node, seen map[string]*node, s *engine.Scratch, rng *rand.Rand, pts []int) *node {
	t.Helper()
	b := root.Clone()
	for _, pt := range pts {
		n.ensure(b, s, rng)
		if _, ok := b.Play(pt, s); !ok {
			t.Fatalf("illegal %d", pt)
		}
		n.dropUntried(pt)
		var fresh bool
		n, fresh = n.attach(b, pt, seen, s, rng)
		if fresh {
			continue
		}
	}
	return n
}

func TestRaveOverridesFewLosses(t *testing.T) {
	withRave := score(10, 4, 0, 100, 90, 0.1, 1000)
	rawWin := score(10, 4, 4, 0, 0, 0.1, 1000)
	if withRave <= rawWin {
		t.Fatalf("rave %.4f raw %.4f", withRave, rawWin)
	}
}
