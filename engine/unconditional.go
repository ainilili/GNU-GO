package engine

// Unconditional 标出无条件活棋和无条件地域。
// 返回值和棋盘一样长。Empty 表示还没定。黑或白表示这个点在搜索里算那一方：
// 树上不准再往这个空点长，终局胜负也不看后来下成什么样，直接算给这一方。
// 这是 GNU Go 在 uct_genmove 之前做的那一步。布局库和经典引擎的着手价值不在这里。
func Unconditional(b *Board) []Color {
	out := make([]Color, b.N)
	markUnconditional(b, Black, out)
	markUnconditional(b, White, out)
	return out
}

func markUnconditional(src *Board, color Color, out []Color) {
	b := src.Clone()
	s := NewScratch(b.N)
	l := &life{b: b, s: s}
	other := Opponent(color)
	potential := l.isolatedPairs(color)
	none := l.captureNonInvincible(color, potential)
	if none {
		l.undoAll()
		return
	}
	for _, origin := range l.origins(color) {
		if potential[origin] {
			continue
		}
		for _, lp := range l.liberties(origin) {
			l.play(lp, other, false)
		}
	}
	for {
		found := false
		for _, origin := range l.origins(other) {
			lps := l.liberties(origin)
			if len(lps) != 1 {
				continue
			}
			if l.play(lps[0], other, false) {
				found = true
			}
		}
		if !found {
			break
		}
	}
	l.extendSeki(color, potential)
	for p := 0; p < b.N; p++ {
		if !potential[p] || b.Color[p] == Empty {
			continue
		}
		for _, lp := range l.liberties(p) {
			l.play(lp, other, false)
		}
	}
	for _, origin := range l.origins(other) {
		lps := l.liberties(origin)
		if len(lps) != 2 || manhattan(b, lps[0], lps[1]) != 1 {
			continue
		}
		a, c := lps[0], lps[1]
		aOpen := l.cappedLibs(a, color)
		cOpen := l.cappedLibs(c, color)
		aLib := l.cappedLibs(a, other)
		cLib := l.cappedLibs(c, other)
		pick := c
		if aOpen > cOpen || (aOpen == cOpen && aLib >= cLib) {
			pick = a
		}
		l.play(pick, other, false)
	}
	for p := 0; p < b.N; p++ {
		if out[p] != Empty {
			continue
		}
		if b.Color[p] == color && !potential[p] {
			out[p] = color
			if l.originOf(p) == p {
				for _, lp := range l.liberties(p) {
					if out[lp] == Empty {
						out[lp] = color
					}
				}
			}
			continue
		}
		if b.Color[p] == other {
			lps := l.liberties(p)
			if len(lps) != 1 {
				continue
			}
			out[p] = color
			if out[lps[0]] == Empty {
				out[lps[0]] = color
			}
		}
	}
	l.undoAll()
}

type life struct {
	b     *Board
	s     *Scratch
	stack []Undo
}

func (l *life) play(pt int, color Color, koOK bool) bool {
	if pt < 0 {
		return false
	}
	prevSide := l.b.Side
	prevKo := l.b.Ko
	l.b.Side = color
	if koOK && pt == l.b.Ko {
		l.b.Ko = -1
	}
	u, ok := l.b.Play(pt, l.s)
	if !ok {
		l.b.Side = prevSide
		l.b.Ko = prevKo
		return false
	}
	u.Side = prevSide
	u.Ko = prevKo
	l.stack = append(l.stack, u)
	return true
}

func (l *life) undo(n int) {
	for n > 0 && len(l.stack) > 0 {
		u := l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
		l.b.Undo(u, l.s)
		n--
	}
}

func (l *life) undoAll() { l.undo(len(l.stack)) }

func (l *life) stones(start int) []int {
	if start < 0 || start >= l.b.N || l.b.Color[start] == Empty {
		return nil
	}
	g, _ := l.b.CopyGroup(start, l.s, nil)
	return g
}

func (l *life) originOf(p int) int {
	stones := l.stones(p)
	if len(stones) == 0 {
		return -1
	}
	m := stones[0]
	for _, st := range stones[1:] {
		if st < m {
			m = st
		}
	}
	return m
}

func (l *life) origins(color Color) []int {
	seen := make([]bool, l.b.N)
	var out []int
	for p := 0; p < l.b.N; p++ {
		if l.b.Color[p] != color || seen[p] {
			continue
		}
		stones := l.stones(p)
		for _, st := range stones {
			seen[st] = true
		}
		if len(stones) == 0 {
			continue
		}
		m := stones[0]
		for _, st := range stones[1:] {
			if st < m {
				m = st
			}
		}
		out = append(out, m)
	}
	return out
}

