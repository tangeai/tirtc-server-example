package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/servicestatus"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/userauth"
	"thing-connect/voip-server/handler"
)

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		slog.Error("db open failed", "err", err)
		os.Exit(1)
	}
	if err := mysqlmigrate.RequireSchemaCurrent(sqlDB); err != nil {
		slog.Error("db schema check failed", "err", err)
		os.Exit(1)
	}

	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		slog.Error("redis init failed", "err", err)
		os.Exit(1)
	}
	broker, err := mqttc.New(cfg.MQTT, rdb)
	if err != nil {
		slog.Error("mqtt init failed", "err", err)
		os.Exit(1)
	}

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		slog.Error("invalid trusted proxies", "err", err)
		os.Exit(1)
	}
	r.Use(gin.Recovery(), logging.RequestID("voip"), logging.BodyLog(), gin.Logger())
	r.Use(userauth.EnforceState(sqlDB, cfg.JWTSecret))
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb), "mqtt": broker.Ping}
	servicestatus.RegisterHealth(r, probes)

	voipHTTP := handler.NewServer(cfg, sqlDB, rdb, broker)
	voipHTTP.Register(r)
	dynamicClient, dynamicRefs, err := voipDynamicConfig(cfg, rdb, voipHTTP)
	if err != nil {
		slog.Error("dynamic config init failed", "err", err)
		os.Exit(1)
	}
	statusCtx, statusCancel := context.WithCancel(context.Background())
	configCtx, cancelConfig := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dynamicClient.ApplyInitial(configCtx, dynamicRefs); err != nil {
		cancelConfig()
		slog.Error("dynamic config load failed", "err", err)
		os.Exit(1)
	}
	cancelConfig()
	go dynamicClient.Run(statusCtx, dynamicRefs)
	reporter, err := servicestatus.NewReporter(rdb, "voip-server", probes, dynamicClient.Revisions)
	if err != nil {
		slog.Error("service status init failed", "err", err)
		os.Exit(1)
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
		slog.Info("voip-server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server run failed", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown: SIGINT/SIGTERM → drain HTTP → close MQTT → close Redis → close DB.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("voip-server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("voip-server shutdown", "err", err)
	}
	statusCancel()
	broker.Close()
	if err := rdb.Close(); err != nil {
		slog.Error("voip-server close redis", "err", err)
	}
	if err := sqlDB.Close(); err != nil {
		slog.Error("voip-server close database", "err", err)
	}
	slog.Info("voip-server stopped")
}
