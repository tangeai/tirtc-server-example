package backoff

import (
	"testing"
	"time"
)

func TestJitterBounds(t *testing.T) {
	for _, ceiling := range []time.Duration{0, 1, time.Second, 5 * time.Minute} {
		for i := 0; i < 1000; i++ {
			got := Jitter(ceiling)
			if got < ceiling/2 || got > ceiling {
				t.Fatalf("%s outside bounds for %s", got, ceiling)
			}
		}
	}
}
