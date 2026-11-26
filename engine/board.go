// Package engine 实现这一步用的围棋规则：禁止自杀、简单劫、中国规则数子。
// 搜索不在这里。这里只回答「这手下得下吗」和「终局谁领先」。
package engine

import "strings"

// Color 是一个交叉点上的状态，或行棋的一方。
type Color int8

const (
	Empty Color = 0 // 空点
	Black Color = 1 // 黑
	White Color = 2 // 白
)

// Opponent 返回对方的颜色。Empty 也会落到 Black，调用方只应对黑白使用。
func Opponent(c Color) Color {
	if c == Black {
		return White
	}
	return Black
}

// Letter 返回接口里用的颜色字母：黑 "B"，白 "W"，空点是空字符串。
func (c Color) Letter() string {
	switch c {
	case Black:
		return "B"
	case White:
		return "W"
	default:
		return ""
	}
}

// Board 是一盘正方形棋的当前局面。
// 邻接点表在 New 里建好后不再改。Clone 只复制棋子，两盘棋共用同一张邻接表。
type Board struct {
	Size     int     // 边长，第 1 步固定为 9
	N        int     // 交叉点总数，等于 Size*Size
	Komi     float64 // 白方贴目。终局从黑方面积里减掉
	Color    []Color // 每个点的颜色，下标是 y*Size+x
	Side     Color   // 现在该谁下
	Ko       int     // 劫争禁着点。没有劫时是 -1。只禁刚刚提掉的那一个点
	Passes   int     // 连续停手的次数。落子后清零
	Captures [3]int  // 按下标 Color 累计提子数。0 不用
	neigh    []int32 // 每个点最多 4 个正交邻点，空位填 -1。布局是点号*4
	neighN   []int8  // 每个点实际有几个邻点。角 2，边 3，中间 4
}

// New 创建空棋盘，黑先。边长小于 2 按 2，大于 19 按 19。
// 邻接表在这里一次填完，之后 Play 不再改它。
func New(size int, komi float64) *Board {
	if size < 2 {
		size = 2
	}
	if size > 19 {
		size = 19
	}
	n := size * size
	b := &Board{
		Size:   size,
		N:      n,
		Komi:   komi,
		Color:  make([]Color, n),
		Side:   Black,
		Ko:     -1,
		neigh:  make([]int32, n*4),
		neighN: make([]int8, n),
	}
	for i := range b.neigh {
		b.neigh[i] = -1
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := y*size + x
			k := 0
			// add 把仍在棋盘内的正交邻点写入 neigh，并让这个点的邻居计数加一。
			add := func(nx, ny int) {
				if nx >= 0 && ny >= 0 && nx < size && ny < size {
					b.neigh[p*4+k] = int32(ny*size + nx)
					k++
				}
			}
			add(x-1, y)
			add(x+1, y)
			add(x, y-1)
			add(x, y+1)
			b.neighN[p] = int8(k)
		}
	}
	return b
}

// Clone 复制局面，供一盘随机棋使用。邻接表不复制，两盘棋一起只读它。
func (b *Board) Clone() *Board {
	c := *b
	c.Color = append([]Color(nil), b.Color...)
	return &c
}

// CloneInto 复制到已有的棋盘上，颜色切片够长就接着用。邻接表仍然共用。
func (b *Board) CloneInto(dst *Board) *Board {
	if dst == nil || cap(dst.Color) < b.N {
		return b.Clone()
	}
	colors := dst.Color[:b.N]
	*dst = *b
	copy(colors, b.Color)
	dst.Color = colors
	return dst
}

// Index 把界面坐标换成 Color 切片的下标。x 从左往右，y 从棋盘上方往下，都从 0 开始。
func (b *Board) Index(x, y int) int {
	return y*b.Size + x
}

// XY 是 Index 的逆运算。
func (b *Board) XY(pt int) (x, y int) {
	return pt % b.Size, pt / b.Size
}

// PassPt 返回「停一手」用的点号，比最后一个交叉点大 1。Play 本身用负数表示停手。
func (b *Board) PassPt() int { return b.N }

// Over 判断对局是否结束。连续停两次就结束，和真实对局一致。
func (b *Board) Over() bool { return b.Passes >= 2 }

