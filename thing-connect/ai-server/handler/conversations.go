package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/ai/conversations"
	"thing-connect/internal/apiresp"
)

type ConversationOperations interface {
	List(context.Context, int64, string, int, int) (conversations.Page, error)
	Detail(context.Context, int64, string, string, string) ([]conversations.Message, error)
	Delete(context.Context, int64, string, string, string) error
}

func RegisterConversations(r *gin.Engine, secret string, history ConversationOperations) {
	group := r.Group("/v1/ai/device/:device_id/conversations", JWTAuth(secret))
	group.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	group.GET("", func(c *gin.Context) {
		page, e1 := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, e2 := strconv.Atoi(c.DefaultQuery("page_size", "10"))
		if e1 != nil || e2 != nil {
			apiresp.BadParam(c, "分页参数不正确")
			return
		}
		result, err := history.List(c.Request.Context(), currentUserID(c), c.Param("device_id"), page, size)
		if err != nil {
			failConversation(c, err)
			return
		}
		apiresp.OK(c, result)
	})
	group.GET("/:id", func(c *gin.Context) {
		result, err := history.Detail(c.Request.Context(), currentUserID(c), c.Param("device_id"), c.Param("id"), c.GetHeader("X-Conversation-Reference"))
		if err != nil {
			failConversation(c, err)
			return
		}
		apiresp.OK(c, gin.H{"items": result})
	})
	group.DELETE("/:id", func(c *gin.Context) {
		err := history.Delete(c.Request.Context(), currentUserID(c), c.Param("device_id"), c.Param("id"), c.GetHeader("X-Conversation-Reference"))
		if err != nil {
			failConversation(c, err)
			return
		}
		apiresp.OK(c, nil)
	})
}
func failConversation(c *gin.Context, err error) {
	switch {
	case errors.Is(err, conversations.ErrInvalid):
		apiresp.BadParam(c, "会话记录参数不正确")
	case errors.Is(err, conversations.ErrForbidden):
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权查看该会话或记录凭证已过期，请刷新列表")
	case errors.Is(err, conversations.ErrNotFound):
		apiresp.Fail(c, http.StatusNotFound, 40400, "会话已不存在，请刷新列表")
	case errors.Is(err, conversations.ErrUpstream):
		failAIUpstream(c, "读取或管理会话记录", err)
	default:
		slog.ErrorContext(c.Request.Context(), "conversation operation failed", "err", err)
		apiresp.Fail(c, http.StatusInternalServerError, 50000, "服务器内部错误，请稍后重试")
	}
}
