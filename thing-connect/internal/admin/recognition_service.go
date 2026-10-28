package admin

import (
	"context"
	"strings"
	r "thing-connect/internal/recognition"
)

type RecognitionMutation struct {
	DeviceID         string
	Allowed          bool
	Limit            int64
	ExpectedRevision int64
	Audit            AuditEvent
}
type RecognitionSchemeMutation struct {
	Scheme   r.Scheme
	Action   string
	Revision int64
	Audit    AuditEvent
}
type RecognitionAdminStore interface {
	State(context.Context, string) (r.State, error)
	Update(context.Context, RecognitionMutation) (r.State, error)
	Schemes(context.Context) ([]r.Scheme, error)
	SaveScheme(context.Context, RecognitionSchemeMutation) (r.Scheme, error)
	Records(context.Context, string) (any, error)
}
type RecognitionService struct{ store RecognitionAdminStore }

func NewRecognitionService(s RecognitionAdminStore) *RecognitionService {
	return &RecognitionService{s}
}
func (s *RecognitionService) State(c context.Context, id string) (r.State, error) {
	return s.store.State(c, id)
}
func (s *RecognitionService) Update(c context.Context, id string, on bool, limit, rev int64, meta BoardRequestMeta) (r.State, error) {
	if strings.TrimSpace(id) == "" || !validBoardMeta(meta) || rev < 0 || limit < 0 || limit > 1000000 {
		return r.State{}, r.ErrInvalid
	}
	a := boardAudit(meta, "device.recognition.write", id, "", "")
	a.Resource = "device"
	return s.store.Update(c, RecognitionMutation{id, on, limit, rev, a})
}
func (s *RecognitionService) Schemes(c context.Context) ([]r.Scheme, error) {
	return s.store.Schemes(c)
}
func (s *RecognitionService) SaveScheme(c context.Context, v r.Scheme, action string, rev int64, meta BoardRequestMeta) (r.Scheme, error) {
	if !validBoardMeta(meta) || rev < 0 {
		return v, r.ErrInvalid
	}
	if action != "save" && action != "publish" && action != "status" {
		return v, r.ErrInvalid
	}
	if action == "save" {
		if e := r.ValidateScheme(v); e != nil {
			return v, e
		}
	}
	a := boardAudit(meta, "recognition.write", v.ID, "", "")
	a.Resource = "recognition_scheme"
	return s.store.SaveScheme(c, RecognitionSchemeMutation{v, action, rev, a})
}
func (s *RecognitionService) Records(c context.Context, id string) (any, error) {
	return s.store.Records(c, id)
}
