package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/userauth"
)

const ctxUserID = "userID"

func JWTAuth(secret string, redisClient *redis.Client, db *sqlx.DB) gin.HandlerFunc {
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
		userID := int64(uid)
		if db != nil {
			var state struct {
				Status       int8  `db:"status"`
				AuthRevision int64 `db:"auth_revision"`
			}
			claimRevision, revisionOK := userauth.ClaimRevision(claims)
			if !revisionOK || db.GetContext(c.Request.Context(), &state, `SELECT status,auth_revision FROM users WHERE id=?`, userID) != nil || state.Status != 1 || state.AuthRevision != claimRevision {
				apiresp.Unauthorized(c)
				c.Abort()
				return
			}
		}
		if redisClient != nil {
			keys := []string{fmt.Sprintf("thingconnect:user:disabled:%d", userID), fmt.Sprintf("thingconnect:user:revoked_after:%d", userID)}
			values, err := redisClient.MGet(c.Request.Context(), keys...).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				apiresp.Unauthorized(c)
				c.Abort()
				return
			}
			if len(values) > 0 && values[0] != nil {
				apiresp.Unauthorized(c)
				c.Abort()
				return
			}
			if len(values) > 1 && values[1] != nil {
				revokedAfter, _ := strconv.ParseInt(values[1].(string), 10, 64)
				issuedAt, _ := claims["iat"].(float64)
				if int64(issuedAt) <= revokedAfter {
					apiresp.Unauthorized(c)
					c.Abort()
					return
				}
			}
		}
		c.Set(ctxUserID, userID)
		c.Next()
	}
}

func currentUserID(c *gin.Context) int64 {
	v, _ := c.Get(ctxUserID)
	id, _ := v.(int64)
	return id
}
