package handler

import (
	"github.com/gin-gonic/gin"
	"thing-connect/internal/ai/roletypes"
	"thing-connect/internal/apiresp"
)

func (h *AgentHandler) SetRoleTypes(catalog roletypes.Reader) { h.roleTypes = catalog }
func (h *AgentHandler) listRoleTypes(c *gin.Context) {
	if h.roleTypes == nil {
		apiresp.Fail(c, 503, 503, "角色类型目录暂不可用，请稍后重试")
		return
	}
	items, err := h.roleTypes.Published(c.Request.Context())
	if err != nil {
		apiresp.Fail(c, 503, 503, "角色类型目录暂不可用，请稍后重试")
		return
	}
	c.Header("Cache-Control", "no-store")
	apiresp.OK(c, gin.H{"items": items})
}
