// 第 1 步的网页服务。浏览器只提交整盘棋的着手列表，服务自己从空棋盘重放，
// 再决定要不要用平坦蒙特卡洛应一手。没有会话，刷新页面不会丢在服务端的棋。
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"time"

	"weiqi-step/engine"
	"weiqi-step/flat"
	"weiqi-step/rave"
	"weiqi-step/tree"
)

//go:embed web
var webRoot embed.FS // 编译进二进制的页面。改了 HTML 必须重新 go run，运行中的进程看不到新文件。

// main 监听本机 8791，直到进程退出。
func main() {
	addr := flag.String("addr", "127.0.0.1:8791", "listen address")
	flag.Parse()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("围棋 平坦蒙特卡洛 / 搜索树 / UCT+RAVE  http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// newMux 注册两个接口和静态页面。
// POST /api/state 只重放着手，返回局面。POST /api/think 重放之后再让电脑下一手。
func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/state", handleState)
	mux.HandleFunc("POST /api/think", handleThink)
	sub, err := fs.Sub(webRoot, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", noCache(http.FileServer(http.FS(sub))))
	return mux
}

// noCache 让浏览器每次都向进程要页面。嵌入的文件没有单独的更新时间。
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// moveReq 是请求里的一手。Pass 为真时忽略 X、Y，表示停一手。
// X 从左往右，Y 从棋盘上沿往下，都从 0 开始。界面上的行号是 9-Y。
type moveReq struct {
	X    int  `json:"x"`
	Y    int  `json:"y"`
	Pass bool `json:"pass"`
}

// playReq 是 /api/state 和 /api/think 共用的请求体。
// 服务不保存对局。Moves 必须是从空棋盘开始的完整着手，顺序就是落子顺序。
type playReq struct {
	Size    int       `json:"size"`    // 只接受 9。0 也当成 9
	Komi    *float64  `json:"komi"`    // 缺省 7.5。用指针才能区分「没传」和「传了 0」
	PerMove int       `json:"perMove"` // 每个候选的随机局数，只对 /api/think 有效。缺省 8。传了 level 时不用
	Level   int       `json:"level"`   // 我方等级 1 到 10。每级 1000 盘，总盘数不再乘以合法点数
	ThinkMS int       `json:"thinkMs"` // 思考时间上限，毫秒，只对 /api/think 有效。传了 level 时按等级放宽
	Algo    string    `json:"algo"`    // "flat"、"tree" 或 "rave"。空字符串按 flat
	Moves   []moveReq `json:"moves"`
}

// point 是响应里的一个交叉点，坐标含义和 moveReq 相同。
type point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// cand 是搜索结果表里的一行。Wins 属于电脑下这手之前该谁走的那一方。
type cand struct {
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Games   int     `json:"games"`           // 这个点下完的随机局数
	Wins    int     `json:"wins"`            // 其中行棋方获胜的局数
	WinRate float64 `json:"winRate"`         // Wins/Games
	RaveN   int     `json:"raveN,omitempty"` // 这个点的 RAVE 样本数。没有 RAVE 时不传
	Rave    float64 `json:"rave,omitempty"`  // RAVE 胜率。raveN 为 0 时不传
}

// analysis 是电脑这一步的搜索账。只出现在 /api/think 的响应里。
type analysis struct {
	Pass    bool    `json:"pass"`           // 电脑选择停一手
	Move    *point  `json:"move,omitempty"` // 落下的点。停一手时没有这个字段
	Games   int     `json:"games"`          // 所有候选的随机局数之和
	WinRate float64 `json:"winRate"`        // 选中那一手的胜率
	Millis  int64   `json:"millis"`         // 搜索耗时
	Rounds  int     `json:"rounds"`         // 跑完的模拟盘数
	Algo    string  `json:"algo"`           // 产生这份账的算法："flat"、"tree" 或 "rave"
	Top     []cand  `json:"top"`            // 按该算法的选择规则排好，至多 12 手。第一行就是落下的那手
}

// scoreView 是终局数子结果。黑白都是面积，白的面积已经加上贴目。
type scoreView struct {
	Black  float64 `json:"black"` // 黑方面积，不含贴目
	White  float64 `json:"white"` // 白方面积加上贴目
	Komi   float64 `json:"komi"`
	Winner string  `json:"winner"`          // "B"、"W"，和棋是空字符串
	Margin float64 `json:"margin"`          // 胜方领先的目数，和棋是 0
	Owner  [][]int `json:"owner,omitempty"` // 每个点的归属：0 单官，1 黑，2 白。行优先，Y 在外层
}

// stateView 是两个接口共同的响应。错误时不是这个结构，而是 {"error": "..."}。
type stateView struct {
	Size     int        `json:"size"`
	Komi     float64    `json:"komi"`
	ToMove   string     `json:"toMove"`         // "B" 或 "W"。终局是空字符串
	Board    [][]int    `json:"board"`          // 0 空，1 黑，2 白。board[y][x]
	Captures [2]int     `json:"captures"`       // 下标 0 是黑提的子，1 是白提的子
	Passes   int        `json:"passes"`         // 当前连续停手次数
	Ended    bool       `json:"ended"`          // 连续停两次、同一方第二次停手，或轮到的一方没有可下的点。终局都数子
	Resigned bool       `json:"resigned"`       // 保留字段。停手收棋后改数子，不再把停手的一方判负
	Legal    []point    `json:"legal"`          // 现在能落子的点。终局为空
	Last     *point     `json:"last,omitempty"` // 最近一颗实子。连续停手时仍指向上一颗子
	Score    *scoreView `json:"score,omitempty"`
	Analysis *analysis  `json:"analysis,omitempty"`
}

// handleState 重放请求里的着手并返回局面，不搜索。
func handleState(w http.ResponseWriter, r *http.Request) {
	pos, err := decodeBoard(w, r)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, makeState(pos, nil))
}

// handleThink 重放之后，如果棋还没结束，就用平坦蒙特卡洛应一手，再把下完的局面返回。
// 没传等级时，思考时间限制在 200 毫秒到 8 秒，每个候选缺省 8 盘。
// 传了等级时，总盘数是等级乘 1000，思考时间按每级 20 秒放宽，最多 3 分钟。
func handleThink(w http.ResponseWriter, r *http.Request) {
	pos, err := decodeBoard(w, r)
	if err != nil {
		return
	}
	b := pos.board
	if b.Over() || pos.stop || len(legalPoints(b, engine.NewScratch(b.N))) == 0 {
		// 没有可下的点时直接数子，不停一手，也不再搜索。
		writeJSON(w, http.StatusOK, makeState(pos, nil))
		return
	}
	per := pos.req.PerMove
	sims := 0
	ms := pos.req.ThinkMS
	if pos.req.Level > 0 {
		level := pos.req.Level
		if level > 10 {
			level = 10
		}
		sims = level * 1000
		ms = level * 20000
		if ms > 180000 {
			ms = 180000
		}
	} else {
		if per <= 0 {
			per = 8
		}
		if ms <= 0 {
			ms = 2000
		}
		if ms < 200 {
			ms = 200
		}
		if ms > 8000 {
			ms = 8000
		}
	}
	mover := b.Side
	res, err := runSearch(b, pos.req.Algo, per, sims, time.Duration(ms)*time.Millisecond)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "算法只能是 flat、tree 或 rave")
		return
	}
	an := toAnalysis(b, res)
	s := engine.NewScratch(b.N)
	pt := -1
	if !res.pass {
		pt = res.pt
	}
	// 搜索承诺这手合法。真的下不上就返回 500，不能把非法点写进响应。
	if _, ok := b.Play(pt, s); !ok {
		writeErr(w, http.StatusInternalServerError, "搜索结果无法落子")
		return
	}
	var played *point
	if pt >= 0 {
		x, y := b.XY(pt)
		played = &point{X: x, Y: y}
		an.Move = played
		an.Pass = false
	} else {
		an.Pass = true
		an.Move = nil
		played = pos.last // 停一手没有新的最后落子点，标记仍留在上一颗子上
		notePass(&pos, mover)
	}
	pos.last = played
	log.Printf("%s %s games=%d rounds=%d ms=%d wr=%.3f", res.algo, mover.Letter(), an.Games, an.Rounds, an.Millis, an.WinRate)
	writeJSON(w, http.StatusOK, makeState(pos, &an))
}

