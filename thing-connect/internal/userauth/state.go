// Package userauth contains cross-service checks for user JWT state. The
// service-specific middleware still owns route authentication and context
// values; this middleware invalidates tokens after account/password changes.
package userauth

import (
	"context"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"

	"thing-connect/internal/apiresp"
)

type stateGetter interface {
	GetContext(context.Context, any, string, ...any) error
}

type accountState struct {
	Status       int8  `db:"status"`
	AuthRevision int64 `db:"auth_revision"`
}

func EnforceState(db *sqlx.DB, secret string) gin.HandlerFunc {
	return enforceState(db, secret)
}

func enforceState(db stateGetter, secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			c.Next()
			return
		}
		token, err := jwt.Parse(strings.TrimPrefix(authorization, "Bearer "), func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if err != nil || !token.Valid {
			c.Next()
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.Next()
			return
		}
		userID, isUserToken := claims["user_id"].(float64)
		if !isUserToken {
			c.Next()
			return
		}
		revision, ok := ClaimRevision(claims)
		if !ok {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		var state accountState
		if db.GetContext(c.Request.Context(), &state, `SELECT status,auth_revision FROM users WHERE id=?`, int64(userID)) != nil || state.Status != 1 || state.AuthRevision != revision {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// ClaimRevision returns the revision carried by a user JWT. Tokens issued
// before account-state invalidation was introduced do not contain the claim;
// they map to the initial database revision so a rolling upgrade does not sign
// every existing user out. A later password or status change increments the
// database revision and invalidates both legacy and current tokens.
func ClaimRevision(claims jwt.MapClaims) (int64, bool) {
	raw, exists := claims["auth_revision"]
	if !exists {
		return 1, true
	}
	value, ok := raw.(float64)
	if !ok || value < 1 || value != math.Trunc(value) {
		return 0, false
	}
	return int64(value), true
}
