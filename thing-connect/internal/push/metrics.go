package push

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// DeliveryMetrics records channel observations without putting a WeChat call in readiness.
type DeliveryMetrics struct {
	Accepted, Permanent, Unknown, Retry, Unsubscribed atomic.Int64
	latencyMu                                         sync.Mutex
	durations                                         [512]int64
	samples                                           uint64
	lastSendMs                                        atomic.Int64
	lastFailure                                       atomic.Bool
}

func (m *DeliveryMetrics) Observe(result Result) {
	switch result {
	case Accepted:
		m.Accepted.Add(1)
		m.lastFailure.Store(false)
	case Failed:
		m.Permanent.Add(1)
		m.lastFailure.Store(true)
	case Unknown:
		m.Unknown.Add(1)
		m.lastFailure.Store(true)
	case Retry, Limited:
		m.Retry.Add(1)
		m.lastFailure.Store(true)
	case Unsubscribed:
		m.Unsubscribed.Add(1)
	}
}
func (m *DeliveryMetrics) Health(context.Context) error {
	if m.lastFailure.Load() {
		return errors.New("微信投递失败，等待渠道成功回执")
	}
	return nil
}
func (m *DeliveryMetrics) Metrics() map[string]int64 {
	values := map[string]int64{"push_wechat_last_send_ms": m.lastSendMs.Load(), "push_wechat_accepted_total": m.Accepted.Load(), "push_wechat_failed_total": m.Permanent.Load(), "push_wechat_unknown_total": m.Unknown.Load(), "push_wechat_retry_total": m.Retry.Load(), "push_wechat_unsubscribed_total": m.Unsubscribed.Load()}
	m.latencyMu.Lock()
	n := min(m.samples, uint64(len(m.durations)))
	sorted := append([]int64(nil), m.durations[:n]...)
	m.latencyMu.Unlock()
	if n > 0 {
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for label, p := range map[string]uint64{"p50": 50, "p95": 95, "p99": 99} {
			values["push_wechat_accepted_"+label+"_ms"] = sorted[(n*p+99)/100-1]
		}
	}
	return values
}
func (m *DeliveryMetrics) observeLatency(d Delivery, result Result, elapsed time.Duration) {
	m.lastSendMs.Store(elapsed.Milliseconds())
	if result != Accepted || d.Occurred.IsZero() {
		return
	}
	m.latencyMu.Lock()
	m.durations[m.samples%uint64(len(m.durations))] = time.Since(d.Occurred).Milliseconds()
	m.samples++
	m.latencyMu.Unlock()
}
