package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"thing-connect/internal/apiresp"
)

const ctxUserID = "userID"

// JWTAuth validates Bearer JWT and sets user_id in context.
// Intended for user-facing agent management routes.
func JWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		tokStr := strings.TrimPrefix(auth, "Bearer ")
		tok, err := jwt.Parse(tokStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !tok.Valid {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		uid, ok := claims["user_id"].(float64)
		if !ok {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		c.Set(ctxUserID, int64(uid))
		c.Next()
	}
}

func currentUserID(c *gin.Context) int64 {
	v, _ := c.Get(ctxUserID)
	id, _ := v.(int64)
	return id
}