// Scratch 是一块可复用的临时内存。气、提子、数子都往这里写，避免每一步重新分配。
// 同一块 Scratch 不能同时服务两盘正在修改的棋。一轮模拟开始前要 ClearSaved。
type Scratch struct {
	mark      []uint32 // 这块棋的连通块已经走过的标记，和 stamp 成对使用
	seen      []uint32 // 另一套标记，给数子和打吃扫描用，避免和 mark 互相冲掉
	stamp     uint32   // mark 的当前代次。换一块棋就加 1，不用把 mark 整个清零
	seenStamp uint32   // seen 的当前代次
	stack     []int32  // 洪水填充用的栈
	group     []int32  // 最近一次算出的整块棋。下次 groupInfo 会覆盖它
	saved     []int32  // 这次落子提掉的点，Undo 靠它把子放回去
	cands     []int32  // AtariLibs 的结果缓冲
	region    []int32  // Areas 正在数的那片空点
}

// NewScratch 按交叉点数量分配临时内存。n 必须和棋盘的 N 相同。
func NewScratch(n int) *Scratch {
	return &Scratch{
		mark:   make([]uint32, n),
		seen:   make([]uint32, n),
		stack:  make([]int32, 0, n),
		group:  make([]int32, 0, 32),
		saved:  make([]int32, 0, 32),
		cands:  make([]int32, 0, 16),
		region: make([]int32, 0, n),
	}
}

// ClearSaved 丢掉上一盘模拟留下的提子记录。不调用的话，下一次 Play 会把旧提子算进自己的撤销区间。
func (s *Scratch) ClearSaved() { s.saved = s.saved[:0] }

// Captured 返回这一手提掉的点。切片指向 Scratch 内部，下次落子或退子会变。
func (s *Scratch) Captured(u Undo) []int32 {
	if u.CapStart < 0 || u.CapEnd > len(s.saved) || u.CapStart > u.CapEnd {
		return nil
	}
	return s.saved[u.CapStart:u.CapEnd]
}

// Liberties 返回 pt 所在整块棋的气数。pt 不是棋子时返回 0。
func (b *Board) Liberties(pt int, s *Scratch) int {
	if pt < 0 || pt >= b.N || b.Color[pt] == Empty {
		return 0
	}
	_, libs := b.groupInfo(pt, s)
	return libs
}

// nextStamp 取下一代 mark。uint32 绕回 0 时，旧标记会和新时代撞上，所以把整表清掉再从 1 开始。
func (s *Scratch) nextStamp() uint32 {
	s.stamp++
	if s.stamp == 0 {
		for i := range s.mark {
			s.mark[i] = 0
		}
		s.stamp = 1
	}
	return s.stamp
}

// nextSeen 和 nextStamp 同一套办法，管的是 seen 表。
func (s *Scratch) nextSeen() uint32 {
	s.seenStamp++
	if s.seenStamp == 0 {
		for i := range s.seen {
			s.seen[i] = 0
		}
		s.seenStamp = 1
	}
	return s.seenStamp
}

// forNeighbors 对 p 的每个正交邻点调用 fn。角和边上的点没有四个邻居。
func (b *Board) forNeighbors(p int, fn func(int)) {
	base := p * 4
	n := int(b.neighN[p])
	for i := 0; i < n; i++ {
		fn(int(b.neigh[base+i]))
	}
}

// groupInfo 从 start 这颗子出发，找出整块相连的同色棋，并数气。
// 返回的 group 指向 Scratch 内部，下一次调用会覆盖，调用方不能留着以后用。
// 空点一旦被当成气数过，就打上标记，同一口气不会数两次。
func (b *Board) groupInfo(start int, s *Scratch) (group []int32, libs int) {
	st := s.nextStamp()
	color := b.Color[start]
	s.stack = s.stack[:0]
	s.group = s.group[:0]
	s.stack = append(s.stack, int32(start))
	s.mark[start] = st
	for len(s.stack) > 0 {
		last := len(s.stack) - 1
		p := int(s.stack[last])
		s.stack = s.stack[:last]
		s.group = append(s.group, int32(p))
		base := p * 4
		n := int(b.neighN[p])
		for i := 0; i < n; i++ {
			nb := int(b.neigh[base+i])
			if s.mark[nb] == st {
				continue
			}
			switch b.Color[nb] {
			case Empty:
				s.mark[nb] = st
				libs++
			default:
				if b.Color[nb] == color {
					s.mark[nb] = st
					s.stack = append(s.stack, int32(nb))
				}
			}
		}
	}
	return s.group, libs
}

// CopyGroup 把 start 所在的整块棋抄进 dst，并返回气数。start 不是子时返回空。
// groupInfo 的结果放在 Scratch 里，下一次调用会盖掉，所以要抄出来才能留着用。
func (b *Board) CopyGroup(start int, s *Scratch, dst []int) ([]int, int) {
	if start < 0 || start >= b.N || b.Color[start] == Empty {
		return dst[:0], 0
	}
	grp, libs := b.groupInfo(start, s)
	dst = dst[:0]
	for _, g := range grp {
		dst = append(dst, int(g))
	}
	return dst, libs
}

