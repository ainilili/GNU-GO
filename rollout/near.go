package rollout

import "weiqi-step/engine"

// orthoDirs 是正交四向，顺序和坐标轴一致：东、西、南、北。
var orthoDirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// NearQueue 是上一手落下之后、下一手抽样要当成「近」的点。
// GNU Go 不是看几何上的 3×3，而是看这手改过的更新队列：
// 正交的气、只剩一两气的相关棋块的气、被提掉的子，以及这手周围还空着的 8 邻点。
// 必须在落子前调用。pt 不是空点时返回 nil。
func NearQueue(b *engine.Board, pt int, s *engine.Scratch) []int {
	if pt < 0 || pt >= b.N || b.Color[pt] != engine.Empty {
		return nil
	}
	on := make([]bool, b.N)
	out := make([]int, 0, 8)
	add := func(p int) {
		if p < 0 || p >= b.N || on[p] {
			return
		}
		on[p] = true
		out = append(out, p)
	}
	color := b.Side
	opp := engine.Opponent(color)
	captured := make([]bool, b.N)
	var ortho []int
	for _, d := range orthoDirs {
		if q := step(b, pt, d[0], d[1]); q >= 0 {
			ortho = append(ortho, q)
		}
	}
	seen := make([]bool, b.N)
	for _, nb := range ortho {
		switch b.Color[nb] {
		case engine.Empty:
			add(nb)
		case color:
			stones, libs := b.CopyGroup(nb, s, nil)
			if len(stones) == 0 || seen[stones[0]] {
				continue
			}
			for _, st := range stones {
				seen[st] = true
			}
			if libs != 2 {
				continue
			}
			lps := libertyPoints(b, stones)
			touches := false
			for _, lp := range lps {
				if lp == pt {
					touches = true
					break
				}
			}
			if !touches {
				continue
			}
			for _, lp := range lps {
				if lp != pt {
					add(lp)
				}
			}
		case opp:
			stones, libs := b.CopyGroup(nb, s, nil)
			if len(stones) == 0 || seen[stones[0]] {
				continue
			}
			for _, st := range stones {
				seen[st] = true
			}
			if libs == 1 {
				for _, st := range stones {
					captured[st] = true
					add(st)
					forAround(b, st, func(q int, orth bool) {
						if b.Color[q] == engine.Empty {
							add(q)
						}
						if !orth || b.Color[q] != color {
							return
						}
						own, nlib := b.CopyGroup(q, s, nil)
						if nlib == 1 || nlib == 2 {
							for _, lp := range libertyPoints(b, own) {
								add(lp)
							}
						}
					})
				}
				continue
			}
			left := libs - 1
			if left >= 1 && left <= 2 {
				for _, lp := range libertyPoints(b, stones) {
					if lp != pt {
						add(lp)
					}
				}
			}
		}
	}
	for _, lp := range postLiberties(b, pt, color, captured, s) {
		add(lp)
	}
	add(pt)
	forAround(b, pt, func(q int, _ bool) {
		if b.Color[q] == engine.Empty || captured[q] {
			add(q)
		}
	})
	return out
}

// postLiberties 是这手落下、提子完成之后，新棋块的气。超过两气就不必列出来，更新队列不会收。
func postLiberties(b *engine.Board, pt int, color engine.Color, captured []bool, s *engine.Scratch) []int {
	in := make([]bool, b.N)
	in[pt] = true
	for _, d := range orthoDirs {
		q := step(b, pt, d[0], d[1])
		if q < 0 || b.Color[q] != color {
			continue
		}
		stones, _ := b.CopyGroup(q, s, nil)
		for _, st := range stones {
			in[st] = true
		}
	}
	out := adjacentLibs(b, in, captured, pt)
	if len(out) > 2 {
		return nil
	}
	return out
}

func adjacentLibs(b *engine.Board, in []bool, captured []bool, pt int) []int {
	seen := make([]bool, b.N)
	var out []int
	for p := 0; p < b.N; p++ {
		if !in[p] {
			continue
		}
		for _, d := range orthoDirs {
			q := step(b, p, d[0], d[1])
			if q < 0 || q == pt || seen[q] || in[q] {
				continue
			}
			empty := b.Color[q] == engine.Empty || (captured != nil && captured[q])
			if !empty {
				continue
			}
			seen[q] = true
			out = append(out, q)
		}
	}
	return out
}

func libertyPoints(b *engine.Board, stones []int) []int {
	in := make([]bool, b.N)
	for _, p := range stones {
		in[p] = true
	}
	return adjacentLibs(b, in, nil, -1)
}

func step(b *engine.Board, pt, dx, dy int) int {
	if pt < 0 {
		return -1
	}
	x, y := b.XY(pt)
	x += dx
	y += dy
	if x < 0 || y < 0 || x >= b.Size || y >= b.Size {
		return -1
	}
	return b.Index(x, y)
}

func forAround(b *engine.Board, pt int, fn func(q int, ortho bool)) {
	x, y := b.XY(pt)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if nx < 0 || ny < 0 || nx >= b.Size || ny >= b.Size {
				continue
			}
			fn(b.Index(nx, ny), dx == 0 || dy == 0)
		}
	}
}
