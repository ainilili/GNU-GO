package rollout

import (
	"testing"

	"weiqi-step/engine"
)

func TestNearQueueReachesDistantLiberty(t *testing.T) {
	b := engine.New(9, 7.5)
	for x := 1; x <= 5; x++ {
		b.Color[b.Index(x, 1)] = engine.Black
		b.Color[b.Index(x, 0)] = engine.White
		b.Color[b.Index(x, 2)] = engine.White
	}
	b.Side = engine.White
	move := b.Index(0, 1)
	far := b.Index(6, 1)
	s := engine.NewScratch(b.N)
	q := NearQueue(b, move, s)
	if !has(q, far) {
		t.Fatalf("queue %v missing %d", q, far)
	}
	if has(q, b.Index(8, 8)) {
		t.Fatal("unrelated corner was near")
	}
}

func has(pts []int, p int) bool {
	for _, q := range pts {
		if q == p {
			return true
		}
	}
	return false
}
