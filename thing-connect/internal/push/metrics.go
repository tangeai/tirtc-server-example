package push

import (
	"context"
	"errors"
	"sync/atomic"
)

// DeliveryMetrics records channel observations without putting a WeChat call in readiness.
type DeliveryMetrics struct {
	Accepted, Permanent, Unknown, Retry, Unsubscribed atomic.Int64
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
	case Retry:
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
	return map[string]int64{"push_wechat_accepted_total": m.Accepted.Load(), "push_wechat_failed_total": m.Permanent.Load(), "push_wechat_unknown_total": m.Unknown.Load(), "push_wechat_retry_total": m.Retry.Load(), "push_wechat_unsubscribed_total": m.Unsubscribed.Load()}
}
