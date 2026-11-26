package rollout

import (
	"math/bits"

	"weiqi-step/engine"
)

// stoneGrp 是一块连着的同色棋。气用位图记，落子时改位，不再把整块棋重走一遍。
type stoneGrp struct {
	color engine.Color
	size  int16
	libN  int16
	atari int16
	seed  int16
	alive bool
	bits  [2]uint64
}

// relWhite 把「黑=1、白=2」的 3×3 换成白棋视角。黑棋视角的键不用换。
var relWhite [neighKeys]uint16

func init() {
	for key := 0; key < neighKeys; key++ {
		relWhite[key] = uint16(swapColor(key))
	}
}

func swapColor(key int) int {
	out := 0
	for i := 0; i < 8; i++ {
		v := (key >> (2 * i)) & 3
		switch v {
		case 1:
			v = 2
		case 2:
			v = 1
		}
		out |= v << (2 * i)
	}
	return out
}

func relIndex(abs uint16, side engine.Color) int {
	if side == engine.White {
		return int(relWhite[abs])
	}
	return int(abs)
}

func setBit(bits *[2]uint64, p int) {
	bits[p>>6] |= 1 << (uint(p) & 63)
}

func clrBit(bits *[2]uint64, p int) {
	bits[p>>6] &^= 1 << (uint(p) & 63)
}

func hasBit(bits [2]uint64, p int) bool {
	return bits[p>>6]&(1<<(uint(p)&63)) != 0
}

func bitCount(b [2]uint64) int {
	return bits.OnesCount64(b[0]) + bits.OnesCount64(b[1])
}

func firstBit(b [2]uint64) int {
	if b[0] != 0 {
		return bits.TrailingZeros64(b[0])
	}
	return 64 + bits.TrailingZeros64(b[1])
}

func (sh *Shape) setBits(g *stoneGrp, b [2]uint64) {
	g.bits = b
	g.libN = int16(bitCount(b))
	if g.libN == 1 {
		g.atari = int16(firstBit(b))
	} else {
		g.atari = -1
	}
}

func neighborList(b *engine.Board, pt int) (out [4]int, n int) {
	size := b.Size
	x0, y0 := pt%size, pt/size
	for _, d := range ortho {
		x, y := x0+d[0], y0+d[1]
		if x < 0 || y < 0 || x >= size || y >= size {
			continue
		}
		out[n] = y*size + x
		n++
	}
	return out, n
}

func (sh *Shape) bumpVis() {
	sh.visMark++
	if sh.visMark == 0 {
		for i := range sh.vis {
			sh.vis[i] = 0
		}
		sh.visMark = 1
	}
}

// indexGroups 开局时给每块棋记一次气。之后落子只改被碰到的块。
func (sh *Shape) indexGroups(b *engine.Board, s *engine.Scratch) {
	for p := 0; p < b.N; p++ {
		if b.Color[p] == engine.Empty || sh.gid[p] >= 0 {
			continue
		}
		stones, libs := sh.floodColor(b, p)
		id := sh.newGrp()
		g := &sh.groups[id]
		g.color = b.Color[p]
		g.size = int16(len(stones))
		g.seed = int16(stones[0])
		g.alive = true
		sh.setBits(g, libs)
		for _, st := range stones {
			sh.gid[st] = id
		}
	}
	sh.ready = true
}

func (sh *Shape) newGrp() int16 {
	if n := len(sh.free); n > 0 {
		id := sh.free[n-1]
		sh.free = sh.free[:n-1]
		sh.groups[id] = stoneGrp{alive: true, atari: -1}
		return id
	}
	sh.groups = append(sh.groups, stoneGrp{alive: true, atari: -1})
	return int16(len(sh.groups) - 1)
}

func (sh *Shape) killGrp(id int16) {
	sh.groups[id] = stoneGrp{atari: -1}
	sh.free = append(sh.free, id)
}

