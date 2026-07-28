package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/service"
	mysqlstore "thing-connect/internal/store/mysql"
)

type Server struct {
	devSvc *service.DeviceService

	// Legacy fields for integration test compatibility.
	// When devSvc is nil and DB+RDB are set, Register() auto-wires the service.
	DB        *sqlx.DB
	RDB       *redis.Client
	JWTSecret string
}

func NewServer(devSvc *service.DeviceService) *Server {
	return &Server{devSvc: devSvc}
}

func (s *Server) Register(r *gin.Engine) {
	if s.devSvc == nil && s.DB != nil {
		devStore := mysqlstore.NewDeviceStore(s.DB)
		cacheStore := mysqlstore.NewCacheStore(s.RDB)
		s.devSvc = service.NewDeviceService(devStore, cacheStore, s.JWTSecret, service.DefaultServiceConfig())
	}
	v1 := r.Group("/v1")
	v1.POST("/device/report", s.postReport)
	v1.POST("/device/token", s.postToken)
	v1.GET("/device/tts", s.getTTS)
}

type reportReq struct {
	MAC string `json:"mac"`

	// Legacy compatibility only. Older clients may still send these fields,
	// but report identity and processing are based solely on MAC.
	ChipUID    string `json:"chip_uid"`
	DeviceRand string `json:"device_rand"`
	DeviceID   string `json:"device_id"` // 可选，预烧设备上报时携带
}

func (s *Server) postReport(c *gin.Context) {
	var req reportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	log.Printf("[report] mac=%s", req.MAC)

	hdr := service.ReportHeaders{
		DeviceID:     c.GetHeader("X-Device-Id"),
		Timestamp:    c.GetHeader("X-Timestamp"),
		Nonce:        c.GetHeader("X-Nonce"),
		Signature:    c.GetHeader("X-Signature"),
		HasDeviceID:  hasHeaderKey(c.Request, "X-Device-Id"),
		HasTimestamp: hasHeaderKey(c.Request, "X-Timestamp"),
		HasNonce:     hasHeaderKey(c.Request, "X-Nonce"),
		HasSignature: hasHeaderKey(c.Request, "X-Signature"),
	}

	result, err := s.devSvc.Report(c.Request.Context(), c.ClientIP(), hdr, req.MAC, req.DeviceID)
	if err != nil {
		if errors.Is(err, service.ErrIPFingerprintLimit) {
			secs := int(s.devSvc.IPRateLimitWindow().Seconds())
			c.Header("Retry-After", strconv.Itoa(secs))
		} else if errors.Is(err, service.ErrRateLimit) {
			secs := int(s.devSvc.RateLimitWindow().Seconds())
			c.Header("Retry-After", strconv.Itoa(secs))
		} else if errors.Is(err, service.ErrVerifyPending) || errors.Is(err, service.ErrGlobalBusy) {
			secs := int(s.devSvc.CodeTTL().Seconds())
			c.Header("Retry-After", strconv.Itoa(secs))
		}
		apiresp.FromError(c, err)
		return
	}
	log.Printf("[report] OK code=%s", result.Code)
	apiresp.OK(c, gin.H{"code": result.Code, "temp_token": result.TempToken, "temp_client_id": result.TempClientID})
}

// hasHeaderKey returns true if the HTTP header key is present in the request,
// regardless of whether its value is empty.
func hasHeaderKey(r *http.Request, key string) bool {
	_, ok := r.Header[http.CanonicalHeaderKey(key)]
	return ok
}

func (s *Server) postToken(c *gin.Context) {
	deviceID := c.GetHeader("X-Device-Id")
	tsStr := c.GetHeader("X-Timestamp")
	nonce := c.GetHeader("X-Nonce")
	sigB64 := c.GetHeader("X-Signature")
	mac := c.GetHeader("X-MAC")

	tok, err := s.devSvc.Token(c.Request.Context(), deviceID, tsStr, nonce, sigB64, mac)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"mqtt_token": tok})
}
