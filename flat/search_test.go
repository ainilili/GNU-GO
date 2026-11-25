package flat

import (
	"math/rand"
	"testing"
	"time"

	"weiqi-step/engine"
)

// TestSearchDoesNotMutateRoot 确认搜索不改原棋盘，而且每个候选正好下完一轮。
func TestSearchDoesNotMutateRoot(t *testing.T) {
	b := engine.New(9, 7.5)
	before := b.String()
	res := Search(b, Config{PerMove: 1, Think: time.Second, Seed: 1})
	if b.String() != before {
		t.Fatal("root board changed")
	}
	if res.Pass || res.Pt < 0 || res.Pt >= b.N {
		t.Fatalf("expected a point, got %+v", res)
	}
	if res.Rounds != 1 {
		t.Fatalf("rounds=%d", res.Rounds)
	}
	for _, m := range res.Moves {
		if m.Games != 1 {
			t.Fatalf("candidate games=%d, want 1", m.Games)
		}
	}
}

// TestOnlyLegalMove 把除中心外的点都填上子。中心能下就只能选它，不能下就应该停一手。
func TestOnlyLegalMove(t *testing.T) {
	b := engine.New(5, 7.5)
	s := engine.NewScratch(b.N)
	// 只留中心是空的。直接改颜色，不经过 Play，所以邻块的气由 Play 当场再算。
	for p := 0; p < b.N; p++ {
		if p == 12 {
			continue
		}
		b.Color[p] = engine.Black
		if p%2 == 0 {
			b.Color[p] = engine.White
		}
	}
	b.Side = engine.Black
	res := Search(b, Config{PerMove: 1, Think: time.Second, Seed: 2})
	u, ok := b.Play(12, s)
	if ok {
		b.Undo(u, s)
		if res.Pt != 12 || res.Pass {
			t.Fatalf("only legal point is 12, got %+v", res)
		}
		return
	}
	if !res.Pass {
		t.Fatalf("no legal move, want pass, got %+v", res)
	}
}

// TestFinishRandomEnds 确认随机终局至少落下过子，或者已经连续停手结束。
func TestFinishRandomEnds(t *testing.T) {
	b := engine.New(9, 7.5)
	s := engine.NewScratch(b.N)
	rng := rand.New(rand.NewSource(3))
	_ = finishRandom(b, s, rng)
	played := 0
	for _, c := range b.Color {
		if c != engine.Empty {
			played++
		}
	}
	if played == 0 && b.Passes < 2 {
		t.Fatalf("playout made no progress: passes=%d", b.Passes)
	}
}

// splitBoard 是 2 路棋，贴 0.5 目：
//
//	..
//	XO
//
// 黑走时，点 1 的随机终局全部黑胜，点 0 全部黑负。
// 白走时正好反过来。贴 7.5 目时 2 路棋黑棋不可能赢，这个区分会消失。
func splitBoard(side engine.Color) *engine.Board {
	b := engine.New(2, 0.5)
	b.Color[2] = engine.Black
	b.Color[3] = engine.White
	b.Side = side
	return b
}

func snapshot(b *engine.Board) (string, engine.Color, int, int, [3]int) {
	return b.String(), b.Side, b.Ko, b.Passes, b.Captures
}

func assertUntouched(t *testing.T, b *engine.Board, before string, side engine.Color, ko, passes int, caps [3]int) {
	t.Helper()
	got, side2, ko2, passes2, caps2 := snapshot(b)
	if got != before || side2 != side || ko2 != ko || passes2 != passes || caps2 != caps {
		t.Fatalf("root changed: board\n%s\nside %s ko %d passes %d caps %v", got, side2.Letter(), ko2, passes2, caps2)
	}
}

// TestSearchOverPasses 确认已经连续停两次时直接停手，不再跑随机局。
func TestSearchOverPasses(t *testing.T) {
	b := engine.New(2, 7.5)
	b.Passes = 2
	before, side, ko, passes, caps := snapshot(b)
	res := Search(b, Config{PerMove: 5, Think: time.Second, Seed: 1})
	assertUntouched(t, b, before, side, ko, passes, caps)
	if !res.Pass || res.Pt != -1 || res.Games != 0 || res.Rounds != 0 || len(res.Moves) != 0 {
		t.Fatalf("%+v", res)
	}
}

