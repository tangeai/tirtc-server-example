package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"thing-connect/internal/ai/roletypes"
)

type RoleTypeMutation struct {
	Type             roletypes.Type
	ExpectedRevision int64
	Action           string
	Audit            AuditEvent
}
type RoleTypeStore interface {
	List(context.Context, bool) ([]roletypes.Type, error)
	Get(context.Context, string) (roletypes.Type, error)
	Save(context.Context, RoleTypeMutation) (roletypes.Type, error)
}
type RoleTypeService struct {
	store  RoleTypeStore
	voices roletypes.VoiceCatalog
}

func NewRoleTypeService(store RoleTypeStore, voices roletypes.VoiceCatalog) *RoleTypeService {
	return &RoleTypeService{store: store, voices: voices}
}
func (s *RoleTypeService) List(ctx context.Context) ([]roletypes.Type, error) {
	return s.store.List(ctx, false)
}
func (s *RoleTypeService) Published(ctx context.Context) ([]roletypes.Type, error) {
	return s.store.List(ctx, true)
}
func (s *RoleTypeService) Voices(ctx context.Context) ([]roletypes.Voice, error) {
	return s.voices.Voices(ctx)
}
func (s *RoleTypeService) Save(ctx context.Context, v roletypes.Type, revision int64, action string, meta RoleTypeRequestMeta) (roletypes.Type, error) {
	if meta.Actor.UserID <= 0 || strings.TrimSpace(meta.Reason) == "" || len(meta.Reason) > 500 {
		return roletypes.Type{}, roletypes.ErrInvalid
	}
	if action != "create" && action != "update" && action != "status" {
		return roletypes.Type{}, roletypes.ErrInvalid
	}
	var before roletypes.Type
	if action == "create" {
		v.ID = uuid.NewString()
		v.Status = "draft"
		revision = 0
	} else {
		if revision < 1 || v.ID == "" {
			return roletypes.Type{}, roletypes.ErrInvalid
		}
		var err error
		before, err = s.store.Get(ctx, v.ID)
		if err != nil {
			return roletypes.Type{}, err
		}
		if before.Revision != revision {
			return roletypes.Type{}, ErrConflict
		}
		if action == "status" {
			status := v.Status
			v = before
			v.Status = status
			if status != "published" && status != "offline" {
				return roletypes.Type{}, roletypes.ErrInvalid
			}
		} else {
			v.Status = before.Status
		}
	}
	v = roletypes.Normalize(v)
	if err := roletypes.Validate(v); err != nil {
		return roletypes.Type{}, err
	}
	// Remote validation happens before the transaction. Disabling a type and
	// editing its copy without changing the voice must work during an outage.
	if (v.Status == "published" && before.Status != "published") || (v.VoiceID != "" && (v.VoiceID != before.VoiceID || v.VoiceModel != before.VoiceModel)) {
		voices, err := s.voices.Voices(ctx)
		if err != nil {
			return roletypes.Type{}, errors.Join(roletypes.ErrUnavailable, err)
		}
		found := false
		for _, voice := range voices {
			if voice.ID == v.VoiceID && voice.Model == v.VoiceModel {
				found = true
				break
			}
		}
		if !found {
			return roletypes.Type{}, roletypes.ErrInvalid
		}
	}
	if v.Status == "published" && v.VoiceID == "" {
		return roletypes.Type{}, roletypes.ErrInvalid
	}
	audit := AuditEvent{AdminUserID: meta.Actor.UserID, RoleCodes: strings.Join(meta.Actor.Roles, ","), RequestID: meta.RequestID, Method: meta.Method, Path: meta.Path, HTTPStatus: 200, Action: "ai_role_type." + action, Resource: "ai_role_type", ResourceID: v.ID, Reason: strings.TrimSpace(meta.Reason), ClientIP: meta.ClientIP, UserAgent: meta.UserAgent, Success: true}
	return s.store.Save(ctx, RoleTypeMutation{Type: v, ExpectedRevision: revision, Action: action, Audit: audit})
}

// RequestMeta belongs to role type management, independently of board resources.
type RoleTypeRequestMeta struct {
	Actor                                                AccessIdentity
	RequestID, Method, Path, ClientIP, UserAgent, Reason string
}
