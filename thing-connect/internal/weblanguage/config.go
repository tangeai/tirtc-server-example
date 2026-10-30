// Package weblanguage owns the language policy for one service's Web pages.
package weblanguage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const DefaultJSON = `{"default":"auto","allow_switch":true}`

type Config struct {
	Default     string `json:"default" yaml:"default"`
	AllowSwitch bool   `json:"allow_switch" yaml:"allow_switch"`
}

func DefaultConfig() Config { return Config{Default: "auto", AllowSwitch: true} }

func (c Config) Validate() error {
	if c.Default != "auto" && c.Default != "zh-CN" && c.Default != "en" {
		return errors.New("默认语言必须为 auto、zh-CN 或 en")
	}
	return nil
}

func Parse(raw []byte) (Config, error) {
	var value struct {
		Default     *string `json:"default"`
		AllowSwitch *bool   `json:"allow_switch"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value.Default == nil || value.AllowSwitch == nil {
		return Config{}, errors.New("语言配置须包含 default 和 allow_switch")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("语言配置必须是单个 JSON 对象")
	}
	c := Config{Default: *value.Default, AllowSwitch: *value.AllowSwitch}
	return c, c.Validate()
}

type Public struct {
	Config
	Revision int64 `json:"revision"`
}

type State struct {
	mu       sync.RWMutex
	fallback Config
	current  Public
}

func New(c Config) (*State, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &State{fallback: c, current: Public{Config: c}}, nil
}

func (s *State) Apply(raw []byte, revision int64) error {
	c, err := Parse(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision == 0 {
		c = s.fallback
	}
	s.current = Public{Config: c, Revision: revision}
	return nil
}

func (s *State) Current() Public {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}
