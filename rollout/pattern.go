package rollout

import (
	"embed"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"weiqi-step/engine"
)

// mc_montegnu_classic.db 是 GNU Go 3.8 的蒙特卡洛棋形表。
// 从 https://ftp.gnu.org/gnu/gnugo/gnugo-3.8.tar.gz 的 patterns/mc_montegnu_classic.db 取出。
// 文件版权归自由软件基金会，按 GNU GPL 3 发布。
//
//go:embed mc_montegnu_classic.db
var patternDB embed.FS

// cell 是 3×3 邻域里一个点的颜色。棋盘外单独算一种。
type cell int8

const (
	cEmpty cell = iota
	cOwn
	cOpp
	cOff
)

// constraint 是棋形行尾的条件。形状对上还不够，这些也要满足。
type constraint int8

const (
	mustNear constraint = iota
	mustFar
	mustSafe
	ocap1
	ocap2
	ocap3
	xcap1
	xcap2
	xcap3
)

// shapePattern 是表里的一行。Value 是抽样权重，0 表示这手不要下。
// 同一个 3×3 可以有多行，条件不同，权重不同。先对上的那行生效。
type shapePattern struct {
	shape [3]string
	need  []constraint
	value int
}

var (
	patternOnce sync.Once
	patterns    []shapePattern
)

// Patterns 返回从棋形文件解析出的全部行，顺序和文件里一样。
func Patterns() []shapePattern {
	patternOnce.Do(func() {
		text, err := patternDB.ReadFile("mc_montegnu_classic.db")
		if err != nil {
			panic(err)
		}
		patterns = parsePatterns(string(text))
	})
	return patterns
}

// parsePatterns 读 GNU Go 的 .db。三行形状后面跟一行或多行「:权重,条件」。
func parsePatterns(text string) []shapePattern {
	var out []shapePattern
	var shape []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, ":") {
			if len(shape) != 3 {
				panic("pattern value without a 3x3 shape")
			}
			value, need := parseRule(line)
			var box [3]string
			copy(box[:], shape)
			out = append(out, shapePattern{shape: box, need: need, value: value})
			continue
		}
		if len(line) != 3 {
			panic("pattern row must be 3 characters: " + line)
		}
		if len(shape) == 3 {
			shape = nil
		}
		shape = append(shape, line)
	}
	if len(out) == 0 {
		panic("no patterns")
	}
	return out
}

// parseRule 解析「:10000,ocap1,near,osafe」这种行。
func parseRule(line string) (int, []constraint) {
	parts := strings.Split(strings.TrimPrefix(line, ":"), ",")
	value, err := strconv.Atoi(parts[0])
	if err != nil {
		panic(line)
	}
	var need []constraint
	for _, name := range parts[1:] {
		switch name {
		case "near":
			need = append(need, mustNear)
		case "far":
			need = append(need, mustFar)
		case "osafe":
			need = append(need, mustSafe)
		case "ocap1":
			need = append(need, ocap1)
		case "ocap2":
			need = append(need, ocap2)
		case "ocap3":
			need = append(need, ocap3)
		case "xcap1":
			need = append(need, xcap1)
		case "xcap2":
			need = append(need, xcap2)
		case "xcap3":
			need = append(need, xcap3)
		default:
			panic("unknown pattern constraint: " + name)
		}
	}
	return value, need
}

// FinishShape 用棋形权重把棋下完。和 Finish 的停手、手数上限、胜负判断相同。
// 每一步按 Weight 抽样，不是均匀抽。last 是上一手的点，没有就传 -1。
// 同一盘里的权重留在 Shape 上，落子后只重算受影响的点。
func FinishShape(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) bool {
	var sh Shape
	limit := b.N * 2
	for b.Passes < 2 && limit > 0 {
		pt, ok := sh.Play(b, s, rng, last)
		if !ok {
			break
		}
		if pt >= 0 {
			last = pt
		}
		limit--
	}
	return b.Margin(s) > 0
}

// PlayShape 按棋形权重下一手。没有正权重的点就停一手。
// 单独调用时会先把整盘权重算一遍。连续下完一盘要用 Shape，避免每手都重算。
func PlayShape(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) (int, bool) {
	var sh Shape
	return sh.Play(b, s, rng, last)
}