// scored 是两种搜索共同交回来的结果。Moves 已按该算法的选择规则排好。
type scored struct {
	algo    string
	pass    bool
	pt      int
	games   int
	winRate float64
	elapsed time.Duration
	rounds  int
	moves   []moveStat
}

type moveStat struct {
	pt      int
	games   int
	wins    int
	winRate float64
	raveN   int
	rave    float64
}

// runSearch 按名字调用算法。空字符串就是平坦蒙特卡洛。
func runSearch(b *engine.Board, algo string, per, sims int, think time.Duration) (scored, error) {
	switch algo {
	case "", "flat":
		res := flat.Search(b, flat.Config{PerMove: per, Sims: sims, Think: think})
		out := scored{algo: "flat", pass: res.Pass, pt: res.Pt, games: res.Games, winRate: res.WinRate, elapsed: res.Elapsed, rounds: res.Rounds}
		for _, m := range res.Moves {
			out.moves = append(out.moves, moveStat{pt: m.Pt, games: m.Games, wins: m.Wins, winRate: m.WinRate})
		}
		return out, nil
	case "tree":
		res := tree.Search(b, tree.Config{PerMove: per, Sims: sims, Think: think})
		out := scored{algo: "tree", pass: res.Pass, pt: res.Pt, games: res.Games, winRate: res.WinRate, elapsed: res.Elapsed, rounds: res.Rounds}
		for _, m := range res.Moves {
			out.moves = append(out.moves, moveStat{pt: m.Pt, games: m.Games, wins: m.Wins, winRate: m.WinRate})
		}
		return out, nil
	case "rave":
		res := rave.Search(b, rave.Config{PerMove: per, Sims: sims, Think: think})
		out := scored{algo: "rave", pass: res.Pass, pt: res.Pt, games: res.Games, winRate: res.WinRate, elapsed: res.Elapsed, rounds: res.Rounds}
		for _, m := range res.Moves {
			out.moves = append(out.moves, moveStat{pt: m.Pt, games: m.Games, wins: m.Wins, winRate: m.WinRate, raveN: m.RaveN, rave: m.Rave})
		}
		return out, nil
	default:
		return scored{}, fmt.Errorf("algo")
	}
}