// Undo 记录一次成功落子之前的局面，供 Board.Undo 退回。
// Play 失败时返回的是空 Undo，不能拿去调用 Board.Undo。
type Undo struct {
	Pt       int    // 落下的点。负数表示停一手
	Color    Color  // 下这手的一方
	Ko       int    // 落子前的劫点
	Passes   int    // 落子前的连续停手次数
	Side     Color  // 落子前该谁下
	Caps     [3]int // 落子前的累计提子
	CapStart int    // 这手提掉的子在 Scratch.saved 里的起点
	CapEnd   int    // 这手提掉的子在 Scratch.saved 里的终点，不含
}

// Play 让当前行棋方走一步。pt 小于 0 是停一手。
// 成功时棋盘已更新，返回的 Undo 可以退回。失败时棋盘保持原样，返回 false。
// 失败的情况：点号越界、点上已有子、这个点是当前劫、或者落子后自己没气又没提到对方的子。
func (b *Board) Play(pt int, s *Scratch) (Undo, bool) {
	u := Undo{
		Pt:       pt,
		Color:    b.Side,
		Ko:       b.Ko,
		Passes:   b.Passes,
		Side:     b.Side,
		Caps:     b.Captures,
		CapStart: len(s.saved),
		CapEnd:   len(s.saved),
	}
	if pt < 0 {
		// 停一手清掉劫，连续停手加一。不提子。
		b.Ko = -1
		b.Passes++
		b.Side = Opponent(b.Side)
		return u, true
	}
	if pt >= b.N || b.Color[pt] != Empty || pt == b.Ko {
		return Undo{}, false
	}
	color := b.Side
	opp := Opponent(color)
	b.Color[pt] = color

	// 先提掉邻接的、下完这手就没有气的对方整块棋。
	base := pt * 4
	nn := int(b.neighN[pt])
	for i := 0; i < nn; i++ {
		nb := int(b.neigh[base+i])
		if b.Color[nb] != opp {
			continue
		}
		grp, libs := b.groupInfo(nb, s)
		if libs > 0 {
			continue
		}
		for _, gp := range grp {
			p := int(gp)
			b.Color[p] = Empty
			s.saved = append(s.saved, int32(p))
		}
	}
	u.CapEnd = len(s.saved)
	captured := u.CapEnd - u.CapStart
	_, ownLibs := b.groupInfo(pt, s)
	// 没提到对方、自己也没气，就是自杀。把刚放下的子拿掉，棋盘回到调用前。
	if captured == 0 && ownLibs == 0 {
		b.Color[pt] = Empty
		for i := u.CapStart; i < u.CapEnd; i++ {
			b.Color[s.saved[i]] = opp
		}
		s.saved = s.saved[:u.CapStart]
		return Undo{}, false
	}
	b.Captures[color] += captured
	b.Passes = 0
	b.Ko = -1
	// 只提一子，而且自己下完只剩一口气：对方下一步不能立刻填回这个点，这就是简单劫。
	if captured == 1 && ownLibs == 1 {
		b.Ko = int(s.saved[u.CapStart])
	}
	b.Side = opp
	return u, true
}

// Undo 退回一次成功的 Play。失败的 Play 不能退，它自己已经把棋盘恢复了。
// 只把这手的提子放回去。saved 末尾正好是这手的记录时才缩掉，避免误删更早的记录。
func (b *Board) Undo(u Undo, s *Scratch) {
	if u.Pt >= 0 {
		b.Color[u.Pt] = Empty
		opp := Opponent(u.Color)
		for i := u.CapStart; i < u.CapEnd; i++ {
			b.Color[s.saved[i]] = opp
		}
		if u.CapEnd == len(s.saved) {
			s.saved = s.saved[:u.CapStart]
		}
	}
	b.Ko = u.Ko
	b.Passes = u.Passes
	b.Side = u.Side
	b.Captures = u.Caps
}