// Shape 记下双方每个空点在「远离上一手」时的权重。
// 抽样时只把上一手的更新队列临时换成近处权重。落子后只重算棋子或气数变了的点。
// 一只 Shape 只服务同一盘棋的连续落子。换一盘棋要换一只，或者让 n 对不上从而重算。
type Shape struct {
	far      [3][]int
	sum      [3]int
	stamp    []int
	list     []int
	buf      []int
	hold     []int
	stack    []int
	caps     []int
	gid      []int16
	absKey   []uint16
	vis      []uint32
	groups   []stoneGrp
	free     []int16
	ownID    [4]int16
	capID    [4]int16
	nOwn     int
	nCap     int
	visMark  uint32
	mark     int
	n        int
	ready    bool
	nearPts  []int
	nearOn   []bool
	holdNear []int
}

// Reset 丢掉上一盘终局留下的权重。下一手会按当前棋盘重算。
func (sh *Shape) Reset() {
	sh.ready = false
	sh.holdNear = sh.holdNear[:0]
}

// PrimeNear 指定开局这一手抽样时当作「近」的点。搜索树里的最后一手用它。
// 必须在 Reset 之后、Play 之前调用。停手则传 nil。
func (sh *Shape) PrimeNear(pts []int) {
	sh.holdNear = append(sh.holdNear[:0], pts...)
}

// Play 按当前权重下一手，并更新受这手影响的权重。停一手返回 -1。
func (sh *Shape) Play(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) (int, bool) {
	if sh.n != b.N || !sh.ready {
		hold := append([]int(nil), sh.holdNear...)
		sh.rebuild(b, s)
		sh.applyNear(hold)
	}
	pt := sh.sample(b, s, rng, last)
	var next []int
	if pt >= 0 {
		next = NearQueue(b, pt, s)
	}
	sh.beginRetouch()
	if pt >= 0 {
		// 棋块账先按「这手已经落下」更新。棋盘本身还没改。
		sh.follow(b, pt)
	}
	u, ok := b.Play(pt, s)
	if !ok {
		sh.ready = false
		return -1, false
	}
	if pt >= 0 {
		sh.rewriteArea(b, pt)
		for _, p := range sh.caps {
			sh.rewriteArea(b, p)
		}
	}
	if u.Ko >= 0 {
		sh.add(u.Ko)
	}
	if b.Ko >= 0 {
		sh.add(b.Ko)
	}
	sh.reweight(b, s)
	if pt < 0 {
		sh.applyNear(nil)
	} else {
		sh.applyNear(next)
	}
	return pt, true
}

func (sh *Shape) applyNear(pts []int) {
	if len(sh.nearOn) != sh.n {
		sh.nearOn = make([]bool, sh.n)
	} else {
		for i := range sh.nearOn {
			sh.nearOn[i] = false
		}
	}
	sh.nearPts = sh.nearPts[:0]
	for _, p := range pts {
		if p < 0 || p >= sh.n || sh.nearOn[p] {
			continue
		}
		sh.nearOn[p] = true
		sh.nearPts = append(sh.nearPts, p)
	}
	sh.holdNear = append(sh.holdNear[:0], sh.nearPts...)
}

// rebuild 按「没有上一手」把双方权重整盘算一遍。近处加成在抽样时再叠上去。
// 先把每块棋的气记下来。后面每个点只查这块账，不再整块重数。
func (sh *Shape) rebuild(b *engine.Board, s *engine.Scratch) {
	sh.alloc(b.N)
	for p := 0; p < b.N; p++ {
		sh.writeKey(b, p)
	}
	sh.indexGroups(b, s)
	for _, c := range []engine.Color{engine.Black, engine.White} {
		sh.sum[c] = 0
		for p := 0; p < b.N; p++ {
			w := sh.weight(b, p, -1, c, s)
			sh.far[c][p] = w
			sh.sum[c] += w
		}
	}
}