func (sh *Shape) floodColor(b *engine.Board, start int) ([]int, [2]uint64) {
	color := b.Color[start]
	var libs [2]uint64
	sh.bumpVis()
	sh.buf = sh.buf[:0]
	sh.stack = sh.stack[:0]
	sh.stack = append(sh.stack, start)
	sh.vis[start] = sh.visMark
	for len(sh.stack) > 0 {
		last := len(sh.stack) - 1
		p := sh.stack[last]
		sh.stack = sh.stack[:last]
		sh.buf = append(sh.buf, p)
		nbs, nn := neighborList(b, p)
		for i := 0; i < nn; i++ {
			nb := nbs[i]
			if sh.vis[nb] == sh.visMark {
				continue
			}
			switch b.Color[nb] {
			case color:
				sh.vis[nb] = sh.visMark
				sh.stack = append(sh.stack, nb)
			case engine.Empty:
				setBit(&libs, nb)
			}
		}
	}
	return sh.buf, libs
}

func (sh *Shape) floodGID(start int, id int16, b *engine.Board) []int {
	sh.bumpVis()
	sh.buf = sh.buf[:0]
	sh.stack = sh.stack[:0]
	sh.stack = append(sh.stack, start)
	sh.vis[start] = sh.visMark
	for len(sh.stack) > 0 {
		last := len(sh.stack) - 1
		p := sh.stack[last]
		sh.stack = sh.stack[:last]
		sh.buf = append(sh.buf, p)
		nbs, nn := neighborList(b, p)
		for i := 0; i < nn; i++ {
			nb := nbs[i]
			if sh.gid[nb] != id || sh.vis[nb] == sh.visMark {
				continue
			}
			sh.vis[nb] = sh.visMark
			sh.stack = append(sh.stack, nb)
		}
	}
	return sh.buf
}

func (sh *Shape) markLibs(libs [2]uint64) {
	for i := 0; i < 2; i++ {
		w := libs[i]
		for w != 0 {
			bit := bits.TrailingZeros64(w)
			sh.add(i*64 + bit)
			w &= w - 1
		}
	}
}