// TestSearchNoLegalMove 确认棋盘下满、但还没终局时停一手。
func TestSearchNoLegalMove(t *testing.T) {
	b := engine.New(2, 7.5)
	b.Color[0], b.Color[1] = engine.Black, engine.White
	b.Color[2], b.Color[3] = engine.White, engine.Black
	before, side, ko, passes, caps := snapshot(b)
	res := Search(b, Config{PerMove: 3, Think: time.Second, Seed: 1})
	assertUntouched(t, b, before, side, ko, passes, caps)
	if !res.Pass || res.Pt != -1 || res.Rounds != 0 || len(res.Moves) != 0 {
		t.Fatalf("%+v", res)
	}
}

// TestSearchPicksHigherWinRate 确认胜率高的点被选中，另一点的胜率是 0。
func TestSearchPicksHigherWinRate(t *testing.T) {
	cases := []struct {
		side engine.Color
		want int
	}{
		{engine.Black, 1},
		{engine.White, 0},
	}
	for _, tc := range cases {
		b := splitBoard(tc.side)
		before, side, ko, passes, caps := snapshot(b)
		res := Search(b, Config{PerMove: 4, Think: time.Second, Seed: 9})
		assertUntouched(t, b, before, side, ko, passes, caps)
		if res.Pass || res.Pt != tc.want || res.WinRate != 1 || res.Rounds != 4 {
			t.Fatalf("side %s: %+v", tc.side.Letter(), res)
		}
		if len(res.Moves) != 2 || res.Moves[0].Pt != tc.want || res.Moves[0].Wins != 4 || res.Moves[0].Games != 4 {
			t.Fatalf("side %s moves: %+v", tc.side.Letter(), res.Moves)
		}
		if res.Moves[1].Wins != 0 || res.Moves[1].Games != 4 || res.Moves[1].WinRate != 0 {
			t.Fatalf("side %s loser: %+v", tc.side.Letter(), res.Moves[1])
		}
		if res.Games != 8 {
			t.Fatalf("games=%d", res.Games)
		}
		s := engine.NewScratch(b.N)
		if _, ok := b.Clone().Play(res.Pt, s); !ok {
			t.Fatalf("chosen point %d is illegal", res.Pt)
		}
		assertUntouched(t, b, before, side, ko, passes, caps)
	}
}

// TestSearchTieKeepsEarliest 确认胜率一样时保留扫描顺序里更早的点。
// 贴 100 目，2 路棋黑棋不可能赢，所以每个点的样本完全相同。
func TestSearchTieKeepsEarliest(t *testing.T) {
	black := engine.New(2, 100)
	res := Search(black, Config{PerMove: 2, Think: time.Second, Seed: 3})
	if res.Pt != 0 || res.WinRate != 0 || len(res.Moves) != 4 {
		t.Fatalf("%+v", res)
	}
	for i, m := range res.Moves {
		if m.Pt != i || m.Wins != 0 || m.Games != 2 {
			t.Fatalf("move %d: %+v", i, m)
		}
	}
	white := engine.New(2, 100)
	white.Side = engine.White
	res = Search(white, Config{PerMove: 2, Think: time.Second, Seed: 3})
	if res.Pt != 0 || res.WinRate != 1 || len(res.Moves) != 4 {
		t.Fatalf("%+v", res)
	}
	for i, m := range res.Moves {
		if m.Pt != i || m.Wins != 2 || m.Games != 2 || m.WinRate != 1 {
			t.Fatalf("white move %d: %+v", i, m)
		}
	}
}

