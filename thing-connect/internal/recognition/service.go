package recognition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"
)

var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Service struct {
	store    Store
	provider Provider
	notifier Notifier
	mu       sync.RWMutex
	config   Config
}

func New(store Store, p Provider, c Config) *Service {
	return &Service{store: store, provider: p, config: c}
}
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }
func (s *Service) Configure(c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = c
	return nil
}
func (s *Service) configCopy() Config { s.mu.RLock(); defer s.mu.RUnlock(); return s.config }
func (s *Service) State(c context.Context, a Actor, id string) (State, error) {
	v, e := s.store.State(c, a, id)
	if s.configCopy().APIKey == "" {
		v.EffectiveEnabled = false
	}
	return v, e
}
func (s *Service) Types(c context.Context) ([]Scheme, error) {
	v, e := s.store.Types(c)
	for i := range v {
		v[i] = PublicScheme(v[i])
	}
	return v, e
}
func (s *Service) SetSelection(c context.Context, a Actor, id string, on bool, b []Binding, rev int64) (State, error) {
	if a.UserID <= 0 || a.DeviceID != "" {
		return State{}, ErrForbidden
	}
	if rev < 0 || len(b) > MaxTypes || on && len(b) == 0 {
		return State{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, x := range b {
		if x.ID == "" || x.Version < 1 || seen[x.ID] {
			return State{}, ErrInvalid
		}
		seen[x.ID] = true
	}
	v, e := s.store.SetSelection(c, a, id, on, b, rev)
	if s.configCopy().APIKey == "" {
		v.EffectiveEnabled = false
	}
	return v, e
}
func (s *Service) Submit(c context.Context, a Actor, id string, r Request) (Task, bool, error) {
	if a.DeviceID != id || id == "" {
		return Task{}, false, ErrForbidden
	}
	if !requestID.MatchString(r.ID) || r.Revision < 0 || r.MediaType != "audio" {
		return Task{}, false, ErrInvalid
	}
	data, err := prepareAudio(r)
	if err != nil {
		return Task{}, false, err
	}
	r.Data = data
	r.RejectNew = s.configCopy().APIKey == ""
	sum := sha256.Sum256(r.Data)
	return s.store.Submit(c, a, id, r, hex.EncodeToString(sum[:]))
}
func (s *Service) Get(c context.Context, a Actor, id, rid string) (Task, error) {
	return s.store.Get(c, a, id, rid)
}
func (s *Service) List(c context.Context, a Actor, id string, cursor int64) ([]Task, error) {
	return s.store.List(c, a, id, cursor)
}
func (s *Service) Audio(c context.Context, a Actor, id, rid, item string) ([]byte, error) {
	return s.store.Audio(c, a, id, rid, item)
}

// Run is owned and awaited by the composition root. No request-scoped goroutines.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.step(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("recognition worker operation failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Service) step(ctx context.Context) error {
	if e := s.store.Recover(ctx); e != nil {
		return e
	}
	if s.notifier != nil {
		events, e := s.store.Events(ctx)
		if e != nil {
			return e
		}
		for _, v := range events {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.notifier.Publish(c, v.DeviceID, v.Payload)
			cancel()
			if e = s.store.Deliver(ctx, v, err == nil); e != nil {
				return e
			}
		}
	}
	cfg := s.configCopy()
	if cfg.APIKey == "" {
		return nil
	}
	w, e := s.store.Claim(ctx)
	if errors.Is(e, ErrNotFound) {
		return nil
	}
	if e != nil || w == nil {
		return e
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	done := Completion{Work: *w}
	if w.Stage == "analysis" {
		r, u, err := s.provider.Analyze(callCtx, cfg, w.Scheme, w.Data)
		done.Usage = u
		e = err
		if err == nil {
			done.Result = &r
		}
	} else {
		done.Audio, done.Usage, e = s.provider.Synthesize(callCtx, cfg, w.Scheme, w.InternalResult.Summary)
	}
	done.Usage.Model = w.Scheme.Model
	if w.Stage == "tts" {
		done.Usage.Model = cfg.TTSModel
	}
	done.Failed = e != nil
	done.Unknown = e != nil && !errors.Is(e, ErrInvalid)
	// A received malformed response is a definite failure; transport loss is unknown.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	return s.store.Complete(finishCtx, done)
}
func ValidateResult(raw []byte) (Result, error) {
	var wire struct {
		Summary string         `json:"summary"`
		Alarm   *bool          `json:"alarm"`
		Labels  []string       `json:"labels"`
		Details map[string]any `json:"details"`
	}
	if len(raw) > 16000 || json.Unmarshal(raw, &wire) != nil || wire.Summary == "" || len([]rune(wire.Summary)) > 500 || wire.Alarm == nil || wire.Labels == nil || wire.Details == nil || len(wire.Labels) > 32 {
		return Result{}, ErrInvalid
	}
	return Result{wire.Summary, *wire.Alarm, wire.Labels, wire.Details}, nil
}