func inIDs(id int16, ids []int16) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// atariCount 用棋块账数下在 pt 能提 color 几颗子。
func (sh *Shape) atariCount(b *engine.Board, pt int, color engine.Color, s *engine.Scratch) int {
	if !sh.ready {
		return b.AtariCapture(pt, color, s)
	}
	total := 0
	var seen [4]int16
	nseen := 0
	nbs, nn := neighborList(b, pt)
	for i := 0; i < nn; i++ {
		nb := nbs[i]
		if b.Color[nb] != color {
			continue
		}
		id := sh.gid[nb]
		if id < 0 || int(id) >= len(sh.groups) || !sh.groups[id].alive {
			return b.AtariCapture(pt, color, s)
		}
		g := &sh.groups[id]
		if g.libN != 1 || int(g.atari) != pt {
			continue
		}
		dup := false
		for k := 0; k < nseen; k++ {
			if seen[k] == id {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		seen[nseen] = id
		nseen++
		total += int(g.size)
	}
	return total
}

// fastSafe 用棋块的气判断这手合不合法、安不安全。算不出才真的落子再退回。
func (sh *Shape) fastSafe(b *engine.Board, pt int, s *engine.Scratch, ocap int) (legal, safe bool) {
	if !sh.ready {
		return moveSafe(b, pt, s, ocap)
	}
	libs, ok := sh.hypotLibs(b, pt)
	if !ok {
		return moveSafe(b, pt, s, ocap)
	}
	if libs <= 0 {
		return false, false
	}
	return true, libs > 1
}

// hypotLibs 算落下 pt 之后自己有几口气。不改棋盘，也不改棋块账。
// ok 为 false 表示账对不上，调用方改走真的落子。
func (sh *Shape) hypotLibs(b *engine.Board, pt int) (int, bool) {
	color := b.Side
	opp := engine.Opponent(color)
	nbs, nn := neighborList(b, pt)
	var own [4]int16
	var cap [4]int16
	nOwn, nCap := 0, 0
	var bits [2]uint64
	for i := 0; i < nn; i++ {
		nb := nbs[i]
		switch b.Color[nb] {
		case engine.Empty:
			setBit(&bits, nb)
		case color:
			id := sh.gid[nb]
			if id < 0 || !sh.groups[id].alive {
				return 0, false
			}
			if inIDs(id, own[:nOwn]) {
				continue
			}
			own[nOwn] = id
			nOwn++
			bits[0] |= sh.groups[id].bits[0]
			bits[1] |= sh.groups[id].bits[1]
		case opp:
			id := sh.gid[nb]
			if id < 0 || !sh.groups[id].alive {
				return 0, false
			}
			g := &sh.groups[id]
			if g.libN == 1 && int(g.atari) == pt && !inIDs(id, cap[:nCap]) {
				cap[nCap] = id
				nCap++
			}
		}
	}
	clrBit(&bits, pt)
	for i := 0; i < nCap; i++ {
		stones := sh.floodGID(int(sh.groups[cap[i]].seed), cap[i], b)
		for _, p := range stones {
			if sh.touches(b, p, pt, own[:nOwn]) {
				setBit(&bits, p)
			}
		}
	}
	return bitCount(bits), true
}

func (sh *Shape) touches(b *engine.Board, p, pt int, own []int16) bool {
	nbs, nn := neighborList(b, p)
	for i := 0; i < nn; i++ {
		nb := nbs[i]
		if nb == pt {
			return true
		}
		id := sh.gid[nb]
		if id >= 0 && inIDs(id, own) && b.Color[nb] == b.Side {
			return true
		}
	}
	return false
}

// follow 按「pt 已经由当前行棋方落下」改棋块账。棋盘颜色还是落子前的。
func (sh *Shape) follow(b *engine.Board, pt int) {
	color := b.Side
	opp := engine.Opponent(color)
	nbs, nn := neighborList(b, pt)
	sh.nOwn = 0
	sh.nCap = 0
	var fresh [2]uint64
	for i := 0; i < nn; i++ {
		nb := nbs[i]
		switch b.Color[nb] {
		case engine.Empty:
			setBit(&fresh, nb)
		case color:
			id := sh.gid[nb]
			if id < 0 || inIDs(id, sh.ownID[:sh.nOwn]) {
				continue
			}
			sh.ownID[sh.nOwn] = id
			sh.nOwn++
			sh.markLibs(sh.groups[id].bits)
			fresh[0] |= sh.groups[id].bits[0]
			fresh[1] |= sh.groups[id].bits[1]
		case opp:
			id := sh.gid[nb]
			if id < 0 {
				continue
			}
			g := &sh.groups[id]
			if g.libN == 1 && int(g.atari) == pt {
				if !inIDs(id, sh.capID[:sh.nCap]) {
					sh.capID[sh.nCap] = id
					sh.nCap++
					sh.markLibs(g.bits)
				}
				continue
			}
		}
	}
	clrBit(&fresh, pt)
	sh.caps = sh.caps[:0]
	for i := 0; i < sh.nCap; i++ {
		id := sh.capID[i]
		stones := sh.floodGID(int(sh.groups[id].seed), id, b)
		sh.caps = append(sh.caps, stones...)
	}
	for _, p := range sh.caps {
		if sh.touches(b, p, pt, sh.ownID[:sh.nOwn]) {
			setBit(&fresh, p)
		}
	}
	// 没被提掉、但挨着这一手的对方棋，少了 pt 这口气。
	var seenOpp [4]int16
	nOpp := 0
	for i := 0; i < nn; i++ {
		nb := nbs[i]
		if b.Color[nb] != opp {
			continue
		}
		id := sh.gid[nb]
		if id < 0 || inIDs(id, sh.capID[:sh.nCap]) || inIDs(id, seenOpp[:nOpp]) {
			continue
		}
		seenOpp[nOpp] = id
		nOpp++
		sh.markLibs(sh.groups[id].bits)
		sh.delLib(id, pt)
		sh.markLibs(sh.groups[id].bits)
	}
	// 提子旁边、又不属于新连上的这块的棋，多了这些空点当气。
	for _, p := range sh.caps {
		around, an := neighborList(b, p)
		for i := 0; i < an; i++ {
			nb := around[i]
			if nb == pt || b.Color[nb] == engine.Empty {
				continue
			}
			id := sh.gid[nb]
			if id < 0 || inIDs(id, sh.ownID[:sh.nOwn]) || inIDs(id, sh.capID[:sh.nCap]) {
				continue
			}
			sh.markLibs(sh.groups[id].bits)
			sh.addLib(id, p)
			sh.markLibs(sh.groups[id].bits)
		}
	}
	for _, p := range sh.caps {
		sh.gid[p] = -1
	}
	for i := 0; i < sh.nCap; i++ {
		sh.killGrp(sh.capID[i])
	}
	if sh.nOwn == 0 {
		id := sh.newGrp()
		g := &sh.groups[id]
		g.color = color
		g.size = 1
		g.seed = int16(pt)
		g.alive = true
		sh.setBits(g, fresh)
		sh.gid[pt] = id
		sh.markLibs(fresh)
		return
	}
	survivor := sh.ownID[0]
	size := int16(1)
	for i := 0; i < sh.nOwn; i++ {
		size += sh.groups[sh.ownID[i]].size
	}
	for i := 1; i < sh.nOwn; i++ {
		id := sh.ownID[i]
		stones := sh.floodGID(int(sh.groups[id].seed), id, b)
		for _, p := range stones {
			sh.gid[p] = survivor
		}
		sh.killGrp(id)
	}
	g := &sh.groups[survivor]
	g.color = color
	g.size = size
	g.alive = true
	sh.setBits(g, fresh)
	sh.gid[pt] = survivor
	sh.markLibs(fresh)
}

func (sh *Shape) delLib(id int16, p int) {
	g := &sh.groups[id]
	if !hasBit(g.bits, p) {
		return
	}
	clrBit(&g.bits, p)
	g.libN--
	if g.libN == 1 {
		g.atari = int16(firstBit(g.bits))
	} else {
		g.atari = -1
	}
}

func (sh *Shape) addLib(id int16, p int) {
	g := &sh.groups[id]
	if hasBit(g.bits, p) {
		return
	}
	setBit(&g.bits, p)
	g.libN++
	if g.libN == 1 {
		g.atari = int16(p)
	} else {
		g.atari = -1
	}
}

// writeKey 把 pt 周围 8 个点压成键。颜色是黑白，不是行棋方视角。
func (sh *Shape) writeKey(b *engine.Board, pt int) {
	size := b.Size
	x0, y0 := pt%size, pt/size
	key := 0
	shift := 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			x, y := x0+dx, y0+dy
			v := 3
			if x >= 0 && y >= 0 && x < size && y < size {
				switch b.Color[y*size+x] {
				case engine.Empty:
					v = 0
				case engine.Black:
					v = 1
				case engine.White:
					v = 2
				}
			}
			key |= v << shift
			shift += 2
		}
	}
	sh.absKey[pt] = uint16(key)
}

// rewriteArea 落子或提子后，重写周围 3×3 的键，并把这些点列入重算。
func (sh *Shape) rewriteArea(b *engine.Board, p int) {
	size := b.Size
	x0, y0 := p%size, p/size
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			x, y := x0+dx, y0+dy
			if x < 0 || y < 0 || x >= size || y >= size {
				continue
			}
			q := y*size + x
			sh.writeKey(b, q)
			sh.add(q)
		}
	}
}
