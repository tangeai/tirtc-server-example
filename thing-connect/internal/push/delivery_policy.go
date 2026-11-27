package push

import (
	"bytes"
	"encoding/json"
)

// DeliveryPolicy contains only values shared with the recognition producer.
type DeliveryPolicy struct {
	SourceCapacity   int  `json:"source_capacity" yaml:"source_capacity"`
	DeliveryCapacity int  `json:"delivery_capacity" yaml:"delivery_capacity"`
	WechatRate       int  `json:"wechat_requests_per_second" yaml:"wechat_requests_per_second"`
	Enabled          bool `json:"enabled" yaml:"enabled"`
	TTLSeconds       int  `json:"event_ttl_seconds" yaml:"event_ttl_seconds"`
	SourceAttempts   int  `json:"source_max_attempts" yaml:"source_max_attempts"`
}

func DefaultDeliveryPolicy() DeliveryPolicy {
	return DeliveryPolicy{Enabled: true, TTLSeconds: 120, SourceAttempts: 3, SourceCapacity: 32000, DeliveryCapacity: 6400, WechatRate: 50}
}
func (p DeliveryPolicy) Validate() error {
	if p.SourceCapacity < 32 || p.SourceCapacity > 1000000 || p.DeliveryCapacity < 32 || p.DeliveryCapacity > 1000000 || p.WechatRate < 1 || p.WechatRate > 10000 {
		return ErrInvalid
	}
	if p.TTLSeconds < 30 || p.TTLSeconds > 600 || p.SourceAttempts < 1 || p.SourceAttempts > 10 {
		return ErrInvalid
	}
	return nil
}
func (c Config) Delivery() DeliveryPolicy {
	if c.DeliveryPolicy != nil {
		return *c.DeliveryPolicy
	}
	return DefaultDeliveryPolicy()
}
func ParseDeliveryPolicy(raw json.RawMessage) (DeliveryPolicy, error) {
	p := DefaultDeliveryPolicy()
	if !json.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return p, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return p, ErrInvalid
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return p, ErrInvalid
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, ErrInvalid
	}
	return p, p.Validate()
}
