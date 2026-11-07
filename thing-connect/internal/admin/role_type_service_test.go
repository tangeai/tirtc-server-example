package admin

import (
	"context"
	"errors"
	"testing"

	"thing-connect/internal/ai/roletypes"
)

type typeStoreStub struct {
	value roletypes.Type
	saves int
	last  RoleTypeMutation
}

func (s *typeStoreStub) List(context.Context, bool) ([]roletypes.Type, error) {
	return []roletypes.Type{s.value}, nil
}
func (s *typeStoreStub) Get(context.Context, string) (roletypes.Type, error) { return s.value, nil }
func (s *typeStoreStub) Save(_ context.Context, m RoleTypeMutation) (roletypes.Type, error) {
	s.saves++
	s.last = m
	return m.Type, nil
}

type voicesStub struct {
	err   error
	calls int
}

func (s *voicesStub) Voices(context.Context) ([]roletypes.Voice, error) {
	s.calls++
	return []roletypes.Voice{{ID: "voice", Model: roletypes.VoiceModel}}, s.err
}
func TestRoleTypeCommandsValidateAndPreserveStatus(t *testing.T) {
	ctx := context.Background()
	store := &typeStoreStub{value: roletypes.Type{ID: "one", Name: "角色", VoiceID: "voice", VoiceModel: roletypes.VoiceModel, Prompt: "已保存设定", Status: "published", Revision: 2}}
	voices := &voicesStub{}
	svc := NewRoleTypeService(store, voices)
	meta := RoleTypeRequestMeta{Actor: AccessIdentity{UserID: 1}, Reason: "测试修改"}
	update := store.value
	update.Prompt = "编辑后的设定"
	update.Status = "offline"
	got, err := svc.Save(ctx, update, 2, "update", meta)
	if err != nil || got.Status != "published" || voices.calls != 0 {
		t.Fatalf("copy edit: %+v %v", got, err)
	}
	if _, err = svc.Save(ctx, update, 1, "update", meta); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	update.VoiceID = "unknown"
	if _, err = svc.Save(ctx, update, 2, "update", meta); !errors.Is(err, roletypes.ErrInvalid) {
		t.Fatalf("invalid voice: %v", err)
	}
	voices.err = errors.New("private upstream failure")
	if _, err = svc.Save(ctx, update, 2, "update", meta); !errors.Is(err, roletypes.ErrUnavailable) {
		t.Fatalf("upstream: %v", err)
	}
	got, err = svc.Save(ctx, roletypes.Type{ID: "one", Status: "offline"}, 2, "status", meta)
	if err != nil || got.Prompt != store.value.Prompt || got.VoiceID != "voice" {
		t.Fatalf("offline during outage: %+v %v", got, err)
	}
	if store.last.Audit.Resource != "ai_role_type" || store.last.Audit.AdminUserID != 1 {
		t.Fatal("missing audit metadata")
	}
}
func TestRoleTypeCreateDoesNotTrustClientIdentityOrStatus(t *testing.T) {
	store := &typeStoreStub{}
	svc := NewRoleTypeService(store, &voicesStub{})
	got, err := svc.Save(context.Background(), roletypes.Type{ID: "client-id", Name: "  类型  ", VoiceModel: roletypes.VoiceModel, Prompt: "设定", Status: "published", Revision: 99}, 99, "create", RoleTypeRequestMeta{Actor: AccessIdentity{UserID: 1}, Reason: "初始化"})
	if err != nil || got.ID == "client-id" || got.Status != "draft" || got.Name != "类型" {
		t.Fatalf("create: %+v %v", got, err)
	}
	if _, err = svc.Save(context.Background(), got, 0, "create", RoleTypeRequestMeta{}); !errors.Is(err, roletypes.ErrInvalid) {
		t.Fatal("missing actor accepted")
	}
}

type modelVoices []roletypes.Voice

func (v modelVoices) Voices(context.Context) ([]roletypes.Voice, error) { return v, nil }
func TestRoleTypePublicationRequiresSupportedModelPromptAndExactCatalogPair(t *testing.T) {
	for _, tc := range []struct {
		name, model, voice, prompt string
		want                       error
	}{
		{"seed", "seed-tts-2.0", "seed", "设定", nil},
		{"v3", "cosyvoice-v3", "shared", "设定", nil},
		{"flash same id", "cosyvoice-v3-flash", "shared", "设定", nil},
		{"v3 cannot borrow flash voice", "cosyvoice-v3", "ali", "设定", roletypes.ErrInvalid},
		{"wrong model", "seed-tts-2.0", "ali", "设定", roletypes.ErrInvalid},
		{"undocumented model", "ve-bidirection", "ve", "设定", roletypes.ErrUnsupportedModel},
		{"missing prompt", "seed-tts-2.0", "seed", "", roletypes.ErrInvalid},
		{"missing voice", "seed-tts-2.0", "", "设定", roletypes.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &typeStoreStub{value: roletypes.Type{ID: "one", Name: "类型", VoiceModel: tc.model, VoiceID: tc.voice, Prompt: tc.prompt, Status: "draft", Revision: 1}}
			svc := NewRoleTypeService(store, modelVoices{{ID: "shared", Model: "cosyvoice-v3"}, {ID: "shared", Model: "cosyvoice-v3-flash"}, {ID: "seed", Model: "seed-tts-2.0"}, {ID: "ali", Model: roletypes.VoiceModel}, {ID: "ve", Model: "ve-bidirection"}})
			_, err := svc.Save(context.Background(), roletypes.Type{ID: "one", Status: "published"}, 1, "status", RoleTypeRequestMeta{Actor: AccessIdentity{UserID: 1}, Reason: "启用"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if tc.want != nil && store.saves != 0 {
				t.Fatal("invalid publication reached store")
			}
		})
	}
}
func TestRoleTypeMissingPromptCanBeMaintainedAsDraft(t *testing.T) {
	store := &typeStoreStub{}
	svc := NewRoleTypeService(store, &voicesStub{})
	got, err := svc.Save(context.Background(), roletypes.Type{Name: "外语预留", Language: "fr", VoiceModel: roletypes.VoiceModel}, 0, "create", RoleTypeRequestMeta{Actor: AccessIdentity{UserID: 1}, Reason: "保留目录"})
	if err != nil || got.Status != "draft" || got.Language != "fr" || got.Prompt != "" {
		t.Fatalf("draft=%+v err=%v", got, err)
	}
}
