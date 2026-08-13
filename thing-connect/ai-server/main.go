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
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/tirtcapi"
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
	if err := db.Migrate(sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	roleStore := mysqlstore.NewRoleBindingStore(sqlDB)
	userRoleStore := mysqlstore.NewUserRoleStore(sqlDB)
	userResourceStore := mysqlstore.NewUserResourceStore(sqlDB)

	staticDir := findStaticDir()

	r := gin.New()
	r.Use(gin.Recovery(), logging.RequestID("ai"), logging.BodyLog(), gin.Logger())
	r.Static("/static", staticDir)
	r.StaticFile("/v1/ai/agent", staticDir+"/agent.html")

	// AI token endpoint (legacy, device-facing)
	aihandler.NewServer(
		cfg.JWTSecret,
		cfg.TirtcAichat.DefaultRoleID,
		cfg.TirtcAichat.BaseURL,
		cfg.Tirtc.AccessKeyID,
		cfg.Tirtc.AppID,
		cfg.Tirtc.SecretKeyID,
		roleStore,
	).Register(r)

	// Agent management (user-facing)
	rolesBaseURL := cfg.TirtcAichat.RolesBaseURL
	if rolesBaseURL == "" {
		rolesBaseURL = cfg.TirtcAichat.BaseURL
	}
	if rolesBaseURL != "" {
		agentCfg := tirtcapi.AgentAPIConfig{
			BaseURL:     rolesBaseURL,
			AppID:       cfg.Tirtc.AppID,
			AccessKeyID: cfg.Tirtc.AccessKeyID,
			SecretKeyID: cfg.Tirtc.SecretKeyID,
		}
		agentClient := tirtcapi.NewAgentAPIClient(agentCfg, &http.Client{Timeout: 10 * time.Second})
		aihandler.NewAgentHandler(agentClient, roleStore, userRoleStore, userResourceStore, cfg.TirtcAichat.DefaultRoleID, cfg.Internal.Key, cfg.TirtcAichat.ResourceQuota, cfg.TirtcAichat.DefaultResources).Register(r, cfg.JWTSecret)
	} else {
		log.Println("tirtc_aichat.base_url not set, agent API disabled")
	}

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
	sqlDB.Close()
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