// alloc 准备本盘用的数组。换一盘棋时把棋块账清掉。
func (sh *Shape) alloc(n int) {
	if cap(sh.stamp) < n {
		sh.stamp = make([]int, n)
		sh.gid = make([]int16, n)
		sh.absKey = make([]uint16, n)
		sh.vis = make([]uint32, n)
	} else {
		sh.stamp = sh.stamp[:n]
		sh.gid = sh.gid[:n]
		sh.absKey = sh.absKey[:n]
		sh.vis = sh.vis[:n]
		for i := range sh.stamp {
			sh.stamp[i] = 0
		}
		for i := range sh.vis {
			sh.vis[i] = 0
		}
	}
	for i := range sh.gid {
		sh.gid[i] = -1
	}
	sh.groups = sh.groups[:0]
	sh.free = sh.free[:0]
	sh.visMark = 0
	for _, c := range []engine.Color{engine.Black, engine.White} {
		if cap(sh.far[c]) < n {
			sh.far[c] = make([]int, n)
		} else {
			sh.far[c] = sh.far[c][:n]
		}
	}
	sh.list = sh.list[:0]
	sh.mark = 0
	sh.n = n
	sh.ready = false
}

// sample 用远处权重，并把上一手更新队列里的点换成近处权重后抽样。
// last 只留给和整盘重算对照。队列本身记在 nearPts 里。
func (sh *Shape) sample(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) int {
	side := b.Side
	total := sh.sum[side]
	repl := make([]int, 0, len(sh.nearPts))
	replW := make([]int, 0, len(sh.nearPts))
	for _, p := range sh.nearPts {
		w := sh.weightSide(b, p, side, true, s)
		total += w - sh.far[side][p]
		repl = append(repl, p)
		replW = append(replW, w)
	}
	if total <= 0 {
		return -1
	}
	pick := rng.Intn(total)
	for p := 0; p < b.N; p++ {
		w := sh.far[side][p]
		for i, q := range repl {
			if q == p {
				w = replW[i]
				break
			}
		}
		pick -= w
		if pick < 0 {
			return p
		}
	}
	return -1
}

// beginRetouch 开始记这一手要重算权重的点。
func (sh *Shape) beginRetouch() {
	sh.mark++
	if sh.mark == 0 {
		for i := range sh.stamp {
			sh.stamp[i] = 0
		}
		sh.mark = 1
	}
	sh.list = sh.list[:0]
	sh.caps = sh.caps[:0]
}

// reweight 按列表重算双方的远处权重。
func (sh *Shape) reweight(b *engine.Board, s *engine.Scratch) {
	for _, p := range sh.list {
		for _, c := range []engine.Color{engine.Black, engine.White} {
			w := sh.weight(b, p, -1, c, s)
			sh.sum[c] += w - sh.far[c][p]
			sh.far[c][p] = w
		}
	}
}

func (sh *Shape) add(p int) {
	if p < 0 || p >= sh.n || sh.stamp[p] == sh.mark {
		return
	}
	sh.stamp[p] = sh.mark
	sh.list = append(sh.list, p)
}

// eachNeighbor 对 pt 的每个正交邻点调用 fn。角和边上的点没有四个邻居。
func eachNeighbor(b *engine.Board, pt int, fn func(int)) {
	size := b.Size
	x0, y0 := pt%size, pt/size
	for _, d := range ortho {
		x, y := x0+d[0], y0+d[1]
		if x < 0 || y < 0 || x >= size || y >= size {
			continue
		}
		fn(y*size + x)
	}
}

var ortho = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// Weight 返回 pt 这一手的抽样权重。非法点是 0。真眼在表里也是 0。
// 对不上任何特殊行时，表的最后一行给 1。
func Weight(b *engine.Board, pt, last int, s *engine.Scratch) int {
	return weightFor(b, pt, last, b.Side, s)
}

// weight 用棋块账算权重。抽样和增量更新走这里，避免每个点都重数气。
func (sh *Shape) weight(b *engine.Board, pt, last int, side engine.Color, s *engine.Scratch) int {
	saved := b.Side
	b.Side = side
	w := sh.weightNow(b, pt, last, s)
	b.Side = saved
	return w
}

func (sh *Shape) weightSide(b *engine.Board, pt int, side engine.Color, near bool, s *engine.Scratch) int {
	saved := b.Side
	b.Side = side
	w := sh.weightFlag(b, pt, near, s)
	b.Side = saved
	return w
}

// weightFor 按指定的一方算权重。棋盘该谁下会被临时改成这一方，算完再改回来。
// 这是对照用的整盘算法，不看棋块账。
func weightFor(b *engine.Board, pt, last int, side engine.Color, s *engine.Scratch) int {
	saved := b.Side
	b.Side = side
	w := weightNow(b, pt, last, s)
	b.Side = saved
	return w
}

