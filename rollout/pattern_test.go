package rollout

import (
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
