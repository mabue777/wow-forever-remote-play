package main

import (
	"math"
	"time"
)

// Analog turning. WoW turns at one fixed speed while a turn key is held, so a digital stick overturns.
// The phone sends a turn rate (-1 left .. 1 right); this loop pulses the turn key on the Mac's own clock:
// held for rate x 90 ms of every 90 ms. Light push = slow turn, full push = full speed, and network jitter
// cannot stretch a press. If the phone stops sending for 600 ms the turn stops.

// postKey sends a key to the game; tests swap it for a recorder so nothing reaches a running game.
var postKey = keyEvent

const turnCycle = 90 * time.Millisecond

type turnKey struct{ code, flags int }

func (h *hub) setTurn(rate float64, left, right turnKey) {
	h.turnMu.Lock()
	h.turnRate = math.Max(-1, math.Min(1, rate))
	h.turnLeft, h.turnRight = left, right
	h.turnAt = time.Now()
	h.turnMu.Unlock()
}

func (h *hub) stopTurn() {
	h.turnMu.Lock()
	h.turnRate = 0
	h.turnMu.Unlock()
}

func (h *hub) turnLoop() {
	held := false
	var heldKey turnKey
	release := func() {
		if held {
			h.key(heldKey.code, false, heldKey.flags)
			held = false
		}
	}
	for {
		h.turnMu.Lock()
		rate, left, right, at := h.turnRate, h.turnLeft, h.turnRight, h.turnAt
		h.turnMu.Unlock()
		if time.Since(at) > 600*time.Millisecond {
			rate = 0
		}
		a := math.Abs(rate)
		if a < 0.02 {
			release()
			time.Sleep(15 * time.Millisecond)
			continue
		}
		k := right
		if rate < 0 {
			k = left
		}
		if held && heldKey != k {
			release()
		}
		if a >= 0.92 { // full push: just hold the key
			if !held {
				h.key(k.code, true, k.flags)
				held, heldKey = true, k
			}
			time.Sleep(15 * time.Millisecond)
			continue
		}
		on := time.Duration(a * float64(turnCycle))
		if on < 17*time.Millisecond { // shorter than a game frame would be lost
			on = 17 * time.Millisecond
		}
		h.key(k.code, true, k.flags)
		held, heldKey = true, k
		time.Sleep(on)
		release()
		time.Sleep(turnCycle - on)
	}
}
