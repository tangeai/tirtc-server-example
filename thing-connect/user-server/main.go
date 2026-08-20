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
	"thing-connect/internal/captcha/registry"
	cleanupoutbox "thing-connect/internal/cleanup"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/logging"
	mailerpkg "thing-connect/internal/mailer"
	smtpmailer "thing-connect/internal/mailer/smtp"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/service"
	"thing-connect/internal/servicestatus"
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
	var mqttInitErr error
	if cfg.MQTT.Broker != "" {
		broker, mqttInitErr = mqttc.New(cfg.MQTT, rdb)
		if mqttInitErr != nil {
			log.Printf("mqtt: %v (continuing without MQTT)", mqttInitErr)
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
	// Email verification has its own policy and does not inherit the device
	// binding code's historical 190-second lifetime.
	svcCfg.CodeTTL = 5 * time.Minute

	userStore := mysqlstore.NewUserStore(sqlDB)
	bindStore := mysqlstore.NewBindStore(sqlDB)
	cacheStore := mysqlstore.NewCacheStore(rdb)

	captchaProvider, captchaConfig := configuredCaptcha(cfg)
	var captchaVerifier captchapkg.Verifier = captchapkg.DevVerifier{}
	if captchaProvider == "" {
		log.Println("captcha provider not set, captcha verification disabled (development mode)")
	} else {
		captchaVerifier, err = registry.New(captchaProvider, registry.Config{
			CaptchaID: captchaConfig.CaptchaID, SecretID: captchaConfig.SecretID, SecretKey: captchaConfig.SecretKey,
			AppSecretKey: captchaConfig.AppSecretKey, MiniProgramSecretKey: captchaConfig.MiniProgramSecretKey, PublicConfig: captchaConfig.PublicConfig,
		})
		if err != nil {
			log.Fatalf("captcha: %v", err)
		}
	}

	var emailMailer mailerpkg.Mailer
	if cfg.SMTP.Host == "" {
		log.Println("smtp.host not set, emails will be logged to stdout (dev mode)")
		emailMailer = mailerpkg.DevMailer{}
	} else {
		emailMailer = smtpmailer.New(smtpmailer.Config{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			TLSMode:  cfg.SMTP.TLSMode,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password,
			From:     cfg.SMTP.From,
		})
	}

	userSvc := service.NewUserService(userStore, cacheStore, captchaVerifier, emailMailer, cfg.JWTSecret, svcCfg)
	passwordResetMailQueue := service.NewInMemoryPasswordResetEmailQueue(userSvc.DeliverPasswordResetCode)
	userSvc.SetPasswordResetEmailQueue(passwordResetMailQueue)

	var mqttPub service.MQTTPublisher
	if broker != nil {
		mqttPub = broker
	}
	bindSvc := service.NewBindService(bindStore, cacheStore, mqttPub, svcCfg)
	var dynamicClient *dynamicconfig.Client
	var dynamicRefs []dynamicconfig.Ref
	if cfg.Admin.ServerURL != "" {
		dynamicClient, dynamicRefs, err = userDynamicConfig(cfg, rdb, userSvc, bindSvc)
		if err != nil {
			log.Printf("dynamic config disabled: %v", err)
			dynamicClient = nil
		}
	}

	staticDir := findStaticDir()

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	r.Use(gin.Recovery(), logging.RequestID("user"), logging.BodyLog(), gin.Logger())
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb)}
	if broker != nil {
		probes["mqtt"] = broker.Ping
	} else if mqttInitErr != nil {
		probes["mqtt"] = func(context.Context) error { return mqttInitErr }
	}
	servicestatus.RegisterHealth(r, probes)
	if err := registerServiceDiscovery(r, cfg.Discovery); err != nil {
		log.Fatalf("service discovery: %v", err)
	}
	r.Static("/static", staticDir)
	r.StaticFile("/", staticDir+"/index.html")
	r.StaticFile("/login", staticDir+"/auth.html")
	r.StaticFile("/register", staticDir+"/auth.html")
	r.StaticFile("/forgot-password", staticDir+"/auth.html")
	r.StaticFile("/devices", staticDir+"/devices.html")
	r.StaticFile("/bind", staticDir+"/bind.html")
	r.StaticFile("/player", staticDir+"/player.html")
	r.StaticFile("/contacts", staticDir+"/contacts.html")
	// SDK loads wasm from root path (hardcoded), so expose them at /
	r.StaticFile("/librender.wasm", staticDir+"/js/librender.wasm")
	r.StaticFile("/plugin.wasm", staticDir+"/js/plugin.wasm")

	usrhandler.SetCaptchaConfig(usrhandler.CaptchaConfig{
		Provider: captchaProvider, Enabled: captchaProvider != "", PublicConfig: captchaConfig.PublicConfig,
		CaptchaID: captchaConfig.CaptchaID,
	})
	usrhandler.SetTirtcCredentials(cfg.Tirtc.AppID, cfg.Tirtc.AccessKeyID, cfg.Tirtc.SecretKeyID, cfg.Tirtc.Endpoint)
	roleStore := mysqlstore.NewRoleBindingStore(sqlDB)

	targets := []cleanupoutbox.Target{}
	if cfg.Ai.ServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "ai", URL: strings.TrimRight(cfg.Ai.ServerURL, "/") + "/v1/ai/internal/unbind", InternalKey: cfg.Internal.Key})
	}
	if cfg.Voip.ServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "voip", URL: strings.TrimRight(cfg.Voip.ServerURL, "/") + "/v1/voip/internal/unbind", InternalKey: cfg.Internal.Key})
	}
	if cfg.Call.ServerURL != "" {
		targets = append(targets, cleanupoutbox.Target{Name: "call", URL: strings.TrimRight(cfg.Call.ServerURL, "/") + "/v1/call/internal/unbind", InternalKey: cfg.Internal.Key})
	}
	if len(targets) > 0 && cfg.Internal.Key == "" {
		log.Fatal("config: internal.key must be set when cross-service cleanup URLs are configured")
	}
	outbox := cleanupoutbox.NewOutbox(sqlDB, targets)
	cleanup := &usrhandler.UnbindCleanup{Targets: cleanupoutbox.TargetNames(targets)}
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	go outbox.Run(outboxCtx)
	go passwordResetMailQueue.Run(outboxCtx)
	go runAdminUserCommands(outboxCtx, rdb, userSvc)
	if dynamicClient != nil {
		go dynamicClient.Run(outboxCtx, dynamicRefs)
	}
	var revisions func() map[string]int64
	if dynamicClient != nil {
		revisions = dynamicClient.Revisions
	}
	reporter, err := servicestatus.NewReporter(rdb, "user-server", probes, revisions)
	if err != nil {
		log.Fatalf("service status: %v", err)
	}
	go reporter.Run(outboxCtx)

	usrhandler.NewServer(userSvc, bindSvc, broker, sqlDB, rdb, cfg.JWTSecret, cfg.Call.ServerURL, cfg.Internal.Key, roleStore, cleanup).Register(r)

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
	if err := rdb.Close(); err != nil {
		log.Printf("user-server close redis: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("user-server close database: %v", err)
	}
	log.Printf("user-server stopped")
}

// configuredCaptcha prefers the provider-neutral captcha section and retains
// yidun as a migration path for existing config files.
func configuredCaptcha(cfg *config.Config) (string, config.CaptchaProviderCfg) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Captcha.Provider))
	if provider != "" {
		return provider, cfg.Captcha.Providers[provider]
	}
	if cfg.Yidun.CaptchaID != "" || cfg.Yidun.SecretID != "" || cfg.Yidun.SecretKey != "" {
		return "yidun", config.CaptchaProviderCfg{CaptchaID: cfg.Yidun.CaptchaID, SecretID: cfg.Yidun.SecretID, SecretKey: cfg.Yidun.SecretKey}
	}
	return "", config.CaptchaProviderCfg{}
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