// ProperSmallEye 判断 pt 对 color 是不是「小真眼」。第 1 步的搜索不用它，规则留在这里给后面的步骤。
// 四个正交邻点必须都是己方或棋盘外。斜角按棋盘外加 1、对方子加 2、空点或己方不加，合计不超过 3 才算眼。
func (b *Board) ProperSmallEye(pt int, color Color) bool {
	if pt < 0 || pt >= b.N || b.Color[pt] != Empty {
		return false
	}
	base := pt * 4
	n := int(b.neighN[pt])
	for i := 0; i < n; i++ {
		nb := b.Color[b.neigh[base+i]]
		if nb == Empty || nb == Opponent(color) {
			return false
		}
	}
	size := b.Size
	x := pt % size
	y := pt / size
	diag := 0
	for _, d := range [4][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= size || ny >= size {
			diag++
			continue
		}
		if b.Color[ny*size+nx] == Opponent(color) {
			diag += 2
		}
	}
	return diag <= 3
}

// IsEye 判断 pt 是不是 color 的单点眼。正交邻点必须全是己方。
// 斜角在棋盘上的个数决定门槛：角上 1 个斜角要己方，边上 2 个都要己方，中间至少 3 个己方。
// 第 1 步不调用。它和 ProperSmallEye 不是同一条规则。
func (b *Board) IsEye(pt int, color Color) bool {
	if pt < 0 || pt >= b.N || b.Color[pt] != Empty {
		return false
	}
	base := pt * 4
	n := int(b.neighN[pt])
	for i := 0; i < n; i++ {
		if b.Color[b.neigh[base+i]] != color {
			return false
		}
	}
	size := b.Size
	x := pt % size
	y := pt / size
	own := 0
	diag := 0
	for _, d := range [4][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= size || ny >= size {
			continue
		}
		diag++
		if b.Color[ny*size+nx] == color {
			own++
		}
	}
	switch diag {
	case 1:
		return own >= 1
	case 2:
		return own >= 2
	default:
		return own >= 3
	}
}

// EmptyNeighbors 数 pt 的正交空邻点。第 1 步不调用，留给后面判断一手是不是安全。
func (b *Board) EmptyNeighbors(pt int) int {
	if pt < 0 || pt >= b.N {
		return 0
	}
	n := 0
	base := pt * 4
	nn := int(b.neighN[pt])
	for i := 0; i < nn; i++ {
		if b.Color[b.neigh[base+i]] == Empty {
			n++
		}
	}
	return n
}

// AtariCapture 数 color 有多少子正处于打吃，且唯一的气就是 pt。
// 邻接的同一块棋只数一次。返回的是子数，不是块数。第 1 步的随机终局不看这个。
func (b *Board) AtariCapture(pt int, color Color, s *Scratch) int {
	if pt < 0 || pt >= b.N || b.Color[pt] != Empty {
		return 0
	}
	st := s.nextSeen()
	total := 0
	base := pt * 4
	nn := int(b.neighN[pt])
	for i := 0; i < nn; i++ {
		nb := int(b.neigh[base+i])
		if b.Color[nb] != color || s.seen[nb] == st {
			continue
		}
		grp, libs := b.groupInfo(nb, s)
		sz := len(grp)
		for _, g := range grp {
			s.seen[int(g)] = st
		}
		// 这块棋挨着空点 pt。气数是 1 时，那一口气只能是 pt，下在这里就能提。
		if libs == 1 {
			total += sz
		}
	}
	return total
}

// AtariLibs 收集 groupColor 每一块打吃棋的那口气。返回的切片指向 Scratch，下次调用会覆盖。
// 第 1 步不调用。
func (b *Board) AtariLibs(groupColor Color, s *Scratch) []int32 {
	s.cands = s.cands[:0]
	st := s.nextSeen()
	for p := 0; p < b.N; p++ {
		if b.Color[p] != groupColor || s.seen[p] == st {
			continue
		}
		grp, libs := b.groupInfo(p, s)
		for _, g := range grp {
			s.seen[int(g)] = st
		}
		if libs != 1 {
			continue
		}
		lib := int32(-1)
		for _, g := range grp {
			gp := int(g)
			base := gp * 4
			nn := int(b.neighN[gp])
			for i := 0; i < nn; i++ {
				nb := b.neigh[base+i]
				if b.Color[nb] == Empty {
					lib = nb
				}
			}
		}
		if lib >= 0 {
			s.cands = append(s.cands, lib)
		}
	}
	return s.cands
}