// weightNow 用棋盘上现在的行棋方算 pt 的权重。
func weightNow(b *engine.Board, pt, last int, s *engine.Scratch) int {
	if pt < 0 || pt >= b.N || b.Color[pt] != engine.Empty || pt == b.Ko {
		return 0
	}
	nb := readNeigh(b, pt, b.Side)
	ocap := b.AtariCapture(pt, engine.Opponent(b.Side), s)
	xcap := b.AtariCapture(pt, b.Side, s)
	legal, safe := moveSafe(b, pt, s, ocap)
	if !legal {
		return 0
	}
	return patternWeight(nb, isNear(b, pt, last), safe, ocap, xcap)
}

// weightNow 用已经记好的气数算权重。
func (sh *Shape) weightNow(b *engine.Board, pt, last int, s *engine.Scratch) int {
	return sh.weightFlag(b, pt, isNear(b, pt, last), s)
}

func (sh *Shape) weightFlag(b *engine.Board, pt int, near bool, s *engine.Scratch) int {
	if pt < 0 || pt >= b.N || b.Color[pt] != engine.Empty || pt == b.Ko {
		return 0
	}
	ocap := sh.atariCount(b, pt, engine.Opponent(b.Side), s)
	xcap := sh.atariCount(b, pt, b.Side, s)
	legal, safe := sh.fastSafe(b, pt, s, ocap)
	if !legal {
		return 0
	}
	if ocap == 0 && xcap == 0 && safe {
		k := relIndex(sh.absKey[pt], b.Side)
		if near {
			return nearQuiet[k]
		}
		return farQuiet[k]
	}
	return patternWeight(readNeigh(b, pt, b.Side), near, safe, ocap, xcap)
}

// patternWeight 在棋形表里找第一行对得上的权重。
// 不提子、自己也安全时，3×3 的答案已经算好，直接查表。
func patternWeight(nb [3][3]cell, near, safe bool, ocap, xcap int) int {
	if ocap == 0 && xcap == 0 && safe {
		k := neighKey(nb)
		if near {
			return nearQuiet[k]
		}
		return farQuiet[k]
	}
	return matchValue(Patterns(), nb, near, safe, ocap, xcap)
}

// matchValue 按表的顺序比对。先对上的那行说了算，一行都没有就是 1。
func matchValue(ps []shapePattern, nb [3][3]cell, near, safe bool, ocap, xcap int) int {
	for i := range ps {
		p := &ps[i]
		if !propsMatch(p.need, near, safe, ocap, xcap) {
			continue
		}
		if shapeMatch(p.shape, nb) {
			return p.value
		}
	}
	return 1
}

// 邻点四种状态各占 2 位，8 个邻点一共 16 位。正中要下的点不放进键里。
const neighKeys = 65536

// farQuiet、nearQuiet 是「提不到子，而且这手至少两气」时的权重。下标是 3×3。
var farQuiet [neighKeys]int
var nearQuiet [neighKeys]int

func init() {
	ps := Patterns()
	var nb [3][3]cell
	nb[1][1] = cEmpty
	for key := 0; key < neighKeys; key++ {
		fillNeigh(key, &nb)
		farQuiet[key] = matchValue(ps, nb, false, true, 0, 0)
		nearQuiet[key] = matchValue(ps, nb, true, true, 0, 0)
	}
}

// fillNeigh 把 16 位的键拆回 3×3。顺序和 neighKey 一致。
func fillNeigh(key int, nb *[3][3]cell) {
	shift := 0
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			if x == 1 && y == 1 {
				continue
			}
			nb[y][x] = cell((key >> shift) & 3)
			shift += 2
		}
	}
}

// neighKey 把 3×3 邻点压成查表用的键。
func neighKey(nb [3][3]cell) int {
	key := 0
	shift := 0
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			if x == 1 && y == 1 {
				continue
			}
			key |= int(nb[y][x]) << shift
			shift += 2
		}
	}
	return key
}

