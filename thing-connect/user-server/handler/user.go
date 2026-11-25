package handler

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/captcha"
	"thing-connect/internal/deviceprofile"
	"thing-connect/internal/service"
)

type Server struct {
	userSvc        *service.UserService
	bindSvc        *service.BindService
	profileSvc     *deviceprofile.Service
	online         service.OnlineChecker
	auth           gin.HandlerFunc
	cleanupTargets []string
}

func NewServer(userSvc *service.UserService, bindSvc *service.BindService, profileSvc *deviceprofile.Service, online service.OnlineChecker, auth gin.HandlerFunc, cleanupTargets []string) *Server {
	if userSvc == nil || bindSvc == nil || online == nil || auth == nil {
		panic("user handler requires user and bind services, online status and authentication")
	}
	return &Server{userSvc: userSvc, bindSvc: bindSvc, profileSvc: profileSvc, online: online, auth: auth, cleanupTargets: cleanupTargets}
}

// CaptchaConfig is the non-secret provider configuration exposed to clients.
// CaptchaID is kept in the response during the API migration so current 易盾
// clients continue to work.
type CaptchaConfig struct {
	Provider     string
	Enabled      bool
	CaptchaID    string
	PublicConfig map[string]string
}

var captchaConfig CaptchaConfig
var captchaConfigMu sync.RWMutex

func SetCaptchaConfig(cfg CaptchaConfig) {
	captchaConfigMu.Lock()
	captchaConfig = cfg
	captchaConfigMu.Unlock()
}

func (s *Server) Register(r *gin.Engine) {
	v1 := r.Group("/v1")

	// Public
	v1.GET("/config/captcha", func(c *gin.Context) {
		captchaConfigMu.RLock()
		currentCaptcha := captchaConfig
		captchaConfigMu.RUnlock()
		publicConfig := make(map[string]string, len(currentCaptcha.PublicConfig)+1)
		for key, value := range currentCaptcha.PublicConfig {
			publicConfig[key] = value
		}
		if currentCaptcha.CaptchaID != "" {
			publicConfig["captcha_id"] = currentCaptcha.CaptchaID
		}
		apiresp.OK(c, gin.H{
			"provider":      currentCaptcha.Provider,
			"enabled":       currentCaptcha.Enabled,
			"public_config": publicConfig,
			"captcha_id":    currentCaptcha.CaptchaID, // deprecated compatibility field
		})
	})
	v1.POST("/user/send-code", s.postSendCode)
	v1.POST("/user/register", s.postRegister)
	v1.POST("/user/login", s.postLogin)
	v1.POST("/user/password-reset/send-code", s.postPasswordResetSendCode)
	v1.POST("/user/password-reset", s.postPasswordReset)

	// Authenticated
	auth := v1.Group("", s.auth)
	auth.GET("/user/quota", s.getQuota)
	auth.GET("/user/device/list", s.getDeviceList)
	auth.PUT("/user/device/name", s.putDeviceName)
	auth.POST("/user/device/bind", s.postBind)
	auth.POST("/user/device/bind-by-id", s.postBindByDeviceID)
	auth.DELETE("/user/device/reset", s.deleteReset)
	auth.GET("/user/device/rtc-token", s.getRtcToken)
}

type sendCodeReq struct {
	Email     string          `json:"email"     binding:"required,email"`
	Captcha   *captchaPayload `json:"captcha"`
	CaptchaID string          `json:"captcha_id"`
	Validate  string          `json:"validate"`
	User      string          `json:"user"`
}

func (s *Server) postSendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok := req.captchaToken()
	tok.UserIP = c.ClientIP()
	if err := s.userSvc.SendCode(c.Request.Context(), req.Email, c.ClientIP(), tok); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, nil)
}

func (s *Server) postPasswordResetSendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok := req.captchaToken()
	tok.UserIP = c.ClientIP()
	if err := s.userSvc.SendPasswordResetCode(c.Request.Context(), req.Email, c.ClientIP(), tok); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, nil)
}

type registerReq struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Code     string `json:"code"     binding:"required"`
}

func (s *Server) postRegister(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok, userID, err := s.userSvc.Register(c.Request.Context(), req.Email, req.Password, req.Code, c.ClientIP())
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"token": tok, "user_id": userID})
}

type loginReq struct {
	Email     string          `json:"email"     binding:"required"`
	Password  string          `json:"password"  binding:"required"`
	CaptchaID string          `json:"captcha_id"`
	Validate  string          `json:"validate"`
	User      string          `json:"user"`
	Captcha   *captchaPayload `json:"captcha"`
}

type captchaPayload struct {
	Provider string            `json:"provider"`
	Token    string            `json:"token"`
	Metadata map[string]string `json:"metadata"`
}

func (r sendCodeReq) captchaToken() captcha.CaptchaToken {
	return makeCaptchaToken(r.Captcha, r.CaptchaID, r.Validate, r.User)
}

func (r loginReq) captchaToken() captcha.CaptchaToken {
	return makeCaptchaToken(r.Captcha, r.CaptchaID, r.Validate, r.User)
}

func makeCaptchaToken(payload *captchaPayload, captchaID, validate, user string) captcha.CaptchaToken {
	tok := captcha.CaptchaToken{CaptchaID: captchaID, Validate: validate, User: user}
	if payload != nil {
		tok.Provider, tok.Token, tok.Metadata = payload.Provider, payload.Token, payload.Metadata
	}
	return tok
}

func (s *Server) postLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok := req.captchaToken()
	tok.UserIP = c.ClientIP()
	jwtTok, userID, err := s.userSvc.Login(c.Request.Context(), req.Email, req.Password, tok)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"token": jwtTok, "user_id": userID})
}

func (s *Server) postPasswordReset(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if err := s.userSvc.ResetPassword(c.Request.Context(), req.Email, req.Password, req.Code, c.ClientIP()); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, nil)
}

func (s *Server) getQuota(c *gin.Context) {
	q, err := s.userSvc.Quota(c.Request.Context(), currentUserID(c))
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"quota": q})
}

func (s *Server) getDeviceList(c *gin.Context) {
	list, err := s.userSvc.DeviceList(c.Request.Context(), currentUserID(c), s.online)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, list)
}

const maxDeviceNameChars = 13

func (s *Server) putDeviceName(c *gin.Context) {
	var req struct {
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.DeviceName = strings.TrimSpace(req.DeviceName)
	if req.DeviceID == "" {
		apiresp.BadParam(c, "缺少 device_id")
		return
	}
	if utf8.RuneCountInString(req.DeviceName) > maxDeviceNameChars {
		apiresp.BadParam(c, "device_name 不能超过 13 个字符")
		return
	}
	if err := s.userSvc.UpdateDeviceName(
		c.Request.Context(), currentUserID(c), req.DeviceID, req.DeviceName,
	); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"device_id": req.DeviceID, "device_name": req.DeviceName})
}
