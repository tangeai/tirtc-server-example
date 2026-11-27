// Package push owns user notification admission and delivery contracts.
package push

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const AlarmTemplate = "rqexMc0m1O7rVv08lXnQ-txMF_Z9v8E4VPsXeP6XxrY"

var ErrInvalid = errors.New("通知参数无效")
var ErrForbidden = errors.New("无权访问此设备")
var ErrConflict = errors.New("事件内容冲突")
var ErrExpired = errors.New("事件已过期")
var ErrBinding = errors.New("绑定关系已变化")
var sourceID = regexp.MustCompile("^[A-Za-z0-9_-]{1,64}:(alarm|crying|quarrel)$")

type Event struct {
	Expires     time.Time `json:"expires_at,omitempty"`
	MaxAttempts int       `json:"-"`
	LabelText   string    `json:"label_text,omitempty"`
	ID          string    `json:"source_event_id" db:"source_event_id"`
	Device      string    `json:"device_id" db:"device_id"`
	Bind        string    `json:"bind_id" db:"bind_id"`
	Kind        string    `json:"event_type" db:"event_type"`
	Registered  time.Time `json:"registered_at" db:"registered_at"`
	Occurred    time.Time `json:"occurred_at" db:"occurred_at"`
}

func (e Event) Validate() error {
	if len([]rune(e.LabelText)) > 256 || !sourceID.MatchString(e.ID) || len(e.Device) == 0 || len(e.Device) > 64 || len(e.Bind) == 0 || len(e.Bind) > 128 || e.Registered.IsZero() {
		return ErrInvalid
	}
	if !strings.HasSuffix(e.ID, ":"+e.Kind) {
		return ErrInvalid
	}
	return nil
}

type Config struct {
	DeliveryPolicy *DeliveryPolicy `yaml:"delivery_policy"`
	AlarmHint      string          `yaml:"alarm_hint"`
	Notifications  *Policy         `yaml:"notifications"`
	ServerURL      string          `yaml:"server_url"`
	TemplateID     string          `yaml:"template_id"`
	Location       string          `yaml:"location"`
	Crying         string          `yaml:"crying"`
	Quarrel        string          `yaml:"quarrel"`
}

// UnmarshalJSON permits legacy missing or malformed display times. The producer
// registration time remains mandatory for deduplication and reconciliation.
func (e *Event) UnmarshalJSON(data []byte) error {
	type rawEvent Event
	var v struct {
		rawEvent
		Display json.RawMessage `json:"occurred_at"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*e = Event(v.rawEvent)
	var occurred time.Time
	if json.Unmarshal(v.Display, &occurred) == nil {
		e.Occurred = occurred
	}
	return nil
}

func (c Config) Validate() error {
	if c.DeliveryPolicy != nil {
		if err := c.DeliveryPolicy.Validate(); err != nil {
			return err
		}
	}
	if c.Notifications != nil {
		if err := c.Notifications.Validate(); err != nil {
			return err
		}
	}
	if c.ServerURL != "" {
		u, err := url.Parse(c.ServerURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
	}
	for _, value := range []string{c.TemplateID, c.Location, c.Crying, c.Quarrel, c.AlarmHint} {
		if strings.TrimSpace(value) != value || len([]rune(value)) > 128 {
			return ErrInvalid
		}
	}
	return nil
}
