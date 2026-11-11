package main

import "testing"

// Letters must map to the physical keys the game binds, whatever the case the phone sends.
func TestKeyForChar(t *testing.T) {
	for ch, want := range map[string]int{"o": 31, "O": 31, "n": 45, "1": 18} {
		if got, ok := keyForChar(ch); !ok || got != want {
			t.Errorf("keyForChar(%q) = %d, %v; want %d", ch, got, ok, want)
		}
	}
}
