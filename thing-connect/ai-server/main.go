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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	aihandler "thing-connect/ai-server/handler"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/servicestatus"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/tirtcapi"
	"thing-connect/internal/userauth"
)

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

	if cfg.TirtcAichat.DefaultRoleID == "" {
		cfg.TirtcAichat.DefaultRoleID = "fin63bby1og0"
	}

	// DB.
	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	if err := mysqlmigrate.RequireSchemaCurrent(sqlDB); err != nil {
		log.Fatalf("schema: %v", err)
	}
	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

	roleStore := mysqlstore.NewRoleBindingStore(sqlDB)
	userRoleStore := mysqlstore.NewUserRoleStore(sqlDB)
	userResourceStore := mysqlstore.NewUserResourceStore(sqlDB)

	staticDir := findStaticDir()

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	r.Use(gin.Recovery(), logging.RequestID("ai"), logging.BodyLog(), gin.Logger())
	r.Use(userauth.EnforceState(sqlDB, cfg.JWTSecret))
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb)}
	servicestatus.RegisterHealth(r, probes)
	r.Static("/static", staticDir)
	r.StaticFile("/v1/ai/agent", staticDir+"/agent.html")

	// AI token endpoint (legacy, device-facing)
	legacyAI := aihandler.NewServer(
		cfg.JWTSecret,
		cfg.TirtcAichat.DefaultRoleID,
		cfg.TirtcAichat.BaseURL,
		cfg.Tirtc.AccessKeyID,
		cfg.Tirtc.AppID,
		cfg.Tirtc.SecretKeyID,
		roleStore,
	)
	legacyAI.Register(r)

	// Agent management (user-facing)
	rolesBaseURL := cfg.TirtcAichat.RolesBaseURL
	if rolesBaseURL == "" {
		rolesBaseURL = cfg.TirtcAichat.BaseURL
	}
	agentCfg := tirtcapi.AgentAPIConfig{
		BaseURL:     rolesBaseURL,
		AppID:       cfg.Tirtc.AppID,
		AccessKeyID: cfg.Tirtc.AccessKeyID,
		SecretKeyID: cfg.Tirtc.SecretKeyID,
	}
	agentClient := tirtcapi.NewAgentAPIClient(agentCfg, &http.Client{Timeout: 10 * time.Second})
	agentHTTP := aihandler.NewAgentHandler(agentClient, roleStore, userRoleStore, userResourceStore, cfg.TirtcAichat.DefaultRoleID, cfg.Internal.Key, cfg.TirtcAichat.ResourceQuota, cfg.TirtcAichat.DefaultResources)
	agentHTTP.Register(r, cfg.JWTSecret)
	dynamicClient, dynamicRefs, err := aiDynamicConfig(cfg, rdb, legacyAI, agentHTTP)
	if err != nil {
		log.Fatalf("dynamic config: %v", err)
	}
	statusCtx, statusCancel := context.WithCancel(context.Background())
	configCtx, cancelConfig := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dynamicClient.ApplyInitial(configCtx, dynamicRefs); err != nil {
		cancelConfig()
		log.Fatalf("dynamic config: %v", err)
	}
	cancelConfig()
	go dynamicClient.Run(statusCtx, dynamicRefs)
	reporter, err := servicestatus.NewReporter(rdb, "ai-server", probes, dynamicClient.Revisions)
	if err != nil {
		log.Fatalf("service status: %v", err)
	}
	go reporter.Run(statusCtx)

	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	srv := &http.Server{
		Addr: addr, Handler: r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	go func() {
		log.Printf("ai-server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown: SIGINT/SIGTERM → drain HTTP → close DB.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("ai-server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("ai-server shutdown: %v", err)
	}
	statusCancel()
	if err := rdb.Close(); err != nil {
		log.Printf("ai-server close redis: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("ai-server close database: %v", err)
	}
	log.Printf("ai-server stopped")
}

func findStaticDir() string {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable path: %v", err)
	}
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, "static"),
		filepath.Join(exeDir, "ai-server", "static"),
		filepath.Join(exeDir, "..", "ai-server", "static"),
		filepath.Join(exeDir, "..", "thing-connect", "ai-server", "static"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	log.Fatalf("static directory not found (tried %v)", candidates)
	return ""
}
