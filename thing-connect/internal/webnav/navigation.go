// Package webnav validates and publishes user-facing navigation links.
package webnav

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

type Link struct {
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
}
type Config struct {
	Links []Link `json:"links" yaml:"links"`
}

func (c Config) Validate() error {
	if len(c.Links) > 3 {
		return errors.New("顶部导航最多配置 3 个链接")
	}
	for _, link := range c.Links {
		if strings.TrimSpace(link.Name) == "" || utf8.RuneCountInString(link.Name) > 20 || strings.IndexFunc(link.Name, unicode.IsControl) >= 0 {
			return errors.New("链接名称须为 1–20 个可见字符")
		}
		u, err := url.Parse(link.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(link.URL) > 2048 || strings.ContainsAny(link.URL, "\\\r\n\t ") {
			return errors.New("链接地址须为有效的 HTTP(S) 地址，不能包含账号密码或空白")
		}
	}
	return nil
}
func Parse(raw []byte) (Config, error) {
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, errors.New("导航配置格式不正确")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, errors.New("导航配置必须是单个 JSON 对象")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return c, errors.New("导航配置不能为 null")
	}
	return c, c.Validate()
}

type Public struct {
	Links    []Link `json:"links"`
	Revision int64  `json:"revision"`
}
type State struct {
	mu    sync.RWMutex
	value Public
}

func New(c Config) (*State, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s := &State{}
	s.set(c, 0)
	return s, nil
}
func (s *State) set(c Config, revision int64) {
	links := make([]Link, 0, len(c.Links))
	for _, l := range c.Links {
		if l.Enabled {
			links = append(links, l)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = Public{Links: links, Revision: revision}
}
func (s *State) Apply(raw []byte, revision int64) error {
	c, err := Parse(raw)
	if err != nil {
		return err
	}
	// Revision zero is the registry default; preserve the explicit YAML fallback.
	if revision > 0 {
		s.set(c, revision)
	}
	return nil
}
func (s *State) Current() Public {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Public{Links: append([]Link{}, s.value.Links...), Revision: s.value.Revision}
}
