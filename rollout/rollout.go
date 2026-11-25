// Package rollout 是随机终局。平坦蒙特卡洛和搜索树均匀抽点。
// UCT+RAVE 用同一套停手规则，但按棋形表的权重抽点。
package rollout

import (
	"math/rand"

	"weiqi-step/engine"
)

// Legal 列出现在能落子的点。试下一手再立刻退回。
// 已有子、劫、自杀都不在结果里。停一手不算一个候选点。
func Legal(b *engine.Board, s *engine.Scratch) []int {
	pts := make([]int, 0, 16)
	for p := 0; p < b.N; p++ {
		if b.Color[p] != engine.Empty {
			continue
		}
		u, ok := b.Play(p, s)
		if !ok {
			continue
		}
		b.Undo(u, s)
		pts = append(pts, p)
	}
	return pts
}

// Finish 从当前局面接着下，双方每次均匀随机选一个合法点，直到连续停两次，
// 或者步数达到棋盘点数的两倍。返回黑方面积减去白方面积再减贴目之后是否大于 0。
// 这个函数会改 b。调用方必须传入复制出来的棋盘。
func Finish(b *engine.Board, s *engine.Scratch, rng *rand.Rand) bool {
	limit := b.N * 2
	for b.Passes < 2 && limit > 0 {
		if _, ok := PlayOne(b, s, rng); !ok {
			break
		}
		limit--
	}
	return b.Margin(s) > 0
}

// PlayOne 下一手均匀随机的合法棋。没有合法点就停一手。
// 返回落下的点，停一手是 -1。下不动时 ok 为 false，棋盘不变。
// 先在全部交叉点里随机抽，抽到能下的就落子。每个合法点被抽中的机会相同。
// 抽满一整遍还没有，说明合法点很少，再完整列一遍，从里面均匀选。
func PlayOne(b *engine.Board, s *engine.Scratch, rng *rand.Rand) (int, bool) {
	for i := 0; i < b.N; i++ {
		p := rng.Intn(b.N)
		if b.Color[p] != engine.Empty || p == b.Ko {
			continue
		}
		if _, ok := b.Play(p, s); ok {
			return p, true
		}
	}
	pts := Legal(b, s)
	if len(pts) == 0 {
		_, ok := b.Play(-1, s)
		return -1, ok
	}
	pt := pts[rng.Intn(len(pts))]
	_, ok := b.Play(pt, s)
	return pt, ok
}
