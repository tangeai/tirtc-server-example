package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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
	"github.com/redis/go-redis/v9"

	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/cache"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/servicestatus"
	adminmysql "thing-connect/internal/store/mysql/admin"
)

func main() {
	configPath := flag.String("c", "config.yaml", "admin-server config file")
	initAdmin := flag.Bool("init-admin", false, "create the first super administrator and exit")
	migrateOnly := flag.Bool("migrate-only", false, "apply core and admin database migrations and exit")
	initEmail := flag.String("init-email", "", "email for the first super administrator")
	initNickName := flag.String("init-nick-name", "", "nick name for the first super administrator")
	flag.Parse()

	cfg, err := adminapp.LoadAppConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	logging.Init(cfg.Log.Level, cfg.Log.Format)

	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()
	if err := db.MigrateAdmin(sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if *migrateOnly {
		log.Print("database migrations applied")
		return
	}
	store := adminapp.NewStore(sqlDB)
	if err := store.SeedDefaults(context.Background()); err != nil {
		log.Fatalf("seed admin defaults: %v", err)
	}
	if *initAdmin {
		password := os.Getenv("ADMIN_INIT_PASSWORD")
		if *initEmail == "" || password == "" {
			log.Fatal("-init-email and ADMIN_INIT_PASSWORD are required for -init-admin")
		}
		hash, err := adminapp.HashAdminPassword(password)
		if err != nil {
			log.Fatal(err)
		}
		nickName := strings.TrimSpace(*initNickName)
		if nickName == "" {
			nickName = strings.SplitN(*initEmail, "@", 2)[0]
		}
		id, err := store.BootstrapAdmin(context.Background(), *initEmail, nickName, hash)
		if err != nil {
			log.Fatalf("initialize administrator: %v", err)
		}
		log.Printf("first administrator created: id=%d email=%s", id, strings.ToLower(*initEmail))
		return
	}

	cipher, err := adminapp.NewCipher(cfg.Security.ConfigEncryptionKeyID, cfg.Security.ConfigEncryptionKey)
	if err != nil {
		log.Fatalf("security: %v", err)
	}
	redisClient, err := cache.New(cfg.Redis)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("close redis: %v", err)
		}
	}()
	authService, err := adminapp.NewAuthService(store, cipher, cache.NewAdminMFAChallengeStore(redisClient), adminapp.AuthConfig{
		JWTSecret: cfg.Admin.JWTSecret, Issuer: cfg.Admin.Issuer,
		AccessTTL: cfg.Admin.AccessTTL, RefreshTTL: cfg.Admin.RefreshTTL,
		ChallengeTTL: cfg.Admin.ChallengeTTL, MFAEnabled: cfg.Admin.MFAIsEnabled(),
	})
	if err != nil {
		log.Fatalf("admin auth: %v", err)
	}
	access, err := adminapp.NewAccessController(context.Background(), sqlDB)
	if err != nil {
		log.Fatalf("admin RBAC: %v", err)
	}
	configService := adminapp.NewConfigService(sqlDB, adminapp.DefaultConfigRegistry(), cipher)
	if value, _, revision, loadErr := configService.Resolved(context.Background(), "system", "mfa.policy", "global", ""); loadErr == nil && revision > 0 {
		var policy struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(value, &policy) == nil {
			authService.SetMFAEnabled(policy.Enabled)
		}
	}
	if value, _, revision, loadErr := configService.Resolved(context.Background(), "system", "admin.session_policy", "global", ""); loadErr == nil && revision > 0 {
		var policy struct {
			AccessTTL   string `json:"access_ttl"`
			RefreshTTL  string `json:"refresh_ttl"`
			MaxSessions int    `json:"max_sessions"`
		}
		if json.Unmarshal(value, &policy) == nil {
			accessTTL, accessErr := time.ParseDuration(policy.AccessTTL)
			refreshTTL, refreshErr := time.ParseDuration(policy.RefreshTTL)
			if accessErr == nil && refreshErr == nil {
				authService.SetSessionPolicy(accessTTL, refreshTTL, policy.MaxSessions)
			}
		}
	}
	jobService, err := adminapp.NewJobService(sqlDB, cfg.Job.StorageDir, cfg.Job.MaxBytes)
	if err != nil {
		log.Fatalf("job service: %v", err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go publishConfigEvents(workerCtx, configService, redisClient)
	go jobService.Run(workerCtx)

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	r.Use(gin.Recovery(), logging.RequestID("admin"), securityHeaders(), requestBodyLimit(1024*1024), logging.BodyLog(), gin.Logger())
	r.GET("/health/live", func(c *gin.Context) { apiresp.OK(c, gin.H{"status": "live"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, apiresp.JSON{Code: 503, Msg: "not ready"})
			return
		}
		if err := redisClient.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, apiresp.JSON{Code: 503, Msg: "not ready"})
			return
		}
		apiresp.OK(c, gin.H{"status": "ready"})
	})
	deviceService := adminapp.NewDeviceService(adminmysql.NewDeviceCommandStore(sqlDB))
	adminHTTP := adminapp.NewHTTPServer(store, authService, access, configService, servicestatus.NewAggregator(redisClient), redisClient, jobService, deviceService, cfg.Admin.CookieSecure)
	if value, _, revision, loadErr := configService.Resolved(context.Background(), "system", "admin.session_policy", "global", ""); loadErr == nil && revision > 0 {
		var policy struct {
			LoginWindow string `json:"login_window"`
			LoginMax    int64  `json:"login_max_attempts"`
			MFAWindow   string `json:"mfa_window"`
			MFAMax      int64  `json:"mfa_max_attempts"`
		}
		if json.Unmarshal(value, &policy) == nil {
			loginWindow, loginErr := time.ParseDuration(policy.LoginWindow)
			mfaWindow, mfaErr := time.ParseDuration(policy.MFAWindow)
			if loginErr == nil && mfaErr == nil {
				adminHTTP.SetAuthRatePolicy(loginWindow, policy.LoginMax, mfaWindow, policy.MFAMax)
			}
		}
	}
	go reconcileAdminRuntimeConfig(workerCtx, configService, authService, adminHTTP)
	adminHTTP.Register(r)
	adminHTTP.RegisterInternal(r, cfg.Internal.Key)
	serveStatic(r, cfg.Server.StaticDir)

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Server.HTTPPort), Handler: r,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	go func() {
		log.Printf("admin-server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("admin-server: %v", err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("admin-server shutdown: %v", err)
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(c.Request.URL.Path, "/v1/admin/") {
			headers.Set("Cache-Control", "no-store")
		}
		c.Next()
	}
}

func requestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && !strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func reconcileAdminRuntimeConfig(ctx context.Context, configs *adminapp.ConfigService, auth *adminapp.AuthService, server *adminapp.HTTPServer) {
	revisions := map[string]int64{}
	apply := func() {
		if value, _, revision, err := configs.Resolved(ctx, "system", "mfa.policy", "global", ""); err == nil && revision > revisions["mfa.policy"] {
			var policy struct {
				Enabled bool `json:"enabled"`
			}
			if json.Unmarshal(value, &policy) == nil {
				auth.SetMFAEnabled(policy.Enabled)
				revisions["mfa.policy"] = revision
			}
		}
		if value, _, revision, err := configs.Resolved(ctx, "system", "admin.session_policy", "global", ""); err == nil && revision > revisions["admin.session_policy"] {
			var policy struct {
				AccessTTL   string `json:"access_ttl"`
				RefreshTTL  string `json:"refresh_ttl"`
				MaxSessions int    `json:"max_sessions"`
				LoginWindow string `json:"login_window"`
				LoginMax    int64  `json:"login_max_attempts"`
				MFAWindow   string `json:"mfa_window"`
				MFAMax      int64  `json:"mfa_max_attempts"`
			}
			if json.Unmarshal(value, &policy) == nil {
				accessTTL, accessErr := time.ParseDuration(policy.AccessTTL)
				refreshTTL, refreshErr := time.ParseDuration(policy.RefreshTTL)
				loginWindow, loginErr := time.ParseDuration(policy.LoginWindow)
				mfaWindow, mfaErr := time.ParseDuration(policy.MFAWindow)
				if accessErr == nil && refreshErr == nil && loginErr == nil && mfaErr == nil {
					auth.SetSessionPolicy(accessTTL, refreshTTL, policy.MaxSessions)
					server.SetAuthRatePolicy(loginWindow, policy.LoginMax, mfaWindow, policy.MFAMax)
					revisions["admin.session_policy"] = revision
				}
			}
		}
	}
	apply()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply()
		}
	}
}

func publishConfigEvents(ctx context.Context, configs *adminapp.ConfigService, redisClient *redis.Client) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := configs.PublishPending(ctx, redisClient, 50); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("publish config events: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func serveStatic(r *gin.Engine, configured string) {
	candidates := []string{configured, "admin/admin-web/dist"}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "static"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			r.Static("/assets", filepath.Join(candidate, "assets"))
			r.Static("/admin/assets", filepath.Join(candidate, "assets"))
			favicon := filepath.Join(candidate, "favicon.svg")
			if _, err := os.Stat(favicon); err == nil {
				r.StaticFile("/favicon.svg", favicon)
				r.StaticFile("/admin/favicon.svg", favicon)
			}
			index := filepath.Join(candidate, "index.html")
			r.NoRoute(func(c *gin.Context) {
				if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
					c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "接口不存在"})
					return
				}
				c.File(index)
			})
			return
		}
	}
	log.Printf("admin-web static directory not found: %s", configured)
}
