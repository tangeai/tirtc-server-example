package installer

import (
	"context"
	"errors"
	"testing"
)

func TestStandardProberPreservesRedisCancellationCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewStandardProber().Probe(ctx, testDraft())
	if !errors.Is(err, ErrRedisUnavailable) {
		t.Fatalf("missing Redis business error: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("missing underlying cancellation: %v", err)
	}
}
