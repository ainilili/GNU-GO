package engine

import "testing"

func TestUnconditionalEyeAndBoardUntouched(t *testing.T) {
	b := New(7, 7.5)
	black := [][2]int{
		{1, 2}, {2, 2}, {3, 2}, {4, 2}, {5, 2},
		{1, 3}, {3, 3}, {5, 3},
		{1, 4}, {2, 4}, {3, 4}, {4, 4}, {5, 4},
	}
	eyes := map[[2]int]bool{{2, 3}: true, {4, 3}: true}
	for _, p := range black {
		b.Color[b.Index(p[0], p[1])] = Black
	}
	for _, p := range black {
		x, y := p[0], p[1]
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if nx < 0 || ny < 0 || nx >= b.Size || ny >= b.Size {
				continue
			}
			i := b.Index(nx, ny)
			if b.Color[i] == Empty && !eyes[[2]int{nx, ny}] {
				b.Color[i] = White
			}
		}
	}
	eye := b.Index(2, 3)
	before := b.String()
	side, ko, passes := b.Side, b.Ko, b.Passes
	settled := Unconditional(b)
	if b.String() != before || b.Side != side || b.Ko != ko || b.Passes != passes {
		t.Fatal("unconditional life changed the board")
	}
	if settled[eye] != Black {
		t.Fatalf("eye settled %d", settled[eye])
	}
	if settled[b.Index(4, 3)] != Black {
		t.Fatal("second eye not settled")
	}
	if settled[b.Index(1, 2)] != Black {
		t.Fatal("live stone not settled")
	}
	if settled[b.Index(0, 0)] != Empty {
		t.Fatal("outside point was settled")
	}
}

func TestSettledPointIgnoresNeighbor(t *testing.T) {
	b := New(3, 0.5)
	b.Color[b.Index(1, 1)] = White
	if !b.McWhiteWins() {
		t.Fatal("baseline should be white")
	}
	settled := make([]Color, b.N)
	for i := range settled {
		settled[i] = Black
	}
	if b.McWhiteWinsWith(settled) {
		t.Fatal("settled black should win")
	}
}
