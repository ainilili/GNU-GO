package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"weiqi-step/engine"
)

// TestStateEmpty 确认空棋盘是 9 路、黑先，并且有合法点。
func TestStateEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/state", bytes.NewBufferString(`{"size":9}`))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Size != 9 || st.ToMove != "B" || len(st.Legal) == 0 || st.Ended {
		t.Fatalf("%+v", st)
	}
}

// TestThinkPlaysOneMove 先放一颗黑子，再让接口应一手白棋，响应里要带搜索表。
func TestThinkPlaysOneMove(t *testing.T) {
	body := `{"size":9,"perMove":1,"thinkMs":1500,"moves":[{"x":2,"y":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/think", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.ToMove != "B" || st.Analysis == nil || st.Analysis.Pass {
		t.Fatalf("%+v", st.Analysis)
	}
	if st.Board[st.Analysis.Move.Y][st.Analysis.Move.X] != 2 {
		t.Fatal("white stone missing")
	}
	if st.Analysis.Rounds != 1 || len(st.Analysis.Top) == 0 {
		t.Fatalf("%+v", st.Analysis)
	}
}

// noMoveGame 下出一盘轮到的一方已经没有合法点、又还没连续停两次的棋。
// 优先下不提子的点，固定种子，92 手以内走到这种局面。
func noMoveGame(t *testing.T) (string, *engine.Board) {
	t.Helper()
	b := engine.New(9, 7.5)
	s := engine.NewScratch(b.N)
	rng := rand.New(rand.NewSource(1))
	var body bytes.Buffer
	body.WriteString(`{"size":9,"perMove":1,"thinkMs":500,"moves":[`)
	first := true
	for n := 0; n < 324; n++ {
		pts := legalPoints(b, s)
		if len(pts) == 0 {
			break
		}
		quiet := pts[:0]
		for _, p := range pts {
			u, ok := b.Play(p, s)
			if !ok {
				t.Fatal("probe")
			}
			caps := u.CapEnd - u.CapStart
			b.Undo(u, s)
			if caps == 0 {
				quiet = append(quiet, p)
			}
		}
		cands := quiet
		if len(cands) == 0 {
			cands = pts
		}
		pt := cands[rng.Intn(len(cands))]
		if _, ok := b.Play(pt, s); !ok {
			t.Fatal("play")
		}
		x, y := b.XY(pt)
		if !first {
			body.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&body, `{"x":%d,"y":%d}`, x, y)
	}
	if len(legalPoints(b, s)) != 0 || b.Over() {
		t.Fatal("generator did not reach a stuck side")
	}
	body.WriteString(`]}`)
	return body.String(), b
}

// TestNoLegalMoveScoresImmediately 确认没有可下的点时当场数子，不记成停一手。
func TestNoLegalMoveScoresImmediately(t *testing.T) {
	raw, b := noMoveGame(t)
	want := scoreOf(b, engine.NewScratch(b.N))

	stateReq := httptest.NewRequest(http.MethodPost, "/api/state", bytes.NewBufferString(raw))
	stateRR := httptest.NewRecorder()
	newMux().ServeHTTP(stateRR, stateReq)
	if stateRR.Code != http.StatusOK {
		t.Fatal(stateRR.Code, stateRR.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(stateRR.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Ended || st.Resigned || st.Score == nil || st.Score.Winner != want.Winner || st.ToMove != "" || st.Passes != 0 || len(st.Legal) != 0 {
		t.Fatalf("%+v score=%+v want=%s", st, st.Score, want.Winner)
	}

	thinkReq := httptest.NewRequest(http.MethodPost, "/api/think", bytes.NewBufferString(raw))
	thinkRR := httptest.NewRecorder()
	newMux().ServeHTTP(thinkRR, thinkReq)
	if thinkRR.Code != http.StatusOK {
		t.Fatal(thinkRR.Code, thinkRR.Body.String())
	}
	var thought stateView
	if err := json.Unmarshal(thinkRR.Body.Bytes(), &thought); err != nil {
		t.Fatal(err)
	}
	if thought.Score == nil || !thought.Ended || thought.Resigned || thought.Analysis != nil || thought.Passes != 0 || thought.Score.Winner != want.Winner {
		t.Fatalf("%+v", thought)
	}
}

// TestSecondPassScores 确认同一方第二次停手收棋并数子。
// 白棋是第二次停手的一方，贴目之后白领先，不能再把白棋判负。
func TestSecondPassScores(t *testing.T) {
	body := `{"size":9,"moves":[{"x":0,"y":0},{"x":8,"y":8},{"x":1,"y":0},{"pass":true},{"x":1,"y":1},{"pass":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/state", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Ended || st.Resigned || st.Score == nil || st.Score.Winner != "W" || st.Score.Margin != 5.5 || st.ToMove != "" || len(st.Legal) != 0 {
		t.Fatalf("%+v score=%+v", st, st.Score)
	}
}

// TestConsecutivePassesScore 确认双方各停一次按数子收棋，不判停手的一方负。
func TestConsecutivePassesScore(t *testing.T) {
	body := `{"size":9,"moves":[{"x":0,"y":0},{"x":8,"y":8},{"pass":true},{"pass":true}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/state", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Ended || st.Resigned || st.Score == nil || st.Score.Winner != "W" {
		t.Fatalf("%+v score=%+v", st, st.Score)
	}
}

// TestThinkTreeUsesTree 确认页面可以改用搜索树，响应里标明算法。
func TestThinkTreeUsesTree(t *testing.T) {
	body := `{"size":9,"algo":"tree","perMove":1,"thinkMs":1500,"moves":[{"x":2,"y":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/think", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Analysis == nil || st.Analysis.Algo != "tree" || st.Analysis.Pass || len(st.Analysis.Top) == 0 {
		t.Fatalf("%+v", st.Analysis)
	}
}

// TestThinkRaveUsesRave 确认页面可以改用 UCT+RAVE，响应里带 RAVE 样本。
func TestThinkRaveUsesRave(t *testing.T) {
	body := `{"size":9,"algo":"rave","perMove":1,"thinkMs":1500,"moves":[{"x":2,"y":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/think", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var st stateView
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Analysis == nil || st.Analysis.Algo != "rave" || st.Analysis.Pass || len(st.Analysis.Top) == 0 || st.Analysis.Top[0].RaveN == 0 {
		t.Fatalf("%+v", st.Analysis)
	}
}

// TestRejectsUnknownAlgo 确认不认识的算法不会被当成平坦蒙特卡洛。
func TestRejectsUnknownAlgo(t *testing.T) {
	body := `{"size":9,"algo":"pattern","moves":[{"x":2,"y":2}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/think", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestGnuGoAssetsAreServed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/gnugo/gnugo.js", nil)
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte("GnuGo")) {
		t.Fatalf("%d %d", rr.Code, rr.Body.Len())
	}
}

// TestRejectsOtherSizes 确认第 1 步拒绝 9 路以外的棋盘。
func TestRejectsOtherSizes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/state", bytes.NewBufferString(`{"size":19}`))
	rr := httptest.NewRecorder()
	newMux().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Code)
	}
}
