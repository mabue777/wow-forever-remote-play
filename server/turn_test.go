package main

import (
	"sync"
	"testing"
	"time"
)

// Measures how long the turn key is held per second at a given rate, with key posting replaced by a recorder.
func heldFraction(t *testing.T, rate float64) (left, right float64) {
	var mu sync.Mutex
	down := map[int]time.Time{}
	total := map[int]time.Duration{}
	postKey = func(code int, d bool, _ int) {
		mu.Lock()
		defer mu.Unlock()
		if d {
			down[code] = time.Now()
		} else if at, ok := down[code]; ok {
			total[code] += time.Since(at)
			delete(down, code)
		}
	}
	h := &hub{held: map[int]bool{}}
	go h.turnLoop()
	stop := time.Now().Add(time.Second)
	for time.Now().Before(stop) {
		h.setTurn(rate, turnKey{0, 0}, turnKey{2, 0})
		time.Sleep(100 * time.Millisecond)
	}
	h.stopTurn()
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(down) != 0 {
		t.Errorf("rate %.2f: key still held after stop: %v", rate, down)
	}
	return total[0].Seconds(), total[2].Seconds()
}

func TestTurnPulses(t *testing.T) {
	for _, c := range []struct{ rate, lo, hi float64 }{{0.3, 0.2, 0.42}, {0.6, 0.48, 0.72}, {1, 0.9, 1.1}} {
		_, r := heldFraction(t, c.rate)
		if r < c.lo || r > c.hi {
			t.Errorf("rate %.1f: right held %.2fs of 1s, want %.2f-%.2f", c.rate, r, c.lo, c.hi)
		}
	}
	if l, r := heldFraction(t, -0.5); l < 0.35 || r > 0.01 {
		t.Errorf("rate -0.5: left %.2fs right %.2fs, want left only", l, r)
	}
}
