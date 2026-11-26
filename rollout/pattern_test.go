package rollout

import (
	"math/rand"
	"testing"

	"weiqi-step/engine"
)

func TestPatternFileLoads(t *testing.T) {
	ps := Patterns()
	if len(ps) != 23 {
		t.Fatalf("rows=%d", len(ps))
	}
	if ps[0].value != 0 || ps[0].shape != [3]string{"oOo", "O*O", "oO?"} {
		t.Fatalf("first %+v", ps[0])
	}
	if ps[3].value != 10000 || len(ps[3].need) != 3 {
		t.Fatalf("capture %+v", ps[3])
	}
	last := ps[len(ps)-1]
	if last.value != 1 || last.shape != [3]string{"??%", "?*%", "%%%"} || len(last.need) != 0 {
		t.Fatalf("default %+v", last)
	}
}

func TestEyeWeightIsZero(t *testing.T) {
	b := engine.New(5, 7.5)
	b.Color[b.Index(1, 0)] = engine.Black
	b.Color[b.Index(0, 1)] = engine.Black
	b.Color[b.Index(1, 1)] = engine.Black
	b.Side = engine.Black
	s := engine.NewScratch(b.N)
	if w := Weight(b, b.Index(0, 0), -1, s); w != 0 {
		t.Fatalf("eye weight %d", w)
	}
}

func TestAtariCaptureOutweighsEmptyPoint(t *testing.T) {
	b := engine.New(9, 7.5)
	b.Color[b.Index(0, 0)] = engine.Black
	b.Color[b.Index(1, 0)] = engine.White
	b.Side = engine.White
	s := engine.NewScratch(b.N)
	last := b.Index(0, 0)
	cap := Weight(b, b.Index(0, 1), last, s)
	far := Weight(b, b.Index(8, 8), last, s)
	if cap != 10000 || far != 1 {
		t.Fatalf("capture %d far %d", cap, far)
	}
}

// TestQuietTableAgrees 确认安静点的查表权重和逐行比对相同。
func TestQuietTableAgrees(t *testing.T) {
	ps := Patterns()
	rng := rand.New(rand.NewSource(3))
	var nb [3][3]cell
	nb[1][1] = cEmpty
	for n := 0; n < 2000; n++ {
		for y := 0; y < 3; y++ {
			for x := 0; x < 3; x++ {
				if x == 1 && y == 1 {
					continue
				}
				nb[y][x] = cell(rng.Intn(4))
			}
		}
		for _, near := range []bool{false, true} {
			got := patternWeight(nb, near, true, 0, 0)
			want := matchValue(ps, nb, near, true, 0, 0)
			if got != want {
				t.Fatalf("near %v got %d want %d", near, got, want)
			}
		}
	}
}

// TestAtariCaptureRequiresSingleLiberty 两口气的邻块不能算提子。只剩一口气才算。
func TestAtariCaptureRequiresSingleLiberty(t *testing.T) {
	b := engine.New(9, 7.5)
	b.Color[b.Index(1, 0)] = engine.White
	b.Color[b.Index(2, 0)] = engine.Black
	b.Color[b.Index(0, 1)] = engine.Black
	s := engine.NewScratch(b.N)
	pt := b.Index(0, 0)
	if n := b.AtariCapture(pt, engine.White, s); n != 0 {
		t.Fatalf("open side counted %d", n)
	}
	b.Color[b.Index(1, 1)] = engine.Black
	if n := b.AtariCapture(pt, engine.White, s); n != 1 {
		t.Fatalf("single liberty %d", n)
	}
}

// TestShapeMatchesWeight 连续落子后，增量权重和整盘重算一致。
func TestShapeMatchesWeight(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for game := 0; game < 4; game++ {
		b := engine.New(9, 7.5)
		s := engine.NewScratch(b.N)
		var sh Shape
		sh.rebuild(b, s)
		last := -1
		for step := 0; step < 160 && b.Passes < 2; step++ {
			for _, c := range []engine.Color{engine.Black, engine.White} {
				for p := 0; p < b.N; p++ {
					got := sh.far[c][p]
					want := weightFor(b, p, -1, c, s)
					if got != want {
						t.Fatalf("game %d step %d color %s pt %d got %d want %d", game, step, c.Letter(), p, got, want)
					}
				}
			}
			for p := 0; p < b.N; p++ {
				if got, want := sh.weight(b, p, last, b.Side, s), Weight(b, p, last, s); got != want {
					t.Fatalf("near game %d step %d pt %d got %d want %d", game, step, p, got, want)
				}
			}
			pt, ok := sh.Play(b, s, rng, last)
			if !ok {
				t.Fatalf("game %d step %d play", game, step)
			}
			if pt >= 0 {
				last = pt
			}
		}
	}
}

