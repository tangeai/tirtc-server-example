package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
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
	"thing-connect/internal/installer"
	"thing-connect/internal/logging"
	"thing-connect/internal/servicestatus"
	adminmysql "thing-connect/internal/store/mysql/admin"
	installermysql "thing-connect/internal/store/mysql/installer"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
)

func main() {
	configPath := flag.String("c", "config.yaml", "admin-server config file")
	initAdmin := flag.Bool("init-admin", false, "create the first super administrator and exit")
	migrateOnly := flag.Bool("migrate-only", false, "apply core and admin database migrations and exit")
	requireRuntimeTarget := flag.Bool("require-runtime-target", false, "require migration DSN to match every configured runtime service")
	initEmail := flag.String("init-email", "", "email for the first super administrator")
	initNickName := flag.String("init-nick-name", "", "nick name for the first super administrator")
	prepareSetup := flag.Bool("prepare-setup", false, "authorize a new empty deployment for one-time web setup")
	validateConfigBundle := flag.Bool("validate-config-bundle", false, "strictly validate required and configured optional service configs and exit")
	deployRoot := flag.String("deploy-root", "", "deployment root used by first-run setup")
	setupPort := flag.Int("setup-port", 9000, "HTTP port used before the Admin config exists")
	setupBind := flag.String("setup-bind", "127.0.0.1", "listen address used only during first-run setup")
	setupStaticDir := flag.String("setup-static-dir", "static", "Admin Web static directory used during first-run setup")
	supervisorCTL := flag.String("supervisorctl", "supervisorctl", "Supervisor control client")
	supervisorGroup := flag.String("supervisor-group", "thing-connect", "Supervisor service group")
	flag.Parse()

	root, err := resolveDeployRoot(*deployRoot, *configPath)
	if err != nil {
		log.Fatal(err)
	}
	setupOptions := installer.Options{
		DeployRoot: root, ConfigPath: *configPath, StaticDir: *setupStaticDir, HTTPPort: *setupPort, SetupBind: *setupBind,
		SupervisorCTL: *supervisorCTL, SupervisorGroup: *supervisorGroup,
	}
	if *validateConfigBundle {
		if err := installer.ValidateConfiguredServiceBundle(root); err != nil {
			log.Fatalf("validate configs: %v", err)
		}
		log.Print("all service configs are valid")
		return
	}
	if *prepareSetup {
		token, err := installer.PrepareFirstRun(setupOptions)
		if err != nil {
			log.Fatalf("prepare first-run setup: %v", err)
		}
		fmt.Printf("ThingConnect setup token (shown once): %s\n", token)
		return
	}
	mode, err := installer.DetectMode(setupOptions)
	if err != nil {
		log.Fatalf("detect first-run mode: %v", err)
	}
	if mode == installer.ModeFresh || mode == installer.ModeRecovery {
		if err := runSetupServer(setupOptions); err != nil {
			log.Fatalf("first-run setup: %v", err)
		}
		return
	}

	cfg, err := adminapp.LoadAppConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	logging.Init(cfg.Log.Level, cfg.Log.Format)
	if *migrateOnly {
		if err := validateMigrationPreflight(context.Background(), root, cfg.Database.DSN, *requireRuntimeTarget); err != nil {
			log.Fatalf("migration preflight: %v", err)
		}
	}

	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()
	if *migrateOnly {
		migrationCtx, stopMigration := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stopMigration()
		pending, err := mysqlmigrate.AdminMigrationsPending(sqlDB)
		if err != nil {
			log.Fatalf("inspect pending migrations: %v", err)
		}
		if pending {
			// MySQL DDL commits implicitly. Emit this marker before the first
			// possible DDL so deployment automation treats even a failed,
			// partially applied migration as a schema-changing attempt.
			log.Print("migration_result=change_possible pending migrations detected")
		}
		if err := mysqlmigrate.MigrateAdminContext(migrationCtx, sqlDB); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		if pending {
			log.Print("migration_result=changed database migrations applied")
		} else {
			log.Print("migration_result=unchanged database schema already current")
		}
		return
	}
	if err := mysqlmigrate.RequireAdminSchemaCurrent(sqlDB); err != nil {
		log.Fatalf("schema: %v", err)
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
	if err := configService.MigratePlaintextSecrets(context.Background()); err != nil {
		log.Fatalf("migrate configuration secrets: %v", err)
	}
	if value, _, _, loadErr := configService.Resolved(context.Background(), "system", "mfa.policy", "global", ""); loadErr == nil {
		var policy struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(value, &policy) == nil {
			authService.SetMFAEnabled(policy.Enabled)
		}
	}
	if value, _, _, loadErr := configService.Resolved(context.Background(), "system", "admin.session_policy", "global", ""); loadErr == nil {
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
	if value, _, _, loadErr := configService.Resolved(context.Background(), "system", "admin.session_policy", "global", ""); loadErr == nil {
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
	setupOptions.StaticDir = cfg.Server.StaticDir
	setupOptions.RuntimeDatabaseDSN = cfg.Database.DSN
	bootstrap := newInstaller(setupOptions)
	newSetupHTTP(bootstrap).Register(r)
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
	if _, err := bootstrap.ResumeRuntime(context.Background()); err != nil && !errors.Is(err, installer.ErrPlanStale) && !errors.Is(err, installer.ErrInstallBusy) {
		log.Printf("resume first-run services: %v", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("admin-server shutdown: %v", err)
	}
	if err := bootstrap.Shutdown(ctx); err != nil {
		log.Printf("installer shutdown: %v", err)
	}
}

func validateMigrationPreflight(ctx context.Context, root, migrationDSN string, requireRuntime bool) error {
	assessment, err := installermysql.New().InspectDSN(ctx, migrationDSN)
	if err != nil {
		return err
	}
	switch assessment.Class {
	case installer.DatabaseEmpty, installer.DatabaseManagedOlder, installer.DatabaseManagedCurrent:
	default:
		return fmt.Errorf("target database class %s is not safe for explicit migration", assessment.Class)
	}
	if !requireRuntime {
		return nil
	}
	return installer.ValidateConfiguredRuntimeTarget(root, migrationDSN)
}

func newInstaller(options installer.Options) *installer.Bootstrap {
	return installer.New(options, installer.Dependencies{
		Database: installermysql.New(),
		Bundles:  installer.NewFileBundleStore(options),
		Probes:   installer.NewStandardProber(),
		Runtime:  installer.NewSupervisorController(options.SupervisorCTL, options.SupervisorGroup, options.HTTPPort),
	})
}

func runSetupServer(options installer.Options) error {
	bootstrap := newInstaller(options)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return err
	}
	router.Use(gin.Recovery(), securityHeaders(), requestBodyLimit(128*1024), gin.Logger())
	router.GET("/health/live", func(c *gin.Context) { apiresp.OK(c, gin.H{"status": "live", "mode": "setup"}) })
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, apiresp.JSON{Code: 503, Msg: "等待首次安装完成"})
	})
	newSetupHTTP(bootstrap).Register(router)
	serveStatic(router, options.StaticDir)
	server := &http.Server{
		Addr: net.JoinHostPort(options.SetupBind, fmt.Sprintf("%d", options.HTTPPort)), Handler: router,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("admin-server first-run setup listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		return err
	case <-quit:
	case <-bootstrap.RestartRequested():
		log.Print("first-run configuration committed; restarting into normal Admin mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	if err := bootstrap.Shutdown(ctx); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	return shutdownErr
}

func resolveDeployRoot(configured, configPath string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		root, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		if root == string(filepath.Separator) {
			return "", fmt.Errorf("deploy-root cannot be the filesystem root")
		}
		return root, nil
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return "", err
	}
	serviceDir := filepath.Dir(absConfig)
	if filepath.Base(serviceDir) == "admin-server" {
		parent := filepath.Dir(serviceDir)
		if filepath.Base(parent) == "admin" {
			return filepath.Dir(parent), nil
		}
		return parent, nil
	}
	if executable, execErr := os.Executable(); execErr == nil {
		execDir := filepath.Dir(executable)
		if filepath.Base(execDir) == "admin-server" {
			return filepath.Dir(execDir), nil
		}
	}
	return filepath.Dir(absConfig), nil
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
