// Package sitebrand owns the public site's naming policy and atomic publication.
package sitebrand

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const DefaultJSON = `{"name":"探鸽 · 小钛","name_en":"Tange · Xiaotai","public_url":""}`

type Config struct {
	Name      string `json:"name" yaml:"name"`
	NameEN    string `json:"name_en" yaml:"name_en"`
	PublicURL string `json:"public_url" yaml:"public_url"`
}

func DefaultConfig() Config { return Config{Name: "探鸽 · 小钛", NameEN: "Tange · Xiaotai"} }

func (c Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("站点名称不能为空")
	}
	for _, name := range []string{c.Name, c.NameEN} {
		if utf8.RuneCountInString(name) > 40 || strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.TrimSpace(name) != name {
			return errors.New("站点名称最多 40 个字符，不能包含控制字符或首尾空白")
		}
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(c.PublicURL, "\\\r\n\t ?#") || len(c.PublicURL) > 2048 {
			return errors.New("站点网址须为 HTTPS 根地址，不能包含账号、路径、查询参数或片段")
		}
	}
	return nil
}

func Parse(raw []byte) (Config, error) {
	var value struct {
		Name      *string `json:"name"`
		NameEN    *string `json:"name_en"`
		PublicURL string  `json:"public_url"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil || value.Name == nil || value.NameEN == nil {
		return Config{}, errors.New("站点配置须包含 name 和 name_en 字符串")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("站点配置必须是单个 JSON 对象")
	}
	c := Config{Name: *value.Name, NameEN: *value.NameEN, PublicURL: value.PublicURL}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	return c, nil
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
func (s *State) Current() Public { s.mu.RLock(); defer s.mu.RUnlock(); return s.current }
