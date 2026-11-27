package recognition

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"thing-connect/internal/push"
)

type UserEvent struct {
	Event    push.Event
	ID       int64
	Lease    string
	Outcome  string
	Checking bool
}
type UserEventQueue interface {
	MayPost(context.Context, UserEvent) (bool, error)
	RefundAuth(context.Context, UserEvent) error
	ClaimProbe(context.Context) (string, error)
	FinishProbe(context.Context, string, bool) error
	ClaimUserEvents(context.Context, int) ([]UserEvent, error)
	FinishUserEvents(context.Context, []UserEvent) error
}
type Publisher struct {
	Queue       UserEventQueue
	BaseURL     string
	InternalKey string
}

func (p Publisher) send(ctx context.Context, v UserEvent) string {
	raw, err := json.Marshal(v.Event)
	if err != nil {
		return "conflict"
	}
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/v1/push/internal/events"
	method := http.MethodPost
	retry := "pending"
	if v.Checking {
		deadline := v.Event.Registered.Add(24 * time.Hour)
		if !time.Now().Before(deadline) {
			return "confirmation_unknown"
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
		retry = "checking"
		method = http.MethodGet
		query := url.Values{"device_id": {v.Event.Device}, "bind_id": {v.Event.Bind}, "source_event_id": {v.Event.ID}}
		endpoint += "?" + query.Encode()
		raw = nil
	} else {
		allowed, err := p.Queue.MayPost(ctx, v)
		if err != nil || !allowed {
			return "withheld"
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, v.Event.Expires)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return retry
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", p.InternalKey)
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return retry
	}
	defer resp.Body.Close()
	var body struct {
		Code int `json:"code"`
		Data struct {
			Outcome string `json:"outcome"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body) != nil {
		return retry
	}
	switch {
	case resp.StatusCode == 429 && body.Code == 42921:
		return "skipped_overload"
	case resp.StatusCode == 403 && body.Code == 40301:
		return "auth_rejected"
	case resp.StatusCode == 404 && body.Code == 40400 && v.Checking:
		return "not_found"
	case resp.StatusCode == 200 && body.Code == 200:
		if body.Data.Outcome == "expired" {
			return "expired"
		}
		if body.Data.Outcome == "skipped_overload" {
			return "skipped_overload"
		}
		return "accepted"
	case resp.StatusCode == 410 && body.Code == 6006:
		return "stale_binding"
	case resp.StatusCode == 410 && body.Code == 41000:
		return "expired"
	case resp.StatusCode == 400 && body.Code == 40000:
		return "invalid"
	case resp.StatusCode == 409 && body.Code == 40901:
		return "conflict"
	}
	return retry
}
func (p Publisher) Run(ctx context.Context) {
	nextProbe := time.Time{}
	for ctx.Err() == nil {
		if time.Now().After(nextProbe) {
			p.probe(ctx)
			nextProbe = time.Now().Add(time.Second)
		}
		batch, err := p.Queue.ClaimUserEvents(ctx, 16)
		if err == nil && len(batch) > 0 {
			var wg sync.WaitGroup
			for i := range batch {
				wg.Add(1)
				go func(v *UserEvent) {
					defer wg.Done()
					v.Outcome = p.send(ctx, *v)
					if v.Outcome == "auth_rejected" {
						if err := p.refund(ctx, *v); err != nil {
							slog.Warn("鉴权暂停事务未确认")
						}
					}
				}(&batch[i])
			}
			wg.Wait()
			finished := batch[:0]
			for _, v := range batch {
				if v.Outcome != "auth_rejected" {
					finished = append(finished, v)
				}
			}
			err = p.finish(ctx, finished)
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("识别通知队列暂不可用")
		}
		if err != nil || len(batch) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func (p Publisher) probe(ctx context.Context) {
	lease, err := p.Queue.ClaimProbe(ctx)
	if err == nil && lease != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/v1/push/internal/auth", nil)
		ok := false
		if err == nil {
			req.Header.Set("X-Internal-Key", p.InternalKey)
			client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err == nil {
				ok = resp.StatusCode == http.StatusNoContent
				resp.Body.Close()
			}
		}
		if err = p.Queue.FinishProbe(ctx, lease, ok); err != nil {
			slog.Warn("鉴权探测回写失败")
		}
	}
}

// Retrying the fenced transaction is safe even after an ambiguous COMMIT.
func (p Publisher) refund(ctx context.Context, v UserEvent) error {
	ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for {
		err := p.Queue.RefundAuth(ack, v)
		if err == nil {
			return nil
		}
		select {
		case <-ack.Done():
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Retry only the local acknowledgement under its original lease.
func (p Publisher) finish(ctx context.Context, batch []UserEvent) error {
	ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for {
		err := p.Queue.FinishUserEvents(ack, batch)
		if err == nil {
			return nil
		}
		select {
		case <-ack.Done():
			return err
		case <-time.After(100 * time.Millisecond):
		}
	}
}
