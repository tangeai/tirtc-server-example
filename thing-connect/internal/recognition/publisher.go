package recognition

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"thing-connect/internal/push"
	"time"
)

type UserEvent struct {
	Event        push.Event
	ID           int64
	Lease        string
	Outcome      string
	FinalAttempt int
}
type UserEventQueue interface {
	ClaimUserEvents(context.Context, int) ([]UserEvent, error)
	FinishUserEvents(context.Context, []UserEvent) error
	CleanUserEvents(context.Context) error
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/v1/push/internal/events", bytes.NewReader(raw))
	if err != nil {
		return "pending"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", p.InternalKey)
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "pending"
	}
	defer resp.Body.Close()
	var body struct {
		Code int `json:"code"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body) != nil {
		return "pending"
	}
	switch {
	case resp.StatusCode == 200 && body.Code == 200:
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
	return "pending"
}
func (p Publisher) Run(ctx context.Context) {
	nextClean := time.Now()
	for ctx.Err() == nil {
		if time.Now().After(nextClean) {
			if err := p.Queue.CleanUserEvents(ctx); err != nil {
				slog.Warn("识别通知历史清理失败")
			}
			nextClean = time.Now().Add(time.Minute)
		}
		batch, err := p.Queue.ClaimUserEvents(ctx, 16)
		if err == nil && len(batch) > 0 {
			var wg sync.WaitGroup
			for i := range batch {
				wg.Add(1)
				go func(v *UserEvent) {
					defer wg.Done()
					v.Outcome = p.send(ctx, *v)
					if v.Outcome == "pending" && v.FinalAttempt >= 3 {
						v.Outcome = "confirmation_unknown"
					}
				}(&batch[i])
			}
			wg.Wait()
			err = p.Queue.FinishUserEvents(ctx, batch)
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