func (l *life) liberties(start int) []int {
	stones := l.stones(start)
	if len(stones) == 0 {
		return nil
	}
	seen := make([]bool, l.b.N)
	var out []int
	for _, p := range stones {
		l.b.forNeighbors(p, func(nb int) {
			if l.b.Color[nb] != Empty || seen[nb] {
				return
			}
			seen[nb] = true
			out = append(out, nb)
		})
	}
	return out
}

func (l *life) isolatedPairs(color Color) []bool {
	pot := make([]bool, l.b.N)
	seen := make([]bool, l.b.N)
	for p := 0; p < l.b.N; p++ {
		if l.b.Color[p] != color || seen[p] {
			continue
		}
		stones := l.stones(p)
		for _, st := range stones {
			seen[st] = true
		}
		if len(stones) != 2 {
			continue
		}
		in := [2]int{stones[0], stones[1]}
		isolated := true
		for _, st := range in {
			x, y := l.b.XY(st)
			for dy := -1; dy <= 1 && isolated; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= l.b.Size || ny >= l.b.Size {
						isolated = false
						break
					}
					q := l.b.Index(nx, ny)
					if l.b.Color[q] == color && q != in[0] && q != in[1] {
						isolated = false
						break
					}
				}
			}
		}
		if isolated {
			pot[in[0]] = true
			pot[in[1]] = true
		}
	}
	return pot
}

func (l *life) captureNonInvincible(color Color, except []bool) bool {
	other := Opponent(color)
	for {
		progress := false
		found := false
		for _, origin := range l.origins(color) {
			if except[origin] {
				continue
			}
			found = true
			lps := l.liberties(origin)
			save := len(l.stack)
			for _, lp := range lps {
				l.play(lp, other, false)
			}
			if l.b.Color[origin] == Empty {
				progress = true
				continue
			}
			left := l.liberties(origin)
			if len(left) == 1 && l.play(left[0], other, true) && l.b.Color[origin] == Empty {
				progress = true
				continue
			}
			l.undo(len(l.stack) - save)
		}
		if !found {
			return true
		}
		if !progress {
			return false
		}
	}
}

func (l *life) extendSeki(color Color, potential []bool) {
	other := Opponent(color)
	dirs := [4][2]int{{0, 1}, {-1, 0}, {0, -1}, {1, 0}}
	for p := 0; p < l.b.N; p++ {
		if !potential[p] || l.b.Color[p] == Empty || l.originOf(p) != p {
			continue
		}
		grown := false
		for r := 0; r < 4 && !grown; r++ {
			up := dirs[r]
			right := dirs[(r+1)%4]
			if !l.match(p, up, color) || !l.empty(p, add2(up, up)) || !l.empty(p, neg(up)) {
				continue
			}
			for k := 0; k < 2; k++ {
				rt := right
				if k == 1 {
					rt = neg(rt)
				}
				if !l.empty(p, rt) || !l.empty(p, add2(up, neg(rt))) {
					continue
				}
				save := len(l.stack)
				if l.empty(p, neg(rt)) {
					l.play(l.shift(p, neg(rt)), other, false)
				}
				if l.empty(p, add2(up, rt)) {
					l.play(l.shift(p, add2(up, rt)), other, false)
				}
				left := l.shift(p, neg(rt))
				rightPt := l.shift(p, add2(up, rt))
				if left >= 0 && rightPt >= 0 && l.b.Color[left] == other && l.b.Color[rightPt] == other && l.originOf(left) == l.originOf(rightPt) {
					l.undo(len(l.stack) - save)
					if l.play(l.shift(p, neg(up)), color, false) {
						grown = true
					}
					break
				}
				l.undo(len(l.stack) - save)
			}
		}
		if grown && len(l.stones(p)) > 2 {
			break
		}
	}
}

func (l *life) cappedLibs(pt int, color Color) int {
	if !l.play(pt, color, false) {
		return 0
	}
	n := l.b.Liberties(pt, l.s)
	l.undo(1)
	if n > 4 {
		return 4
	}
	return n
}

func (l *life) match(p int, d [2]int, color Color) bool {
	q := l.shift(p, d)
	return q >= 0 && l.b.Color[q] == color
}

func (l *life) empty(p int, d [2]int) bool {
	q := l.shift(p, d)
	return q >= 0 && l.b.Color[q] == Empty
}

func (l *life) shift(p int, d [2]int) int {
	if p < 0 {
		return -1
	}
	x, y := l.b.XY(p)
	x += d[0]
	y += d[1]
	if x < 0 || y < 0 || x >= l.b.Size || y >= l.b.Size {
		return -1
	}
	return l.b.Index(x, y)
}

func neg(d [2]int) [2]int { return [2]int{-d[0], -d[1]} }

func add2(a, b [2]int) [2]int { return [2]int{a[0] + b[0], a[1] + b[1]} }

func manhattan(b *Board, a, c int) int {
	ax, ay := b.XY(a)
	cx, cy := b.XY(c)
	dx := ax - cx
	dy := ay - cy
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}
