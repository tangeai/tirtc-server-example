package wechat

import common "thing-connect/internal/wechat"

type APIError = common.APIError
type RefreshGate = common.RefreshGate

var CallWithAccessToken = common.CallWithAccessToken
var GetAccessToken = common.GetAccessToken
var Jscode2session = common.Jscode2session
var ErrTokenRefreshInProgress = common.ErrTokenRefreshInProgress
