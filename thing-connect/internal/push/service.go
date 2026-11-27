package push

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("订阅模板尚未配置")

type Reference struct {
	Device    string `db:"device_id" json:"device_id"`
	LabelText string `db:"label_text" json:"label_text"`
}

type Repository interface {
	Lookup(context.Context, Event) (string, error)
	Admit(context.Context, Event, string, string) (string, error)
	Preference(context.Context, int64, string, string, bool, *int) (*int, error)
	Prepare(context.Context, int64, string, string, string) (string, bool, error)
	Confirm(context.Context, int64, string, bool) error
	Report(context.Context, int64, string, string) error
	Reference(context.Context, int64, string) (Reference, error)
}
type IdentityProvider interface {
	Ready(string) bool
	Login(context.Context, string) (string, error)
}
type Service struct {
	Repository
	wechat IdentityProvider
	policy Policy
}

func New(repo Repository, w IdentityProvider, policy Policy) *Service {
	if repo == nil || w == nil {
		panic("push requires a repository and a WeChat identity provider")
	}
	return &Service{repo, w, policy}
}

// AwaitingPolicy keeps device preferences available during the initial Admin load.
func AwaitingPolicy(repo Repository) *Service {
	return &Service{Repository: repo, policy: DefaultPolicy()}
}

func (s *Service) Receive(ctx context.Context, e Event) (string, error) {
	if s.wechat == nil {
		return "", ErrUnavailable
	}
	template := ""
	if s.wechat.Ready(e.Kind) {
		template = s.policy.Template(e.Kind).ID
	}
	e.MaxAttempts = s.policy.MaxAttempts
	return s.Admit(ctx, e, s.policy.AppID, template)
}
func (s *Service) Enabled(kind string) bool { return s.wechat != nil && s.wechat.Ready(kind) }
func (s *Service) Subscribe(ctx context.Context, user int64, app, code, kind string) (string, bool, error) {
	if !s.Enabled(kind) {
		return "", false, ErrUnavailable
	}
	if app != s.policy.AppID || code == "" || len(code) > 256 {
		return "", false, ErrInvalid
	}
	open, err := s.wechat.Login(ctx, code)
	if err != nil {
		return "", false, ErrUnavailable
	}
	return s.Prepare(ctx, user, app, open, s.policy.Template(kind).ID)
}
func (s *Service) TemplateID(kind string) string { return s.policy.Template(kind).ID }
func (s *Service) SuggestedSeconds() int         { return s.policy.SuggestedSeconds }
