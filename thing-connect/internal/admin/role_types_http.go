package admin

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/ai/roletypes"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/logging"
)

func (s *HTTPServer) SetRoleTypeService(service *RoleTypeService) { s.roleTypes = service }
func (s *HTTPServer) listRoleTypes(c *gin.Context) {
	items, err := s.roleTypes.List(c.Request.Context())
	if err != nil {
		writeRoleTypeResult(c, nil, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	apiresp.OK(c, gin.H{"items": items})
}
func (s *HTTPServer) roleTypeVoices(c *gin.Context) {
	items, err := s.roleTypes.Voices(c.Request.Context())
	if err != nil {
		writeRoleTypeResult(c, nil, errors.Join(roletypes.ErrUnavailable, err))
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}
func (s *HTTPServer) saveRoleType(c *gin.Context) {
	var body struct {
		Type             roletypes.Type `json:"role_type"`
		ExpectedRevision int64          `json:"expected_revision"`
		Reason           string         `json:"reason"`
		Status           string         `json:"status"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	if err := c.ShouldBindJSON(&body); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	action := "create"
	if c.Request.Method == http.MethodPut {
		action = "update"
		body.Type.ID = c.Param("id")
	}
	if c.FullPath() == "/v1/admin/ai-role-types/:id/status" {
		action = "status"
		body.Type.Status = body.Status
	}
	identity, _ := identityFromContext(c)
	value, err := s.roleTypes.Save(c.Request.Context(), body.Type, body.ExpectedRevision, action, RoleTypeRequestMeta{Actor: identity, RequestID: logging.RequestIDFrom(c.Request.Context()), Method: c.Request.Method, Path: c.Request.URL.Path, ClientIP: c.ClientIP(), UserAgent: c.Request.UserAgent(), Reason: body.Reason})
	writeRoleTypeResult(c, value, err)
}
func writeRoleTypeResult(c *gin.Context, value any, err error) {
	switch {
	case errors.Is(err, roletypes.ErrUnsupportedModel):
		apiresp.BadParam(c, "探鸽存在该音色，但当前角色编辑器暂不支持此模型，请保留为草稿")
	case errors.Is(err, roletypes.ErrInvalid):
		apiresp.BadParam(c, "请检查类型名称、提示词、排序及所选音色，启用前需选择可用音色")
	case errors.Is(err, ErrConflict):
		apiresp.Fail(c, 409, 40901, "类型名称重复或记录已被修改，请刷新后重试")
	case errors.Is(err, ErrNotFound):
		apiresp.Fail(c, 404, 40400, "角色类型不存在")
	case errors.Is(err, roletypes.ErrUnavailable):
		apiresp.Fail(c, 502, 50200, "音色目录暂不可用，请稍后重试")
	case err != nil:
		slog.ErrorContext(c.Request.Context(), "role type operation failed", "err", err)
		apiresp.Internal(c, "角色类型操作失败")
	default:
		apiresp.OK(c, value)
	}
}
func (s *HTTPServer) registerRoleTypesInternal(r *gin.Engine, key string) {
	r.GET("/v1/internal/ai-role-types", func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Internal-Key")), []byte(key)) != 1 {
			apiresp.Fail(c, 403, 40301, "内部服务凭证无效")
			return
		}
		items, err := s.roleTypes.Published(c.Request.Context())
		if err != nil {
			writeRoleTypeResult(c, nil, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		apiresp.OK(c, gin.H{"items": items})
	})
}
