package handler

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/captcha"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/service"
	"thing-connect/internal/store"
	mysqlstore "thing-connect/internal/store/mysql"
)

// UnbindCleanup holds callbacks for cross-cutting cleanup after a device unbind.
// Each callback is best-effort — failures are logged, never block the response.
type UnbindCleanup struct {
	// Enqueue persists all configured cross-service cleanup notifications.
	Enqueue func(ctx context.Context, deviceID string) error
	// DeleteLocalRole removes the ai_device_role row for this device.
	DeleteLocalRole func(ctx context.Context, deviceID string) error
	// DeleteCloudRoles calls the tange cloud to remove device-role bindings.
	DeleteCloudRoles func(ctx context.Context, deviceID string) error
	// NotifyVoIP tells the voip-server to clean up device profile/auth.
	NotifyVoIP func(deviceID string)
}

type Server struct {
	userSvc   *service.UserService
	bindSvc   *service.BindService
	mqtt      *mqttc.Broker
	jwtSecret string

	callServerURL string // call-server base URL for internal/unbind
	internalKey   string // X-Internal-Key header value

	// UnbindCleanup holds cross-cutting cleanup callbacks invoked after unbind.
	UnbindCleanup *UnbindCleanup

	// RoleStore is used by the legacy integration-test path.
	RoleStore store.RoleBindingStore

	// Legacy fields for integration test compatibility.
	DB        *sqlx.DB
	RDB       *redis.Client
	MQTT      *mqttc.Broker
	JWTSecret string

	passwordResetMailQueueCancel context.CancelFunc
}

func NewServer(userSvc *service.UserService, bindSvc *service.BindService, mqtt *mqttc.Broker, jwtSecret, callServerURL, internalKey string, roleStore store.RoleBindingStore, cleanup *UnbindCleanup) *Server {
	return &Server{
		userSvc: userSvc, bindSvc: bindSvc, mqtt: mqtt, jwtSecret: jwtSecret,
		callServerURL: callServerURL, internalKey: internalKey, RoleStore: roleStore, UnbindCleanup: cleanup,
	}
}

// noopCaptcha always passes — used in the legacy integration-test wiring path.
type noopCaptcha struct{}

func (noopCaptcha) Verify(_ context.Context, _ captcha.CaptchaToken) error { return nil }

// noopMailer silently discards email — used in the legacy integration-test wiring path.
type noopMailer struct{}

func (noopMailer) Send(_ context.Context, _, _, _ string) error { return nil }

// captchaID is exposed to the frontend so the widget knows which captcha to load.
// Empty when yidun is not configured (dev mode).
var captchaID string

// SetCaptchaID is called from main.go to pass the configured captcha_id to the handler.
func SetCaptchaID(id string) { captchaID = id }

func (s *Server) Register(r *gin.Engine) {
	if s.userSvc == nil && s.DB != nil {
		service.RegisterErrors()
		userStore := mysqlstore.NewUserStore(s.DB)
		bindStore := mysqlstore.NewBindStore(s.DB)
		cacheStore := mysqlstore.NewCacheStore(s.RDB)
		cfg := service.DefaultServiceConfig()
		// In the legacy/test path captcha and mailer are noop stubs.
		s.userSvc = service.NewUserService(userStore, cacheStore, &noopCaptcha{}, &noopMailer{}, s.JWTSecret, cfg)
		passwordResetMailQueue := service.NewInMemoryPasswordResetEmailQueue(s.userSvc.DeliverPasswordResetCode)
		s.userSvc.SetPasswordResetEmailQueue(passwordResetMailQueue)
		queueCtx, queueCancel := context.WithCancel(context.Background())
		s.passwordResetMailQueueCancel = queueCancel
		go passwordResetMailQueue.Run(queueCtx)
		var mqttPub service.MQTTPublisher
		if s.MQTT != nil {
			mqttPub = s.MQTT
		}
		s.bindSvc = service.NewBindService(bindStore, cacheStore, mqttPub, cfg)
		s.mqtt = s.MQTT
		s.jwtSecret = s.JWTSecret
	}
	v1 := r.Group("/v1")

	// Public
	v1.GET("/config/captcha", func(c *gin.Context) {
		apiresp.OK(c, gin.H{"captcha_id": captchaID})
	})
	v1.POST("/user/send-code", s.postSendCode)
	v1.POST("/user/register", s.postRegister)
	v1.POST("/user/login", s.postLogin)
	v1.POST("/user/password-reset/send-code", s.postPasswordResetSendCode)
	v1.POST("/user/password-reset", s.postPasswordReset)

	// Authenticated
	auth := v1.Group("", JWTAuth(s.jwtSecret))
	auth.GET("/user/quota", s.getQuota)
	auth.GET("/user/device/list", s.getDeviceList)
	auth.PUT("/user/device/name", s.putDeviceName)
	auth.POST("/user/device/bind", s.postBind)
	auth.POST("/user/device/bind-by-id", s.postBindByDeviceID)
	auth.DELETE("/user/device/reset", s.deleteReset)
	auth.GET("/user/device/rtc-token", s.getRtcToken)
}

// Close releases background workers created by the legacy integration path.
// Production wiring owns and stops its queue in user-server/main.go.
func (s *Server) Close() {
	if s.passwordResetMailQueueCancel != nil {
		s.passwordResetMailQueueCancel()
		s.passwordResetMailQueueCancel = nil
	}
}

type sendCodeReq struct {
	Email     string `json:"email"     binding:"required,email"`
	CaptchaID string `json:"captcha_id"`
	Validate  string `json:"validate"`
	User      string `json:"user"`
}

func (s *Server) postSendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok := captcha.CaptchaToken{CaptchaID: req.CaptchaID, Validate: req.Validate, User: req.User}
	if err := s.userSvc.SendCode(c.Request.Context(), req.Email, tok); err != nil {
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
	tok := captcha.CaptchaToken{CaptchaID: req.CaptchaID, Validate: req.Validate, User: req.User}
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
	tok, userID, err := s.userSvc.Register(c.Request.Context(), req.Email, req.Password, req.Code)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"token": tok, "user_id": userID})
}

type loginReq struct {
	Email     string `json:"email"     binding:"required"`
	Password  string `json:"password"  binding:"required"`
	CaptchaID string `json:"captcha_id"`
	Validate  string `json:"validate"`
	User      string `json:"user"`
}

func (s *Server) postLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	tok := captcha.CaptchaToken{CaptchaID: req.CaptchaID, Validate: req.Validate, User: req.User}
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
	var checker service.OnlineChecker
	if s.mqtt != nil {
		checker = s.mqtt
	}
	list, err := s.userSvc.DeviceList(c.Request.Context(), currentUserID(c), checker)
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
