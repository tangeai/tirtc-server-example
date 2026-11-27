package push

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Template maps the four available data sources to fields on an approved long-term template.
// An empty field name omits that source from the message.
type Template struct {
	LabelKey    string `json:"label_key,omitempty" yaml:"label_key"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	ID          string `json:"template_id" yaml:"template_id"`
	Hint        string `json:"hint" yaml:"hint"`
	Location    string `json:"location" yaml:"location"`
	TimeKey     string `json:"time_key" yaml:"time_key"`
	HintKey     string `json:"hint_key" yaml:"hint_key"`
	LocationKey string `json:"location_key" yaml:"location_key"`
	DeviceKey   string `json:"device_key" yaml:"device_key"`
}

type Policy struct {
	Alarm            Template `json:"alarm" yaml:"alarm"`
	AppID            string   `json:"app_id" yaml:"app_id"`
	Crying           Template `json:"crying" yaml:"crying"`
	Quarrel          Template `json:"quarrel" yaml:"quarrel"`
	Page             string   `json:"page" yaml:"page"`
	SuggestedSeconds int      `json:"suggested_seconds" yaml:"suggested_seconds"`
	MaxAttempts      int      `json:"max_attempts" yaml:"max_attempts"`
	BacklogSeconds   int      `json:"backlog_seconds" yaml:"backlog_seconds"`
}

func DefaultPolicy() Policy {
	t := Template{ID: AlarmTemplate, TimeKey: "time1", HintKey: "enum_string3", LocationKey: "enum_string2", DeviceKey: "character_string4"}
	return Policy{Alarm: t, Crying: t, Quarrel: t, Page: "pages/notifications/index", SuggestedSeconds: 60, MaxAttempts: 3, BacklogSeconds: 300}
}

func (c Config) Policy(app string) Policy {
	if c.Notifications != nil {
		return *c.Notifications
	}
	p := DefaultPolicy()
	p.AppID = app
	for t, hint := range map[*Template]string{&p.Alarm: c.AlarmHint, &p.Crying: c.Crying, &p.Quarrel: c.Quarrel} {
		t.ID, t.Hint, t.Location = c.TemplateID, hint, c.Location
		t.Enabled = app != "" && t.ID != "" && hint != "" && t.Location != ""
	}
	return p
}

func (p Policy) Template(kind string) Template {
	switch kind {
	case "alarm":
		return p.Alarm
	case "crying":
		return p.Crying
	case "quarrel":
		return p.Quarrel
	}
	return Template{}
}

var templateIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var regexpThing = regexp.MustCompile(`^thing[1-9][0-9]{0,2}$`)
var fieldPattern = regexp.MustCompile(`^[a-z][a-z_]{0,32}[1-9][0-9]{0,2}$`)
var pagePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(/[a-zA-Z0-9_-]+)+$`)

func (p Policy) Validate() error {
	if p.SuggestedSeconds < 0 || p.SuggestedSeconds > 86400 || p.MaxAttempts < 1 || p.MaxAttempts > 10 || p.BacklogSeconds < 1 || p.BacklogSeconds > 86400 {
		return errors.New("请检查建议间隔、最大尝试次数和积压阈值")
	}
	if (p.Page != "" && !pagePattern.MatchString(p.Page)) || len(p.Page) > 256 {
		return errors.New("跳转页须填写小程序内已发布的页面路径")
	}
	if p.AppID != "" && (len(p.AppID) > 64 || !templateIDPattern.MatchString(p.AppID)) {
		return errors.New("小程序 AppID 格式无效")
	}
	if p.Alarm.Enabled && p.Alarm.LabelKey == "" && p.Page == "" {
		return errors.New("请配置显示识别标签的自由文本字段或提醒详情页")
	}
	if p.Alarm.ID == AlarmTemplate && p.Alarm.LabelKey != "" {
		return errors.New("模板 27919 通过提醒详情页展示标签，请留空模型标签字段")
	}
	for _, t := range []Template{p.Alarm, p.Crying, p.Quarrel} {
		if t.ID != "" && !templateIDPattern.MatchString(t.ID) {
			return errors.New("微信模板 ID 格式无效")
		}
		if t.LabelKey != "" && !regexpThing.MatchString(t.LabelKey) {
			return errors.New("模型标签只能使用自由文本字段，如 thing5")
		}
		seen := map[string]bool{}
		for _, k := range []string{t.TimeKey, t.HintKey, t.LocationKey, t.DeviceKey, t.LabelKey} {
			if k == "" {
				continue
			}
			if !fieldPattern.MatchString(k) || seen[k] {
				return errors.New("模板字段名无效或重复")
			}
			seen[k] = true
		}
		for _, v := range []string{t.Location, t.Hint} {
			if strings.TrimSpace(v) != v || len([]rune(v)) > 128 {
				return errors.New("模板枚举值无效")
			}
		}
		if t.Enabled && (p.AppID == "" || t.ID == "" || len(seen) == 0 || t.HintKey != "" && t.Hint == "" || t.LocationKey != "" && t.Location == "") {
			return errors.New("启用事件前请填写 AppID、模板及已映射字段的值")
		}
	}
	return nil
}

func ParsePolicy(raw json.RawMessage) (Policy, error) {
	p := DefaultPolicy()
	// Decode into a new value so fields removed by a publication stay removed.
	var v Policy
	if !json.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return v, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	v.MaxAttempts, v.BacklogSeconds, v.SuggestedSeconds = p.MaxAttempts, p.BacklogSeconds, p.SuggestedSeconds
	if err := d.Decode(&v); err != nil {
		return v, errors.New("推送配置字段或类型无效")
	}
	return v, v.Validate()
}
