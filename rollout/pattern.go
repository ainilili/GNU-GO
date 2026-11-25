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
func FinishShape(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) bool {
	limit := b.N * 2
	for b.Passes < 2 && limit > 0 {
		pt, ok := PlayShape(b, s, rng, last)
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
// 返回落下的点，停一手是 -1。权重和 GNU Go 一样按抽样，不是硬性规定必须下某一手。
func PlayShape(b *engine.Board, s *engine.Scratch, rng *rand.Rand, last int) (int, bool) {
	weights := make([]int, b.N)
	total := 0
	for p := 0; p < b.N; p++ {
		w := Weight(b, p, last, s)
		weights[p] = w
		total += w
	}
	if total == 0 {
		_, ok := b.Play(-1, s)
		return -1, ok
	}
	pick := rng.Intn(total)
	for p := 0; p < b.N; p++ {
		pick -= weights[p]
		if pick < 0 {
			if _, ok := b.Play(p, s); ok {
				return p, true
			}
			break
		}
	}
	_, ok := b.Play(-1, s)
	return -1, ok
}

// Weight 返回 pt 这一手的抽样权重。非法点是 0。真眼在表里也是 0。
// 对不上任何特殊行时，表的最后一行给 1。
func Weight(b *engine.Board, pt, last int, s *engine.Scratch) int {
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
	near := isNear(b, pt, last)
	for i := range Patterns() {
		p := &Patterns()[i]
		if !propsMatch(p.need, near, safe, ocap, xcap) {
			continue
		}
		if shapeMatch(p.shape, nb) {
			return p.value
		}
	}
	return 1
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
// 周围已经有两口空气、又提不到子时，不必真的落子。
func moveSafe(b *engine.Board, pt int, s *engine.Scratch, ocap int) (legal, safe bool) {
	if b.EmptyNeighbors(pt) >= 2 && ocap == 0 {
		return true, true
	}
	u, ok := b.Play(pt, s)
	if !ok {
		return false, false
	}
	libs := b.Liberties(pt, s)
	b.Undo(u, s)
	return true, libs > 1
}
