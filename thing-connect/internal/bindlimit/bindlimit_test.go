package bindlimit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseConfigIsStrict(t *testing.T) {
	p, err := ParseConfig([]byte(DefaultConfigJSON))
	if err != nil || p != (Policy{Window: 10 * time.Minute, MaxFailures: 10, Cooldown: time.Hour}) {
		t.Fatalf("default policy=%+v err=%v", p, err)
	}
	for _, bad := range []string{
		`{}`,
		strings.Replace(DefaultConfigJSON, `"10m"`, `"0s"`, 1),
		strings.Replace(DefaultConfigJSON, `"1h"`, `"25h"`, 1),
		strings.Replace(DefaultConfigJSON, `10,`, `0,`, 1),
		strings.Replace(DefaultConfigJSON, `10,`, `1001,`, 1),
		strings.Replace(DefaultConfigJSON, `"10m"`, `"oops"`, 1),
		strings.TrimSuffix(DefaultConfigJSON, "}") + `,"extra":1}`,
		DefaultConfigJSON + `{}`,
	} {
		if _, err := ParseConfig([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

type scriptedStore struct {
	reserve, fail time.Duration
	reserveErr    error
	failErr       error
	released      int
	failed        int
}

func (s *scriptedStore) Reserve(context.Context, int64, Policy) (time.Duration, error) {
	return s.reserve, s.reserveErr
}
func (s *scriptedStore) Release(context.Context, int64) error { s.released++; return nil }
func (s *scriptedStore) Fail(context.Context, int64, Policy) (time.Duration, error) {
	s.failed++
	return s.fail, s.failErr
}

var errGuess = errors.New("wrong code")
var errOther = errors.New("upstream")

func counts(err error) bool { return errors.Is(err, errGuess) }

func newLimiter(t *testing.T, s Store) *Limiter {
	t.Helper()
	l, err := New(s, Policy{Window: time.Minute, MaxFailures: 3, Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestVerifyRefundsEverythingExceptCountedFailures(t *testing.T) {
	s := &scriptedStore{}
	l := newLimiter(t, s)
	ctx := context.Background()
	if err := l.Verify(ctx, 1, func() error { return nil }, counts); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(ctx, 1, func() error { return errOther }, counts); !errors.Is(err, errOther) {
		t.Fatal(err)
	}
	if err := l.Verify(ctx, 1, func() error { return errGuess }, counts); !errors.Is(err, errGuess) {
		t.Fatal(err)
	}
	if s.released != 2 || s.failed != 1 {
		t.Fatalf("released=%d failed=%d", s.released, s.failed)
	}
}

func TestVerifyLocksBeforeCheckAndOnExhaustingFailure(t *testing.T) {
	ctx := context.Background()
	called := false
	err := newLimiter(t, &scriptedStore{reserve: 90 * time.Second}).Verify(ctx, 1, func() error { called = true; return nil }, counts)
	var locked *LockedError
	if !errors.As(err, &locked) || called || err.Error() != "绑定失败次数过多，请 2 分钟后重试" {
		t.Fatalf("locked reserve: err=%v called=%v", err, called)
	}
	err = newLimiter(t, &scriptedStore{fail: time.Hour}).Verify(ctx, 1, func() error { return errGuess }, counts)
	if !errors.Is(err, ErrLocked) || err.Error() != "绑定失败次数过多，请 60 分钟后重试" {
		t.Fatalf("exhausting failure: %v", err)
	}
}

func TestVerifyFailsClosedWhenReserveFails(t *testing.T) {
	called := false
	err := newLimiter(t, &scriptedStore{reserveErr: errOther}).Verify(context.Background(), 1, func() error { called = true; return nil }, counts)
	if !errors.Is(err, errOther) || errors.Is(err, ErrLocked) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	err = newLimiter(t, &scriptedStore{failErr: errOther}).Verify(context.Background(), 1, func() error { return errGuess }, counts)
	if !errors.Is(err, errGuess) {
		t.Fatalf("unrecorded failure must keep the original result: %v", err)
	}
}

func TestUpdateRejectsInvalidPolicy(t *testing.T) {
	l := newLimiter(t, &scriptedStore{})
	if err := l.Update(Policy{Window: time.Minute, MaxFailures: 0, Cooldown: time.Hour}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if l.Policy().MaxFailures != 3 {
		t.Fatal("invalid update replaced active policy")
	}
	if _, err := New(nil, l.Policy()); err == nil {
		t.Fatal("nil store accepted")
	}
}