// propsMatch 检查行尾的条件。ocap、xcap 超过 3 的按 3 个子算。
func propsMatch(need []constraint, near, safe bool, ocap, xcap int) bool {
	if ocap > 3 {
		ocap = 3
	}
	if xcap > 3 {
		xcap = 3
	}
	for _, c := range need {
		switch c {
		case mustNear:
			if !near {
				return false
			}
		case mustFar:
			if near {
				return false
			}
		case mustSafe:
			if !safe {
				return false
			}
		case ocap1:
			if ocap != 1 {
				return false
			}
		case ocap2:
			if ocap != 2 {
				return false
			}
		case ocap3:
			if ocap < 3 {
				return false
			}
		case xcap1:
			if xcap != 1 {
				return false
			}
		case xcap2:
			if xcap != 2 {
				return false
			}
		case xcap3:
			if xcap < 3 {
				return false
			}
		}
	}
	return true
}

// isNear 判断 pt 是不是上一手的 3×3 邻域。GNU Go 在上一手周围重算权重，范围就是这块。
func isNear(b *engine.Board, pt, last int) bool {
	if last < 0 || last >= b.N {
		return false
	}
	dx := pt%b.Size - last%b.Size
	dy := pt/b.Size - last/b.Size
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= 1 && dy <= 1
}

// shapeMatch 把形状旋转、镜像共 8 种，有一种对上就算对上。
func shapeMatch(shape [3]string, nb [3][3]cell) bool {
	for s := 0; s < 8; s++ {
		if shapeFits(shape, nb, s) {
			return true
		}
	}
	return false
}

// shapeFits 用一种对称方式比对。sym 的低两位是旋转次数，4 及以上先镜像。
func shapeFits(shape [3]string, nb [3][3]cell, sym int) bool {
	mirror := sym >= 4
	rot := sym & 3
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			sx, sy := x, y
			if mirror {
				sx = 2 - sx
			}
			for r := 0; r < rot; r++ {
				sx, sy = sy, 2-sx
			}
			if !cellOK(shape[y][x], nb[sy][sx]) {
				return false
			}
		}
	}
	return true
}

// cellOK 对照 GNU Go 的一个格子。O 是自己，X 是对方，小写表示空或该色，% 是任意，| - + 是棋盘外。
func cellOK(want byte, got cell) bool {
	switch want {
	case '*':
		return true
	case '.':
		return got == cEmpty
	case 'O':
		return got == cOwn
	case 'X':
		return got == cOpp
	case 'o':
		return got == cEmpty || got == cOwn
	case 'x':
		return got == cEmpty || got == cOpp
	case '?':
		return got != cOff
	case '%':
		return true
	case '|', '-', '+':
		return got == cOff
	default:
		return false
	}
}

// readNeigh 读 pt 周围 3×3。下标 [dy+1][dx+1]，正中是要下的点。
func readNeigh(b *engine.Board, pt int, side engine.Color) [3][3]cell {
	var nb [3][3]cell
	size := b.Size
	x0 := pt % size
	y0 := pt / size
	opp := engine.Opponent(side)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			x, y := x0+dx, y0+dy
			var c cell
			if x < 0 || y < 0 || x >= size || y >= size {
				c = cOff
			} else {
				switch b.Color[y*size+x] {
				case engine.Empty:
					c = cEmpty
				case side:
					c = cOwn
				case opp:
					c = cOpp
				}
			}
			nb[dy+1][dx+1] = c
		}
	}
	return nb
}

// moveSafe 判断这手能不能下，以及下完自己是不是至少两气。
// 周围已经有两口空气时，不必真的落子。提子只会再多一口气。
func moveSafe(b *engine.Board, pt int, s *engine.Scratch, ocap int) (legal, safe bool) {
	empties, own := countNeighbors(b, pt)
	if empties >= 2 {
		return true, true
	}
	if own == 0 && ocap == 0 {
		if empties == 0 {
			return false, false
		}
		return true, false
	}
	u, ok := b.Play(pt, s)
	if !ok {
		return false, false
	}
	libs := b.Liberties(pt, s)
	b.Undo(u, s)
	return true, libs > 1
}

// countNeighbors 数 pt 的正交空点和己方子。
func countNeighbors(b *engine.Board, pt int) (empties, own int) {
	eachNeighbor(b, pt, func(nb int) {
		switch b.Color[nb] {
		case engine.Empty:
			empties++
		case b.Side:
			own++
		}
	})
	return empties, own
}
