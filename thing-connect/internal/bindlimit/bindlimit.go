// Package bindlimit bounds how many device binding codes one user may guess.
package bindlimit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

// Config is the YAML and Admin JSON form of the policy.
type Config struct {
	Window      string `json:"window" yaml:"window"`
	MaxFailures int    `json:"max_failures" yaml:"max_failures"`
	Cooldown    string `json:"cooldown" yaml:"cooldown"`
}

const DefaultConfigJSON = `{"window":"10m","max_failures":10,"cooldown":"1h"}`

func DefaultConfig() Config {
	var c Config
	_ = json.Unmarshal([]byte(DefaultConfigJSON), &c)
	return c
}

type Policy struct {
	Window      time.Duration
	MaxFailures int
	Cooldown    time.Duration
}

const maxDuration = 24 * time.Hour
const maxFailures = 1000

func ParseConfig(raw []byte) (Policy, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return Policy{}, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Policy{}, errors.New("unexpected trailing bind attempt policy")
	}
	return c.Policy()
}

func (c Config) Policy() (Policy, error) {
	window, err := time.ParseDuration(c.Window)
	if err != nil {
		return Policy{}, fmt.Errorf("window: %w", err)
	}
	cooldown, err := time.ParseDuration(c.Cooldown)
	if err != nil {
		return Policy{}, fmt.Errorf("cooldown: %w", err)
	}
	p := Policy{Window: window, MaxFailures: c.MaxFailures, Cooldown: cooldown}
	return p, p.Validate()
}

func (p Policy) Validate() error {
	if p.Window < time.Second || p.Window > maxDuration {
		return errors.New("window must be between 1s and 24h")
	}
	if p.Cooldown < time.Second || p.Cooldown > maxDuration {
		return errors.New("cooldown must be between 1s and 24h")
	}
	if p.MaxFailures < 1 || p.MaxFailures > maxFailures {
		return fmt.Errorf("max_failures must be between 1 and %d", maxFailures)
	}
	return nil
}

// Store keeps per-user counters. Every method must be atomic per user so that
// concurrent requests cannot exceed the budget.
type Store interface {
	// Reserve returns the remaining cooldown when the user is locked. Otherwise
	// it counts one in-flight attempt and starts the cooldown once the count
	// exceeds MaxFailures.
	Reserve(ctx context.Context, userID int64, p Policy) (time.Duration, error)
	// Release refunds a reservation that turned out not to be a failed guess.
	Release(ctx context.Context, userID int64) error
	// Fail confirms a failed guess and starts the cooldown when the count has
	// reached MaxFailures. It returns the remaining cooldown, if any.
	Fail(ctx context.Context, userID int64, p Policy) (time.Duration, error)
}

var ErrLocked = errors.New("bind attempts locked")

// LockedError carries the remaining cooldown for Retry-After.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string {
	minutes := int((e.RetryAfter + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("绑定失败次数过多，请 %d 分钟后重试", max(minutes, 1))
}

func (e *LockedError) Is(target error) bool { return target == ErrLocked }

type Limiter struct {
	store  Store
	policy atomic.Pointer[Policy]
}

func New(store Store, p Policy) (*Limiter, error) {
	if store == nil {
		return nil, errors.New("bindlimit: store is required")
	}
	l := &Limiter{store: store}
	if err := l.Update(p); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Limiter) Update(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	l.policy.Store(&p)
	return nil
}

func (l *Limiter) Policy() Policy { return *l.policy.Load() }

// Verify runs check within the user's failure budget. Only errors for which
// counts returns true consume the budget; success and other errors are
// refunded. The failure that exhausts the budget returns *LockedError.
func (l *Limiter) Verify(ctx context.Context, userID int64, check func() error, counts func(error) bool) error {
	p := l.Policy()
	retry, err := l.store.Reserve(ctx, userID, p)
	if err != nil {
		return fmt.Errorf("bindlimit reserve: %w", err)
	}
	if retry > 0 {
		return &LockedError{RetryAfter: retry}
	}
	checkErr := check()
	if checkErr == nil || !counts(checkErr) {
		if err := l.store.Release(ctx, userID); err != nil {
			slog.WarnContext(ctx, "bind attempt refund failed", "user_id", userID, "err", err)
		}
		return checkErr
	}
	retry, err = l.store.Fail(ctx, userID, p)
	if err != nil {
		slog.WarnContext(ctx, "bind attempt failure not recorded", "user_id", userID, "err", err)
		return checkErr
	}
	if retry > 0 {
		return &LockedError{RetryAfter: retry}
	}
	return checkErr
}
