package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/cache"
	captchapkg "thing-connect/internal/captcha"
	"thing-connect/internal/captcha/yidun"
	cleanupoutbox "thing-connect/internal/cleanup"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	mailerpkg "thing-connect/internal/mailer"
	smtpmailer "thing-connect/internal/mailer/smtp"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/service"
	mysqlstore "thing-connect/internal/store/mysql"
	usrhandler "thing-connect/user-server/handler"
)

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

	service.RegisterErrors()

	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	var broker *mqttc.Broker
	if cfg.MQTT.Broker != "" {
		broker, err = mqttc.New(cfg.MQTT, rdb)
		if err != nil {
			log.Printf("mqtt: %v (continuing without MQTT)", err)
		}
	}

	svcCfg := service.ServiceConfig{
		QuotaPerUser:     cfg.Service.QuotaPerUser,
		CodeTTL:          cfg.Service.CodeTTL,
		RateLimitWindow:  cfg.Service.RateLimitWindow,
		RateLimitMaxHits: cfg.Service.RateLimitMaxHits,
		TokenExpiry:      cfg.Service.TokenExpiry,
		MQTTACKTimeout:   cfg.Service.MQTTACKTimeout,
	}

	userStore := mysqlstore.NewUserStore(sqlDB)
	bindStore := mysqlstore.NewBindStore(sqlDB)
	cacheStore := mysqlstore.NewCacheStore(rdb)

	var captchaVerifier captchapkg.Verifier
	if cfg.Yidun.SecretID == "" {
		log.Println("yidun.secret_id not set, captcha verification disabled (dev mode)")
		captchaVerifier = captchapkg.DevVerifier{}
	} else {
		captchaVerifier = yidun.New(cfg.Yidun.SecretID, cfg.Yidun.SecretKey)
	}

	var emailMailer mailerpkg.Mailer
	if cfg.SMTP.Host == "" {
		log.Println("smtp.host not set, emails will be logged to stdout (dev mode)")
		emailMailer = mailerpkg.DevMailer{}
	} else {
		emailMailer = smtpmailer.New(smtpmailer.Config{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password,
			From:     cfg.SMTP.From,
		})
	}

	userSvc := service.NewUserService(userStore, cacheStore, captchaVerifier, emailMailer, cfg.JWTSecret, svcCfg)

	var mqttPub service.MQTTPublisher
	if broker != nil {
		mqttPub = broker
	}
	bindSvc := service.NewBindService(bindStore, cacheStore, mqttPub, svcCfg)

	staticDir := findStaticDir()

	r := gin.New()
	r.Use(gin.Recovery(), logging.RequestID("user"), logging.BodyLog(), gin.Logger())
	r.Static("/static", staticDir)
	r.StaticFile("/", staticDir+"/index.html")
	r.StaticFile("/devices", staticDir+"/devices.html")
	r.StaticFile("/bind", staticDir+"/bind.html")
	r.StaticFile("/player", staticDir+"/player.html")
	r.StaticFile("/contacts", staticDir+"/contacts.html")
	// SDK loads wasm from root path (hardcoded), so expose them at /
	r.StaticFile("/librender.wasm", staticDir+"/js/librender.wasm")
	r.StaticFile("/plugin.wasm", staticDir+"/js/plugin.wasm")

	usrhandler.SetCaptchaID(cfg.Yidun.CaptchaID)
	usrhandler.SetTirtcCredentials(cfg.Tirtc.AppID, cfg.Tirtc.AccessKeyID, cfg.Tirtc.SecretKeyID, cfg.Tirtc.Endpoint)
	roleStore := mysqlstore.NewRoleBindingStore(sqlDB)

	targets := []cleanupoutbox.Target{}
	if cfg.Ai.ServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "ai", URL: strings.TrimRight(cfg.Ai.ServerURL, "/") + "/v1/ai/internal/unbind", InternalKey: cfg.Call.InternalKey})
	}
	if cfg.Voip.ServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "voip", URL: strings.TrimRight(cfg.Voip.ServerURL, "/") + "/v1/voip/internal/unbind", InternalKey: cfg.Call.InternalKey})
	}
	if cfg.Call.CallServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "call", URL: strings.TrimRight(cfg.Call.CallServerURL, "/") + "/v1/call/internal/unbind", InternalKey: cfg.Call.InternalKey})
	}
	if len(targets) > 0 && cfg.Call.InternalKey == "" {
		log.Fatal("config: call.internal_key must be set when cross-service cleanup URLs are configured")
	}
	outbox := cleanupoutbox.NewOutbox(sqlDB, targets)
	cleanup := &usrhandler.UnbindCleanup{Enqueue: outbox.Enqueue}
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	go outbox.Run(outboxCtx)

	usrhandler.NewServer(userSvc, bindSvc, broker, cfg.JWTSecret, cfg.Call.CallServerURL, cfg.Call.InternalKey, roleStore, cleanup).Register(r)

	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	srv := &http.Server{
		Addr: addr, Handler: r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	go func() {
		log.Printf("user-server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown: SIGINT/SIGTERM → drain HTTP → close MQTT → close Redis → close DB.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("user-server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("user-server shutdown: %v", err)
	}
	outboxCancel()
	if broker != nil {
		broker.Close()
	}
	rdb.Close()
	sqlDB.Close()
	log.Printf("user-server stopped")
}

// findStaticDir locates the static/ directory relative to the executable so
// the server works regardless of the process's working directory (e.g. when
// started by systemd with an unrelated WorkingDirectory). It supports both
// the dist/ package layout (static/ next to the binary) and running the
// binary from the repo root (static/ under user-server/).
func findStaticDir() string {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable path: %v", err)
	}
	exeDir := filepath.Dir(exe)

	candidates := []string{
		filepath.Join(exeDir, "static"),
		filepath.Join(exeDir, "user-server", "static"),
		filepath.Join(exeDir, "..", "user-server", "static"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	log.Fatalf("static directory not found (tried %v)", candidates)
	return ""
}