// toAnalysis 把搜索结果收成页面用的表。只留前 12 行，第一行是落下的那手。
func toAnalysis(b *engine.Board, res scored) analysis {
	an := analysis{
		Pass:    res.pass,
		Games:   res.games,
		WinRate: res.winRate,
		Millis:  res.elapsed.Milliseconds(),
		Rounds:  res.rounds,
		Algo:    res.algo,
		Top:     []cand{},
	}
	limit := 12
	for _, m := range res.moves {
		if len(an.Top) >= limit {
			break
		}
		x, y := b.XY(m.pt)
		an.Top = append(an.Top, cand{X: x, Y: y, Games: m.games, Wins: m.wins, WinRate: m.winRate, RaveN: m.raveN, Rave: m.rave})
	}
	if !res.pass && res.pt >= 0 && res.pt < b.N {
		x, y := b.XY(res.pt)
		an.Move = &point{X: x, Y: y}
	}
	return an
}

// position 是重放完的局面。stop 表示同一方已经第二次停手，对局结束，接下来数子。
type position struct {
	req   playReq
	board *engine.Board
	last  *point
	passN [3]int
	stop  bool
}

// notePass 记下这一方主动停手。双方连续各停一次时，Play 已经把连续次数加成 2。
// 中间对手下过子、同一方再停一次，连续次数不到 2，这里照样收棋，胜负交给数子。
func notePass(pos *position, side engine.Color) {
	pos.passN[side]++
	if !pos.board.Over() && pos.passN[side] >= 2 {
		pos.stop = true
	}
}