// TestSearchSameSeedSameResult 确认同一个种子两次搜索的选点和每手胜负都相同。
func TestSearchSameSeedSameResult(t *testing.T) {
	cfg := Config{PerMove: 2, Think: time.Second, Seed: 11}
	a := Search(engine.New(2, 7.5), cfg)
	b := Search(engine.New(2, 7.5), cfg)
	if a.Pt != b.Pt || a.Pass != b.Pass || a.Games != b.Games || a.Rounds != b.Rounds || a.WinRate != b.WinRate {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if len(a.Moves) != len(b.Moves) {
		t.Fatalf("len %d %d", len(a.Moves), len(b.Moves))
	}
	for i := range a.Moves {
		if a.Moves[i] != b.Moves[i] {
			t.Fatalf("stat %d: %+v vs %+v", i, a.Moves[i], b.Moves[i])
		}
	}
}

// TestSearchThinkStopsAfterFirstRound 确认思考时间到了以后不再开新轮，但第 0 轮会走完。
func TestSearchThinkStopsAfterFirstRound(t *testing.T) {
	res := Search(engine.New(2, 7.5), Config{PerMove: 30, Think: time.Nanosecond, Seed: 1})
	if res.Rounds != 1 || res.Pass {
		t.Fatalf("%+v", res)
	}
	if res.Games != len(res.Moves) {
		t.Fatalf("games=%d moves=%d", res.Games, len(res.Moves))
	}
	for _, m := range res.Moves {
		if m.Games != 1 || m.Wins > m.Games {
			t.Fatalf("%+v", m)
		}
	}
}

// TestSearchPerMoveAndCap 确认每手局数会跑满，并且超过 40 的请求被收成 40。
func TestSearchPerMoveAndCap(t *testing.T) {
	res := Search(splitBoard(engine.Black), Config{PerMove: 3, Think: time.Second, Seed: 2})
	if res.Rounds != 3 || res.Pt != 1 {
		t.Fatalf("%+v", res)
	}
	capped := Search(engine.New(2, 7.5), Config{PerMove: 100, Think: 5 * time.Second, Seed: 2})
	if capped.Rounds != 40 {
		t.Fatalf("rounds=%d, want 40", capped.Rounds)
	}
	for _, m := range capped.Moves {
		if m.Games != 40 {
			t.Fatalf("%+v", m)
		}
	}
}

// TestSearchDefaults 确认没填局数时按 8 局跑完。
func TestSearchDefaults(t *testing.T) {
	res := Search(engine.New(2, 7.5), Config{Seed: 4})
	if res.Rounds != 8 || res.Pass || len(res.Moves) != 4 {
		t.Fatalf("%+v", res)
	}
	for _, m := range res.Moves {
		if m.Games != 8 {
			t.Fatalf("%+v", m)
		}
	}
}

// TestSearchMovesSorted 确认返回的候选按 better 从高到低，且第一行就是落下的那手。
func TestSearchMovesSorted(t *testing.T) {
	res := Search(splitBoard(engine.Black), Config{PerMove: 3, Think: time.Second, Seed: 6})
	if len(res.Moves) == 0 || res.Moves[0].Pt != res.Pt || res.Moves[0].WinRate != res.WinRate {
		t.Fatalf("%+v", res)
	}
	for i := 1; i < len(res.Moves); i++ {
		cur, prev := res.Moves[i], res.Moves[i-1]
		if better(cur.Wins, cur.Games, prev.Wins, prev.Games) {
			t.Fatalf("not sorted: %+v before %+v", prev, cur)
		}
	}
}

// TestBetter 确认 Search 用的比较：高胜率优先，胜率相同则样本多的优先，没样本的永远不赢。
func TestBetter(t *testing.T) {
	if !better(1, 2, 1, 4) { // 1/2 > 1/4
		t.Fatal("higher rate")
	}
	if !better(2, 4, 1, 2) { // 2/4 == 1/2，样本多的优先
		t.Fatal("equal rate prefers more games")
	}
	if better(1, 2, 2, 4) {
		t.Fatal("fewer games must not win the tie")
	}
	if better(0, 0, 1, 1) || !better(1, 1, 0, 0) {
		t.Fatal("zero games")
	}
	if better(3, 5, 3, 5) {
		t.Fatal("identical is not better")
	}
}
