// Package tree 是第 2 步：蒙特卡洛搜索树。
//
// 没有 UCT，没有 RAVE，没有棋形。终局仍然是均匀随机。
// 和扁平蒙特卡洛的差别是：下过的局面留下来。下一盘可以从半路接着走。
// 每一盘最多新展开一个点。最后下访问次数最多的点，不是胜率最高的点。
package tree

import (
	"math/rand"
	"time"

	"weiqi-step/engine"
	"weiqi-step/rollout"
)

// Config 控制模拟盘数。PerMove 乘以合法点数，是希望完成的总盘数。
// 这样页面上同一个数字，两种算法花费的随机终局数量大致相同。
type Config struct {
	PerMove int
	Sims    int // 大于 0 时是总盘数，不再乘以合法点数
	Think   time.Duration
	Seed    int64
}

// Stat 是根上一个候选点的账。Wins 属于搜索开始时该谁下的那一方。
type Stat struct {
	Pt      int
	Games   int
	Wins    int
	WinRate float64
}

// Result 是这一步要下的棋，以及根上每个候选的账。
type Result struct {
	Pt      int
	Pass    bool
	Games   int
	WinRate float64
	Moves   []Stat
	Elapsed time.Duration
	Rounds  int
}

// node 是树上的一个局面。pt 是从父节点走进来的那一手。根节点的 pt 没有意义。
type node struct {
	pt       int
	visits   int
	wins     int
	children []*node
	untried  []int
}

func (c Config) normalized() Config {
	if c.PerMove <= 0 {
		c.PerMove = 8
	}
	if c.PerMove > 40 {
		c.PerMove = 40
	}
	if c.Think <= 0 {
		c.Think = 2 * time.Second
	}
	limit := 8 * time.Second
	if c.Sims > 0 {
		limit = 3 * time.Minute
	}
	if c.Think > limit {
		c.Think = limit
	}
	return c
}

// Search 为 root 当前行棋方选一手。不修改 root。
// 没有合法点，或棋已经结束，就返回停一手。
func Search(root *engine.Board, cfg Config) Result {
	cfg = cfg.normalized()
	start := time.Now()
	out := Result{Pt: -1, Pass: true}
	if root.Over() {
		out.Elapsed = time.Since(start)
		return out
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	s := engine.NewScratch(root.N)
	legal := rollout.Legal(root, s)
	if len(legal) == 0 {
		out.Elapsed = time.Since(start)
		return out
	}
	shuffle(rng, legal)
	rootNode := &node{untried: legal}
	// 总盘数对齐平坦蒙特卡洛：每个候选大约 PerMove 盘。
	// 第一批把根上每个合法点都展开一次，即使已经超时也要做完。
	budget := cfg.PerMove * len(legal)
	if cfg.Sims > 0 {
		budget = cfg.Sims
	}
	deadline := start.Add(cfg.Think)
	sims := 0
	for sims < budget {
		if sims >= len(legal) && time.Now().After(deadline) {
			break
		}
		simulate(root, rootNode, s, rng)
		sims++
	}

	if len(rootNode.children) == 0 {
		out.Elapsed = time.Since(start)
		out.Rounds = sims
		return out
	}
	best := rootNode.children[0]
	for _, c := range rootNode.children[1:] {
		if visitBetter(c.wins, c.visits, best.wins, best.visits) {
			best = c
		}
	}
	moves := make([]Stat, len(rootNode.children))
	total := 0
	for i, c := range rootNode.children {
		wr := 0.0
		if c.visits > 0 {
			wr = float64(c.wins) / float64(c.visits)
		}
		moves[i] = Stat{Pt: c.pt, Games: c.visits, Wins: c.wins, WinRate: wr}
		total += c.visits
	}
	sortByVisits(moves)
	out.Pt = best.pt
	out.Pass = false
	out.Games = total
	if best.visits > 0 {
		out.WinRate = float64(best.wins) / float64(best.visits)
	}
	out.Moves = moves
	out.Rounds = sims
	out.Elapsed = time.Since(start)
	return out
}

// simulate 走一盘。先沿已有节点下行，走到还有没试过的点就只展开一个，然后随机下完。
// 胜负写回整条路径。根的 wins 不记，根的 visits 就是模拟盘数。
func simulate(root *engine.Board, rootNode *node, s *engine.Scratch, rng *rand.Rand) {
	b := root.Clone()
	s.ClearSaved()
	n := rootNode
	path := []*node{n}
	for len(path) < b.N*2 && b.Passes < 2 {
		if len(n.untried) > 0 {
			j := rng.Intn(len(n.untried))
			pt := n.untried[j]
			n.untried = append(n.untried[:j], n.untried[j+1:]...)
			if _, ok := b.Play(pt, s); !ok {
				continue
			}
			child := &node{pt: pt, untried: rollout.Legal(b, s)}
			shuffle(rng, child.untried)
			n.children = append(n.children, child)
			path = append(path, child)
			break
		}
		if len(n.children) == 0 {
			break
		}
		// 这个局面的合法点都已经在树上。走进行棋方胜率最高的子节点。
		next := n.children[0]
		for _, c := range n.children[1:] {
			if rateBetter(c.wins, c.visits, next.wins, next.visits) {
				next = c
			}
		}
		if _, ok := b.Play(next.pt, s); !ok {
			break
		}
		path = append(path, next)
		n = next
	}
	blackWon := rollout.Finish(b, s, rng)
	moverIsBlack := root.Side == engine.Black
	for i, nd := range path {
		nd.visits++
		if i == 0 {
			continue
		}
		blackMoved := moverIsBlack
		if i%2 == 0 {
			blackMoved = !moverIsBlack
		}
		if blackMoved == blackWon {
			nd.wins++
		}
	}
}

// rateBetter 用于往下走：胜率高的优先，胜率相同则访问多的优先。
func rateBetter(wins, visits, bestWins, bestVisits int) bool {
	if visits == 0 {
		return false
	}
	if bestVisits == 0 {
		return true
	}
	if wins*bestVisits != bestWins*visits {
		return wins*bestVisits > bestWins*visits
	}
	return visits > bestVisits
}

// visitBetter 用于最后选点：访问多的优先。次数相同再看胜率。
func visitBetter(wins, visits, bestWins, bestVisits int) bool {
	if visits != bestVisits {
		return visits > bestVisits
	}
	if visits == 0 {
		return false
	}
	return wins*bestVisits > bestWins*visits
}

func sortByVisits(moves []Stat) {
	for i := 1; i < len(moves); i++ {
		j := i
		for j > 0 && visitBetter(moves[j].Wins, moves[j].Games, moves[j-1].Wins, moves[j-1].Games) {
			moves[j], moves[j-1] = moves[j-1], moves[j]
			j--
		}
	}
}

func shuffle(rng *rand.Rand, pts []int) {
	for i := len(pts) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		pts[i], pts[j] = pts[j], pts[i]
	}
}