// decodeBoard 读请求，从空棋盘重放全部着手。
// 出错时已经写了 HTTP 错误，调用方看到 err != nil 就直接返回。
// 同一方第二次停手后对局已经结束，不再重放后面的着手。
func decodeBoard(w http.ResponseWriter, r *http.Request) (position, error) {
	var req playReq
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "请求不是合法的 JSON")
		return position{}, err
	}
	size := req.Size
	if size == 0 {
		size = 9
	}
	if size != 9 {
		err := fmt.Errorf("size")
		writeErr(w, http.StatusBadRequest, "第 1 步只下 9 路")
		return position{}, err
	}
	komi := 7.5
	if req.Komi != nil {
		komi = *req.Komi
	}
	if math.IsNaN(komi) || math.IsInf(komi, 0) || komi < -50 || komi > 50 {
		err := fmt.Errorf("komi")
		writeErr(w, http.StatusBadRequest, "贴目不合法")
		return position{}, err
	}
	// 正常对局远到不了棋盘点数的四倍。这个上限只用来挡住过大的请求。
	if len(req.Moves) > size*size*4 {
		err := fmt.Errorf("moves")
		writeErr(w, http.StatusBadRequest, "手数过多")
		return position{}, err
	}
	pos := position{req: req, board: engine.New(size, komi)}
	b := pos.board
	s := engine.NewScratch(b.N)
	for i, m := range req.Moves {
		if b.Over() || pos.stop {
			break
		}
		pt := -1
		if !m.Pass {
			if m.X < 0 || m.Y < 0 || m.X >= size || m.Y >= size {
				err := fmt.Errorf("range")
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("第 %d 手坐标越界", i+1))
				return position{}, err
			}
			pt = b.Index(m.X, m.Y)
		}
		side := b.Side
		if _, ok := b.Play(pt, s); !ok {
			err := fmt.Errorf("illegal")
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("第 %d 手非法", i+1))
			return position{}, err
		}
		if m.Pass {
			notePass(&pos, side)
		} else {
			pos.last = &point{X: m.X, Y: m.Y}
		}
	}
	return pos, nil
}

// makeState 把棋盘收成响应。终局才数子，并且不再列合法点。an 为 nil 表示这次没有搜索。
// 连续停两次、同一方第二次停手，都用中国规则数子定输赢。
// 轮到的一方没有任何可下的点时，也不停一手，当场数子定输赢。
func makeState(pos position, an *analysis) stateView {
	b := pos.board
	st := stateView{
		Size:     b.Size,
		Komi:     b.Komi,
		ToMove:   b.Side.Letter(),
		Board:    grid(b),
		Captures: [2]int{b.Captures[engine.Black], b.Captures[engine.White]},
		Passes:   b.Passes,
		Ended:    b.Over() || pos.stop,
		Legal:    []point{},
		Last:     pos.last,
		Analysis: an,
	}
	s := engine.NewScratch(b.N)
	if st.Ended {
		st.Score = scoreOf(b, s)
		st.ToMove = ""
		return st
	}
	pts := legalPoints(b, s)
	if len(pts) == 0 {
		st.Ended = true
		st.Score = scoreOf(b, s)
		st.ToMove = ""
		return st
	}
	for _, p := range pts {
		x, y := b.XY(p)
		st.Legal = append(st.Legal, point{X: x, Y: y})
	}
	return st
}

// legalPoints 列出能落子的点，试下后立刻退回。和 flat.legalPoints 同一规则，这里不引用未导出的那个函数。
func legalPoints(b *engine.Board, s *engine.Scratch) []int {
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

// scoreOf 数终局。Winner 用领先的一方，Margin 改成正数，方便页面直接显示「谁胜几目」。
func scoreOf(b *engine.Board, s *engine.Scratch) *scoreView {
	owner := make([]engine.Color, b.N)
	black, white := b.Areas(s, owner)
	margin := float64(black-white) - b.Komi
	sc := &scoreView{
		Black:  float64(black),
		White:  float64(white) + b.Komi,
		Komi:   b.Komi,
		Margin: margin,
		Owner:  make([][]int, b.Size),
	}
	if margin > 0 {
		sc.Winner = "B"
	} else if margin < 0 {
		sc.Winner = "W"
		sc.Margin = -margin
	}
	for y := 0; y < b.Size; y++ {
		row := make([]int, b.Size)
		for x := 0; x < b.Size; x++ {
			row[x] = int(owner[b.Index(x, y)])
		}
		sc.Owner[y] = row
	}
	return sc
}

// grid 把一维棋子颜色换成 board[y][x]，数值仍是 0 空、1 黑、2 白。
func grid(b *engine.Board) [][]int {
	g := make([][]int, b.Size)
	for y := 0; y < b.Size; y++ {
		row := make([]int, b.Size)
		for x := 0; x < b.Size; x++ {
			row[x] = int(b.Color[b.Index(x, y)])
		}
		g[y] = row
	}
	return g
}

// writeErr 返回 {"error": msg}。页面用 error 字段显示这句中文。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeJSON 按 UTF-8 JSON 写出响应，不把中文转成 \uXXXX。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
