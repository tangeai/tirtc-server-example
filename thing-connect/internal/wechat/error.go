// Package wechat provides application-scoped login and shared access token management.
package wechat

import "fmt"

// APIError preserves the WeChat business error code so callers can apply
// targeted recovery (notably errcode=9 for an invalid device authorization).
type APIError struct {
	Errcode int
	Errmsg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wechat err %d: %s", e.Errcode, e.Errmsg)
}
