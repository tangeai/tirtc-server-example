package ai

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nameLookup func(context.Context, string) (string, error)

func (f nameLookup) DevicePluginName(ctx context.Context, id string) (string, error) {
	return f(ctx, id)
}

func TestPluginNamesPreserveReferencesOnPartialFailure(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	source := nameLookup(func(_ context.Context, id string) (string, error) {
		mu.Lock()
		calls[id]++
		mu.Unlock()
		if id == "failed" {
			return "private error", errors.New("upstream failed")
		}
		if id == "missing" {
			return "", nil
		}
		return "设备呼叫", nil
	})
	got := ResolvePluginNames(context.Background(), source, []string{"call", "failed", "", "missing", "call"})
	want := []PluginRef{{"call", "设备呼叫"}, {"failed", ""}, {"missing", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(calls, map[string]int{"call": 1, "failed": 1, "missing": 1}) {
		t.Fatalf("unexpected lookups: %v", calls)
	}
}

func TestPluginNamesRespectCancellationAndBoundConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	started := make(chan struct{}, 4)
	source := nameLookup(func(ctx context.Context, _ string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	done := make(chan []PluginRef, 1)
	go func() { done <- ResolvePluginNames(ctx, source, ids) }()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("lookup workers did not start")
		}
	}
	cancel()
	select {
	case refs := <-done:
		if len(refs) != len(ids) || calls.Load() != 4 {
			t.Fatalf("lost references or unbounded calls: refs=%v calls=%d", refs, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("lookup workers did not stop on cancellation")
	}
}

func TestPluginNamesEmptyOrAlreadyCancelled(t *testing.T) {
	source := nameLookup(func(context.Context, string) (string, error) { t.Error("unexpected lookup"); return "", nil })
	if refs := ResolvePluginNames(context.Background(), source, nil); refs == nil || len(refs) != 0 {
		t.Fatalf("expected empty array, got %v", refs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if refs := ResolvePluginNames(ctx, source, []string{"linked"}); len(refs) != 1 || refs[0].ID != "linked" {
		t.Fatalf("lost linked ID: %v", refs)
	}
}
