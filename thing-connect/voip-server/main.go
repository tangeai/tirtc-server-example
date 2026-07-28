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
	if err := db.Migrate(sqlDB); err != nil {
		slog.Error("db migrate failed", "err", err)
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
	r.Use(gin.Recovery(), logging.RequestID("voip"), logging.BodyLog(), gin.Logger())

	handler.NewServer(cfg, sqlDB, rdb, broker).Register(r)

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
	broker.Close()
	rdb.Close()
	sqlDB.Close()
	slog.Info("voip-server stopped")
}
