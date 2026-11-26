package push

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("订阅模板尚未配置")

type Repository interface {
	Admit(context.Context, Event, string, string) (string, error)
	Preference(context.Context, int64, string, string, bool, *int) (*int, error)
	Prepare(context.Context, int64, string, string, string) (string, bool, error)
	Confirm(context.Context, int64, string, bool) error
	Report(context.Context, int64, string, string) error
	Reference(context.Context, int64, string) (string, error)
}
type IdentityProvider interface {
	Ready(string) bool
	Login(context.Context, string) (string, error)
}
type Service struct {
	Repository
	wechat        IdentityProvider
	app, template string
}

func New(repo Repository, w IdentityProvider, app, template string) *Service {
	if repo == nil || w == nil {
		panic("push requires a repository and a WeChat identity provider")
	}
	return &Service{repo, w, app, template}
}
func (s *Service) Receive(ctx context.Context, e Event) (string, error) {
	template := ""
	if s.wechat.Ready(e.Kind) {
		template = s.template
	}
	return s.Admit(ctx, e, s.app, template)
}
func (s *Service) Enabled(kind string) bool { return s.wechat.Ready(kind) }
func (s *Service) Subscribe(ctx context.Context, user int64, app, code, kind string) (string, bool, error) {
	if !s.Enabled(kind) {
		return "", false, ErrUnavailable
	}
	if app != s.app || code == "" || len(code) > 256 {
		return "", false, ErrInvalid
	}
	open, err := s.wechat.Login(ctx, code)
	if err != nil {
		return "", false, ErrUnavailable
	}
	return s.Prepare(ctx, user, app, open, s.template)
}
func (s *Service) TemplateID() string { return s.template }