// Areas 用中国规则数子：己方的子，加上只和己方接触的空点，都算己方面积。
// 一片空点既挨着黑又挨着白，是单官，双方都不算。不提死子，还在棋盘上的子都算活。
// owner 非空且长度是 N 时，写入每个点的归属。Empty 表示单官或还没写入。
func (b *Board) Areas(s *Scratch, owner []Color) (black, white int) {
	if owner != nil {
		for i := range owner {
			owner[i] = Empty
		}
	}
	for p, c := range b.Color {
		switch c {
		case Black:
			black++
			if owner != nil {
				owner[p] = Black
			}
		case White:
			white++
			if owner != nil {
				owner[p] = White
			}
		}
	}
	st := s.nextSeen()
	for start := 0; start < b.N; start++ {
		if b.Color[start] != Empty || s.seen[start] == st {
			continue
		}
		s.stack = s.stack[:0]
		s.region = s.region[:0]
		s.stack = append(s.stack, int32(start))
		s.seen[start] = st
		touch := 0
		for len(s.stack) > 0 {
			last := len(s.stack) - 1
			p := int(s.stack[last])
			s.stack = s.stack[:last]
			s.region = append(s.region, int32(p))
			base := p * 4
			nn := int(b.neighN[p])
			for i := 0; i < nn; i++ {
				nb := int(b.neigh[base+i])
				switch b.Color[nb] {
				case Empty:
					if s.seen[nb] == st {
						continue
					}
					s.seen[nb] = st
					s.stack = append(s.stack, int32(nb))
				case Black:
					touch |= 1
				case White:
					touch |= 2
				}
			}
		}
		// touch 用两个位：1 表示挨过黑，2 表示挨过白。只有一位时整片空点归那一方。
		if touch == 1 {
			black += len(s.region)
			if owner != nil {
				for _, rp := range s.region {
					owner[int(rp)] = Black
				}
			}
		} else if touch == 2 {
			white += len(s.region)
			if owner != nil {
				for _, rp := range s.region {
					owner[int(rp)] = White
				}
			}
		}
	}
	return black, white
}

// Margin 返回黑方面积减白方面积再减贴目。大于 0 表示黑领先，随机终局用这个判断黑胜。
func (b *Board) Margin(s *Scratch) float64 {
	black, white := b.Areas(s, nil)
	return float64(black-white) - b.Komi
}

// McWhiteWins 是 GNU Go 蒙特卡洛终局的胜负，只给搜索用。页面上的终局仍走中国规则。
// 每个点不是白就是黑：棋子算自己的颜色。空点按南、西、北、东找第一颗子，跟那颗子；
// 四个方向都没有子，算黑。白点数减黑点数，再加贴目，大于 0 白胜。
func (b *Board) McWhiteWins() bool {
	return b.McWhiteWinsWith(nil)
}

// McWhiteWinsWith 和 McWhiteWins 相同。settled 里非空的点直接算给那一方，
// 不再看棋盘上后来变成什么样。这是开局时无条件活棋留下的账。
func (b *Board) McWhiteWinsWith(settled []Color) bool {
	score := 0
	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			i := b.Index(x, y)
			c := Empty
			if i < len(settled) {
				c = settled[i]
			}
			if c == Empty {
				c = b.Color[i]
			}
			if c == Empty {
				c = mcNeighborColor(b, x, y)
			}
			if c == White {
				score++
			} else {
				score--
			}
		}
	}
	return float64(score)+b.Komi > 0
}

// mcNeighborColor 按 GNU Go delta 的前四格看正交邻居。没有子就当黑。
func mcNeighborColor(b *Board, x, y int) Color {
	dirs := [4][2]int{{0, 1}, {-1, 0}, {0, -1}, {1, 0}}
	for _, d := range dirs {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= b.Size || ny >= b.Size {
			continue
		}
		c := b.Color[b.Index(nx, ny)]
		if c == Black || c == White {
			return c
		}
	}
	return Black
}

// String 把棋盘打成文本。黑是 X，白是 O，空点是点。测试用它比较搜索有没有改原棋盘。
func (b *Board) String() string {
	var sb strings.Builder
	for y := 0; y < b.Size; y++ {
		for x := 0; x < b.Size; x++ {
			switch b.Color[b.Index(x, y)] {
			case Black:
				sb.WriteByte('X')
			case White:
				sb.WriteByte('O')
			default:
				sb.WriteByte('.')
			}
		}
		if y+1 < b.Size {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// columns 是棋盘列名。跳过 I，避免和 1 相混。显示行号从下往上数，和坐标 y 相反。
const columns = "ABCDEFGHJKLMNOPQRST"

// FormatPoint 把点号写成棋谱坐标，例如 9 路左上角是 A9。越界或停手返回 "pass"。
func FormatPoint(pt, size int) string {
	if pt < 0 || pt >= size*size {
		return "pass"
	}
	x := pt % size
	y := pt / size
	return string(columns[x]) + itoa(size-y)
}

// itoa 把非负整数写成十进制，给坐标行号用，避免为这件事再引 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
