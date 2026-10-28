package recognition

import (
	"errors"
	"testing"
	"time"
)

func taskFixture(t *testing.T, modes ...string) Snapshot {
	t.Helper()
	schemes := make([]Scheme, len(modes))
	for i, mode := range modes {
		schemes[i] = Scheme{ID: mode, Version: 1, Enabled: true, OutputMode: mode, Limits: Limits{5 << 20, 10}}
	}
	p := DefaultPolicy()
	p.Enabled = true
	v, err := NewSnapshot("device", Request{ID: "request", Data: wav(1)}, p, schemes, Usage{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTaskOutputModesAndStageOrder(t *testing.T) {
	for _, mode := range []string{"text", "audio", "text_audio"} {
		t.Run(mode, func(t *testing.T) {
			v := taskFixture(t, mode)
			if v.NextStage(0) != "analysis" {
				t.Fatal("analysis must be first")
			}
			v.Start(0, "analysis")
			if v.Task.Status != "processing" || v.Task.Revision != 2 {
				t.Fatal("claim transition")
			}
			result := &Result{Summary: "声音", Labels: []string{}, Details: map[string]any{}}
			v.Complete(Completion{Work: Work{ItemIndex: 0, Stage: "analysis"}, Result: result})
			public := v.PublicTask()
			if (public.Results[0].Result == nil) != (mode == "audio") || v.Internal[0] != result {
				t.Fatal("output visibility or internal speech source")
			}
			if mode == "text" {
				if v.NextStage(0) != "" || v.Task.Status != "succeeded" || public.Results[0].Speech.Status != "skipped" {
					t.Fatal("text must never synthesize")
				}
			} else {
				if v.NextStage(0) != "tts" || v.Task.Status != "queued" {
					t.Fatal("speech must follow successful analysis")
				}
				v.Start(0, "tts")
				v.Complete(Completion{Work: Work{ItemIndex: 0, Stage: "tts"}, Audio: []byte("wav")})
				if v.Task.Status != "succeeded" || v.Task.Results[0].Speech.URL != "/v1/recognition/requests/request/audio/item_1" {
					t.Fatal("speech completion")
				}
			}
		})
	}
}

func TestTaskFailureAndLeaseExpiry(t *testing.T) {
	for _, stage := range []string{"analysis", "tts"} {
		for _, unknown := range []bool{false, true} {
			v := taskFixture(t, "text_audio")
			if stage == "tts" {
				v.Complete(Completion{Work: Work{Stage: "analysis"}, Result: &Result{Summary: "text"}})
			}
			v.Complete(Completion{Work: Work{Stage: stage}, Failed: true, Unknown: unknown})
			want := "failed"
			if stage == "tts" {
				want = "partial_failed"
			}
			if unknown {
				want = "unknown"
			}
			if v.Task.Status != want || v.NextStage(0) != "" {
				t.Fatalf("stage=%s unknown=%v: %+v", stage, unknown, v.Task)
			}
		}
		v := taskFixture(t, "text_audio")
		if stage == "tts" {
			v.Complete(Completion{Work: Work{Stage: "analysis"}, Result: &Result{Summary: "text"}})
		}
		v.Start(0, stage)
		v.Expire()
		if v.Task.Status != "unknown" || v.NextStage(0) != "" {
			t.Fatal("expired work must not be retried")
		}
	}
	v := taskFixture(t, "text", "text_audio")
	v.DisableItem(0)
	if v.Task.Status != "queued" || v.NextStage(1) != "analysis" {
		t.Fatal("disabled item must allow following item")
	}
	v.Cancel()
	if v.Task.Status != "cancelled" || v.NextStage(1) != "" {
		t.Fatal("unbound task must be cancelled")
	}
}

func TestSubmissionPolicyQuotaAndLimits(t *testing.T) {
	p := DefaultPolicy()
	p.Enabled = true
	p.MonthlyLimit = 1
	p.Revision = 2
	req := Request{ID: "request", Revision: 2, Data: wav(1)}
	schemes := []Scheme{{Enabled: true, OutputMode: "text", Limits: Limits{5 << 20, 10}}}
	tests := []struct {
		name   string
		change func(*Policy, *Request, *[]Scheme, *Usage)
		want   error
	}{
		{"disabled", func(p *Policy, _ *Request, _ *[]Scheme, _ *Usage) { p.Enabled = false }, ErrDisabled},
		{"forbidden", func(p *Policy, _ *Request, _ *[]Scheme, _ *Usage) { p.Allowed = false }, ErrDisabled},
		{"stale", func(_ *Policy, r *Request, _ *[]Scheme, _ *Usage) { r.Revision = 1 }, ErrConflict},
		{"quota", func(_ *Policy, _ *Request, _ *[]Scheme, u *Usage) { u.Used = 1 }, ErrQuota},
		{"empty", func(_ *Policy, _ *Request, s *[]Scheme, _ *Usage) { *s = nil }, ErrDisabled},
		{"oversize", func(_ *Policy, r *Request, _ *[]Scheme, _ *Usage) { r.Data = wav(11) }, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, r, s, u := p, req, schemes, Usage{}
			tt.change(&p, &r, &s, &u)
			if _, err := NewSnapshot("d", r, p, s, u, time.Now()); !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}
