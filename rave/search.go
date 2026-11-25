// Package rave 是第 3 步：蒙特卡洛树加上 UCT 和 RAVE。
// 随机终局按 GNU Go 的 3×3 棋形表抽样，不再均匀抽点。和搜索树的差别还有选点：
//
//	UCT:  Q + c * sqrt(ln(N+1) / (n+1))，c = 0.1
//	RAVE: (1-β) * Q + β * Q_AMAF，β = sqrt(k / (3n+k))，k = 1000
//
// 混好的值放进 UCT，再和还没试过的点一起比。最后下访问次数最多的点。
// 不同次序落到同一盘棋时共用一个节点：这一手记在边上，胜负和 RAVE 记在局面上。
package rave

import (
	"math"
	"math/rand"
	"time"

	"weiqi-step/engine"
	"weiqi-step/rollout"
)

// Config 控制模拟盘数和两个常数。PerMove 乘以合法点数，是希望完成的总盘数。
// C、K 传 0 时用上面的缺省值。
type Config struct {
	PerMove int
	Think   time.Duration
	Seed    int64
	C       float64
	K       float64
}

// Stat 是根上一个候选点的账。Wins 属于搜索开始时该谁下的那一方。
// Rave 是这个点作为 AMAF 被记到的胜率，和这手自己的 WinRate 不是同一笔账。
type Stat struct {
	Pt      int
	Games   int
	Wins    int
	WinRate float64
	RaveN   int
	RaveW   int
	Rave    float64
}

// Result 是这一步要下的棋，以及根上每个已展开候选的账。
type Result struct {
	Pt      int
	Pass    bool
	Games   int
	WinRate float64
	Moves   []Stat
	Elapsed time.Duration
	Rounds  int
}

// edge 是从某个局面走出的一手。pt 是这一手的点。
// to 是落下之后的局面。几条边可以指向同一个节点。
type edge struct {
	pt int
	to *node
}

// node 是一个局面，不记「从哪一手走进来」。
// 同一盘棋无论哪条路径走到，都是这一个节点。
// raveN、raveW 按下标记交叉点。停一手不进这两张表。
type node struct {
	visits  int
	wins    int
	edges   []edge
	untried []int
	inited  bool
	raveN   []int
	raveW   []int
}

