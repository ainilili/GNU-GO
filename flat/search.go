// Package flat 是第 1 步：平坦蒙特卡洛。
//
// 没有搜索树，没有 UCT，没有 RAVE。
// 每个合法点单独先下一手，然后双方在合法点里均匀随机下到终局。
// 最后下「当时该谁走」胜率最高的那个点。
package flat

import (
	"math/rand"
	"time"

	"weiqi-step/engine"
	"weiqi-step/rollout"
)

// Config 控制每个候选点要下完多少盘随机棋。
type Config struct {
	// PerMove 是每个候选希望完成的随机局数。第一轮一定跑完，后面的轮次受到 Think 限制。
	PerMove int
	// Think 是思考时间上限。第一轮不受它限制。
	Think time.Duration
	// Seed 是随机数种子。0 表示用当前时间，同一局面每次结果可以不同。
	Seed int64
}

// Stat 是一个候选点的账。Wins 属于搜索开始时该谁下的那一方，不是固定的黑方。
type Stat struct {
	Pt      int     // 候选点的下标
	Games   int     // 这个点实际下完的随机局数
	Wins    int     // 其中行棋方获胜的局数
	WinRate float64 // Wins/Games。没有样本时是 0
}

// Result 是这一步要下的棋，以及每个候选的账。
type Result struct {
	Pt      int           // 选中的点。停一手时是 -1
	Pass    bool          // 没有合法点，或者棋已经结束
	Games   int           // 所有候选的随机局数之和
	WinRate float64       // 选中那一手的胜率
	Moves   []Stat        // 全部候选，已按胜率从高到低排好
	Elapsed time.Duration // 这次搜索花的时间
	Rounds  int           // 实际跑完的轮数。一轮里每个候选各下一盘
}

// normalized 把缺省和过大的配置收进可用范围。调用方可以只填想改的字段。
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
	return c
}

// Search 为 root 当前行棋方选一手。这是平坦蒙特卡洛，不是 MCTS。
//
// 没有搜索树，也没有 UCT、RAVE。不做的事：不沿已有节点往下走，不展开新节点，
// 不把胜负写回路径上的中间局面。每一盘都从根局面重新复制。
//
// 做的事分三步：
//  1. 列出当前所有合法点。棋已结束，或一个合法点都没有，就停一手。
//  2. 对每个点单独先把这手下了，再双方随机下到终局，记下行棋方赢了没有。
//     一轮里每个点各下一盘。第 0 轮一定跑完，后面的轮次受思考时间限制。
//  3. 选胜率最高的点。胜率相同就留样本更多的；还相同就留扫描顺序里更早的。
//
// 不修改 root。随机棋都在复制出来的棋盘上下。
func Search(root *engine.Board, cfg Config) Result {
	cfg = cfg.normalized()
	start := time.Now()
	out := Result{Pt: -1, Pass: true} // 默认停一手，下面找到合法点再改掉
	if root.Over() {                  // 已经连续停两次，对局结束
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
	if len(legal) == 0 { // 没有能落子的点。停一手不算候选，只在这里使用
		out.Elapsed = time.Since(start)
		return out
	}

	// games[i]、wins[i] 只属于 legal[i] 这一手。没有子节点，下完就丢掉那盘棋。
	games := make([]int, len(legal))
	wins := make([]int, len(legal))
	deadline := start.Add(cfg.Think)
	rounds := 0
	for round := 0; round < cfg.PerMove; round++ {
		// 第 0 轮即使已经超时也要跑完，否则有的点一盘都没有，没法比。
		if round > 0 && time.Now().After(deadline) {
			break
		}
		rounds++
		for i, pt := range legal {
			b := root.Clone() // 每次都从根复制，不从上一盘的半路继续
			s.ClearSaved()
			if _, ok := b.Play(pt, s); !ok {
				continue
			}
			blackWon := rollout.Finish(b, s, rng) // 这一手之后的棋全部随机，不再记点
			games[i]++
			// 黑走时黑胜才算胜；白走时要黑没赢才算白胜。
			if (root.Side == engine.Black) == blackWon {
				wins[i]++
			}
		}
	}

	// 胜率最高的下标。一样高时 better 会看谁的样本更多；都一样就保持更小的下标。
	best := 0
	for i := 1; i < len(legal); i++ {
		if better(wins[i], games[i], wins[best], games[best]) {
			best = i
		}
	}
	// 下面只是把账抄出来给页面。选哪一手已经由 best 决定，排序不改变它。
	moves := make([]Stat, len(legal))
	total := 0
	for i, pt := range legal {
		wr := 0.0
		if games[i] > 0 {
			wr = float64(wins[i]) / float64(games[i])
		}
		moves[i] = Stat{Pt: pt, Games: games[i], Wins: wins[i], WinRate: wr}
		total += games[i]
	}
	sortStats(moves) // 胜率从高到低。第一行就是落下的那手
	out.Pt = legal[best]
	out.Pass = false
	out.Games = total // 所有候选的随机局数之和，不是某一手的局数
	if games[best] > 0 {
		out.WinRate = float64(wins[best]) / float64(games[best])
	}
	out.Moves = moves
	out.Rounds = rounds
	out.Elapsed = time.Since(start)
	return out
}

// better 比较两个候选谁该排前面。没有样本的候选永远不赢。
// 胜率用交叉相乘比较，避免浮点把 1/2 和 2/4 比出差别。胜率相同就看谁的样本更多。
func better(wins, games, bestWins, bestGames int) bool {
	if games == 0 {
		return false
	}
	if bestGames == 0 {
		return true
	}
	if wins*bestGames != bestWins*games {
		return wins*bestGames > bestWins*games
	}
	return games > bestGames
}

// sortStats 按 better 从高到低做插入排序。胜率和样本都相同的点保持原来的先后。
func sortStats(moves []Stat) {
	for i := 1; i < len(moves); i++ {
		j := i
		for j > 0 && better(moves[j].Wins, moves[j].Games, moves[j-1].Wins, moves[j-1].Games) {
			moves[j], moves[j-1] = moves[j-1], moves[j]
			j--
		}
	}
}

// legalPoints 列出现在能落子的点。规则在 rollout.Legal。
func legalPoints(b *engine.Board, s *engine.Scratch) []int {
	return rollout.Legal(b, s)
}

// finishRandom 把一盘棋随机下完。规则在 rollout.Finish。
func finishRandom(b *engine.Board, s *engine.Scratch, rng *rand.Rand) bool {
	return rollout.Finish(b, s, rng)
}
