package handler

import (
	"github.com/gin-gonic/gin"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/service"
)

func RegisterAccount(r *gin.Engine, auth gin.HandlerFunc, accounts *service.AccountService) {
	r.GET("/v1/user/me", auth, func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		account, err := accounts.Current(c.Request.Context(), currentUserID(c))
		if err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
		apiresp.OK(c, account)
	})
}