// played 是这一盘里实际落下的一手，包括树上的手和后面的随机终局。
type played struct {
	pt    int
	color engine.Color
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
	if c.Think > 8*time.Second {
		c.Think = 8 * time.Second
	}
	if c.C <= 0 {
		c.C = 0.1
	}
	if c.K <= 0 {
		c.K = 1000
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
	rootNode := &node{}
	rootNode.ensure(root, s, rng)
	// 根不放进表。根的账是整次搜索，不是「又走回开局」这一手的账。
	// 搜索中第一次走回和根相同的盘面时另建节点，之后再走到才并进去。
	seen := make(map[string]*node)
	// 总盘数对齐另外两种算法。UCT 自己决定先展开谁，不强制把每个点都先走一遍。
	budget := cfg.PerMove * len(legal)
	deadline := start.Add(cfg.Think)
	sims := 0
	// 每一圈跑一盘，不是在这里随机选点。传入的 root、rootNode、c、k 每次相同：
	// root 是开局的那盘棋，模拟都从这里重新复制一份再下。
	// rootNode 是同一棵树。选哪一手由它里面的访问、胜局和 RAVE 决定，simulate 会改这些数。
	// 所以下一盘看到的分数已经变了。rng 也是同一个，只是内部随机数往前走。
	// 第一盘即使已经超过 Think 也要跑完，后面的盘才看时间。
	for sims < budget {
		if sims > 0 && time.Now().After(deadline) {
			break
		}
		simulate(root, rootNode, seen, s, rng, cfg.C, cfg.K)
		sims++
	}
	if len(rootNode.edges) == 0 {
		out.Elapsed = time.Since(start)
		out.Rounds = sims
		return out
	}
	best := rootNode.edges[0]
	for _, e := range rootNode.edges[1:] {
		if visitBetter(e.to.wins, e.to.visits, best.to.wins, best.to.visits) {
			best = e
		}
	}
	moves := make([]Stat, len(rootNode.edges))
	total := 0
	for i, e := range rootNode.edges {
		wr := 0.0
		if e.to.visits > 0 {
			wr = float64(e.to.wins) / float64(e.to.visits)
		}
		rn, rw := 0, 0
		if e.pt >= 0 && e.pt < len(rootNode.raveN) {
			rn = rootNode.raveN[e.pt]
			rw = rootNode.raveW[e.pt]
		}
		rv := 0.0
		if rn > 0 {
			rv = float64(rw) / float64(rn)
		}
		moves[i] = Stat{Pt: e.pt, Games: e.to.visits, Wins: e.to.wins, WinRate: wr, RaveN: rn, RaveW: rw, Rave: rv}
		total += e.to.visits
	}
	sortByVisits(moves)
	out.Pt = best.pt
	out.Pass = false
	out.Games = total
	if best.to.visits > 0 {
		out.WinRate = float64(best.to.wins) / float64(best.to.visits)
	}
	out.Moves = moves
	out.Rounds = sims
	out.Elapsed = time.Since(start)
	return out
}

// ensure 第一次走进这个局面时列出合法点，并准备 RAVE 表。
func (n *node) ensure(b *engine.Board, s *engine.Scratch, rng *rand.Rand) {
	if n.inited {
		return
	}
	n.inited = true
	n.raveN = make([]int, b.N)
	n.raveW = make([]int, b.N)
	n.untried = rollout.Legal(b, s)
	shuffle(rng, n.untried)
}

// simulate 从根局面走完整的一盘。外面每次传入的 root、rootNode、seen、c、k 都是同一个，
// 模拟仍会往前走，因为变的是这些对象里面的内容，不是参数本身：
//
//   - root 只读。每盘都 Clone 一份到 b 上落子，下完就丢掉，原棋盘始终是开局。
//   - rootNode 是同一棵树。上一盘 backup 写进去的访问、胜局、边、RAVE 还在。
//     这一盘的 choose 读的就是这些已经变过的数，所以选路和上一盘不同。
//   - seen 是局面指纹到节点的表。这一盘新建的节点留在表里，下一盘走到同一盘棋就接上。
//   - rng 是同一个随机源，每用一次内部状态就往后移，棋形抽样因此也不重复。
//
// 一盘分三段。先沿树向下。走到已经见过的局面就接上那个节点，继续用它的账选点。
// 真正新建的节点一盘只加一个，然后不再往下长。剩下的棋按棋形权重下完。
// 最后把胜负写回 path 上的那些节点，供下一盘使用。
func simulate(root *engine.Board, rootNode *node, seen map[string]*node, s *engine.Scratch, rng *rand.Rand, c, k float64) {
	// b 是这盘的棋盘副本。rootNode 不复制，后面改的就是外面那棵树。
	b := root.Clone()
	s.ClearSaved()
	n := rootNode
	path := []*node{n}
	moves := make([]played, 0, 16)
	last := -1
	// 第一段：沿已有的树往下走。每一步用当前节点上的账算 UCT+RAVE。
	// 选中旧边就走到它指向的局面。选中还没试过的点，先看落子后的盘面见没见过。
	for len(path) < b.N*2 && b.Passes < 2 {
		n.ensure(b, s, rng)
		pt, to := n.choose(rng, c, k)
		if to == nil && pt < 0 {
			break
		}
		// 自己的真眼不长进树里。从候选里拿掉，这一步重选。终局抽样里它的权重仍是 0。
		if to == nil && b.ProperSmallEye(pt, b.Side) {
			n.dropUntried(pt)
			continue
		}
		color := b.Side
		// 落子落在副本 b 上。root 不动。
		if _, ok := b.Play(pt, s); !ok {
			break
		}
		moves = append(moves, played{pt: pt, color: color})
		if pt >= 0 {
			last = pt
		}
		if to != nil {
			// 这条边以前就有。局面已经在路径上说明走回了旧盘，本盘不再绕进去。
			if nodeOnPath(path, to) {
				break
			}
			path = append(path, to)
			n = to
			continue
		}
		// 这手从当前节点第一次走出。见过的局面直接接上，没见过才新建。
		n.dropUntried(pt)
		to, fresh := n.attach(b, pt, seen, s, rng)
		if nodeOnPath(path, to) {
			break
		}
		path = append(path, to)
		n = to
		if fresh {
			break
		}
	}
	// 第二段：树不再往下长。后面双方按棋形权重下到终局。
	// 这些手只记在 moves 里，不再长出节点。RAVE 会用到它们。
	limit := b.N * 2
	for b.Passes < 2 && limit > 0 {
		color := b.Side
		pt, ok := rollout.PlayShape(b, s, rng, last)
		if !ok {
			break
		}
		moves = append(moves, played{pt: pt, color: color})
		if pt >= 0 {
			last = pt
		}
		limit--
	}
	// 第三段：用终局谁赢了，去改 path 上每个节点的 visits、wins 和 RAVE。
	// 改的是 rootNode 以及它的子节点。下一盘 simulate 还是传入这个 rootNode，读到的就是更新后的账。
	backup(path, moves, root.Side, b.Margin(s) > 0)
}

// choose 在已有的边和未试过的点里挑分数最高的。
// to 非空表示沿这条边走到已有局面，pt 是这条边上的点。
// to 为空且 pt >= 0 表示要从当前局面第一次走这个点。两边都没有，pt 为 -1。
func (n *node) choose(rng *rand.Rand, c, k float64) (int, *node) {
	best := math.Inf(-1)
	bestPt := -1
	var bestTo *node
	parent := n.visits
	// UCT+RAVE：分数 = (1-β)Q + β Q_AMAF + c * sqrt(ln(N+1)/(n+1))
	// Q        这个点自己的胜率，wins/visits。还没下过按 0.5。
	// wins     落到这一手之后的那个局面并且赢了的盘数。别的路径走到同一盘也算。
	// visits   走到那个局面的盘数。公式里的 n 就是它。
	// Q_AMAF   RAVE 胜率，raveW/raveN。没有 RAVE 样本时按 0.5。
	// raveN    从这里往后，轮到走的一方下到这个点的盘数。同一盘只记一次。
	// raveW    上面这些盘里，这一方赢了的盘数。
	// N        当前局面已经完成的模拟盘数，也就是 parent。
	// n        同 visits。探索项的分母用 n+1，所以没下过的点也能算。
	// β        sqrt(k/(3n+k))。n 越小越接近 1，分数越信 Q_AMAF。
	// k        RAVE 常数，缺省 1000。越大，越久才改信这个点自己的胜率。
	// c        UCT 常数，缺省 0.1。越大，少下过的点越容易被拿来试。
	// 1e-9     随机数，只用来拆完全相同的分数，不算进公式。
	consider := func(visits, wins, raveN, raveW int) float64 {
		return score(parent, visits, wins, raveN, raveW, c, k) + rng.Float64()*1e-9
	}
	// 已经走出的边。visits、wins 记在落下之后的那个局面上。
	// 别的路径若走到同一盘棋，加进去的也是这本账。
	// RAVE 记在当前节点上，用这一手的点号去查。
	for _, e := range n.edges {
		rn, rw := 0, 0
		if e.pt >= 0 && e.pt < len(n.raveN) {
			rn, rw = n.raveN[e.pt], n.raveW[e.pt]
		}
		sc := consider(e.to.visits, e.to.wins, rn, rw)
		if sc > best {
			best = sc
			bestTo = e.to
			bestPt = e.pt
		}
	}
	for _, pt := range n.untried {
		rn, rw := 0, 0
		if pt >= 0 && pt < len(n.raveN) {
			rn, rw = n.raveN[pt], n.raveW[pt]
		}
		sc := consider(0, 0, rn, rw)
		if sc > best {
			best = sc
			bestTo = nil
			bestPt = pt
		}
	}
	return bestPt, bestTo
}

// attach 把刚落下的局面接到当前节点上。
// 指纹见过就返回原来的节点，fresh 为 false，调用方继续用那本账往下走。
// 没见过才新建，fresh 为 true，这一盘的树在这里停住。
func (n *node) attach(b *engine.Board, pt int, seen map[string]*node, s *engine.Scratch, rng *rand.Rand) (*node, bool) {
	key := positionKey(b)
	if child, ok := seen[key]; ok {
		n.edges = append(n.edges, edge{pt: pt, to: child})
		return child, false
	}
	child := &node{}
	child.ensure(b, s, rng)
	seen[key] = child
	n.edges = append(n.edges, edge{pt: pt, to: child})
	return child, true
}

// positionKey 是局面指纹。棋子、该谁下、劫点、连续停手次数都算进去。
// 次序不同但指纹相同，就是同一盘棋。
func positionKey(b *engine.Board) string {
	buf := make([]byte, b.N+4)
	for i, c := range b.Color {
		buf[i] = byte(c)
	}
	buf[b.N] = byte(b.Side)
	ko := b.Ko + 1
	buf[b.N+1] = byte(ko)
	buf[b.N+2] = byte(ko >> 8)
	buf[b.N+3] = byte(b.Passes)
	return string(buf)
}

func nodeOnPath(path []*node, n *node) bool {
	for _, p := range path {
		if p == n {
			return true
		}
	}
	return false
}

// dropUntried 把刚展开的点从未试列表里拿走。
func (n *node) dropUntried(pt int) {
	for i, p := range n.untried {
		if p != pt {
			continue
		}
		n.untried = append(n.untried[:i], n.untried[i+1:]...)
		return
	}
}

// score 计算一个候选的 UCT+RAVE 分数。visits 为 0 时 Q 按 0.5，没有 RAVE 样本时 Q_AMAF 也按 0.5。
// parent 是当前局面已经完成的模拟盘数，visits 是这个候选自己的盘数。
func score(parent, visits, wins, raveN, raveW int, c, k float64) float64 {
	n := float64(visits)
	q := 0.5
	if visits > 0 {
		q = float64(wins) / n
	}
	qr := 0.5
	if raveN > 0 {
		qr = float64(raveW) / float64(raveN)
	}
	beta := math.Sqrt(k / (3*n + k))
	explore := c * math.Sqrt(math.Log(float64(parent)+1)/(n+1))
	return (1-beta)*q + beta*qr + explore
}

// backup 给路径上的每个节点加访问。根不记胜。
// 走进节点的那一方赢了，这个节点的 wins 才加。
// RAVE 从该节点往后看，只记轮到它走的那一方后来下过的点，每个点这一盘只记一次。
func backup(path []*node, moves []played, rootSide engine.Color, blackWon bool) {
	seen := make([]int, 0)
	stamp := 0
	for i, nd := range path {
		nd.visits++
		if i > 0 && (moves[i-1].color == engine.Black) == blackWon {
			nd.wins++
		}
		if nd.raveN == nil {
			continue
		}
		if len(seen) != len(nd.raveN) {
			seen = make([]int, len(nd.raveN))
			stamp = 0
		}
		stamp++
		side := rootSide
		if i%2 == 1 {
			side = engine.Opponent(rootSide)
		}
		for j := i; j < len(moves); j++ {
			if moves[j].color != side {
				continue
			}
			a := moves[j].pt
			if a < 0 || a >= len(nd.raveN) {
				continue
			}
			if seen[a] == stamp {
				continue
			}
			seen[a] = stamp
			nd.raveN[a]++
			if (side == engine.Black) == blackWon {
				nd.raveW[a]++
			}
		}
	}
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
