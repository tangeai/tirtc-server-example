package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	callhandler "thing-connect/call-server/handler"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/servicestatus"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/userauth"
)

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

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
	broker, err := mqttc.New(cfg.MQTT, rdb)
	if err != nil {
		log.Fatalf("mqtt: %v", err)
	}

	devStore := mysqlstore.NewDeviceStore(sqlDB)

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	r.Use(gin.Recovery(), logging.RequestID("call"), logging.BodyLog(), gin.Logger())
	r.Use(userauth.EnforceState(sqlDB, cfg.JWTSecret))
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb), "mqtt": broker.Ping}
	servicestatus.RegisterHealth(r, probes)

	callHTTP := callhandler.NewServer(cfg, sqlDB, rdb, broker, devStore)
	callHTTP.Register(r)
	dynamicClient, dynamicRefs, err := callDynamicConfig(cfg, rdb, callHTTP)
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
	reporter, err := servicestatus.NewReporter(rdb, "call-server", probes, dynamicClient.Revisions)
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
		log.Printf("call-server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown: SIGINT/SIGTERM → drain HTTP → close MQTT → close Redis → close DB.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("call-server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("call-server shutdown: %v", err)
	}
	statusCancel()
	broker.Close()
	if err := rdb.Close(); err != nil {
		log.Printf("call-server close redis: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("call-server close database: %v", err)
	}
	log.Printf("call-server stopped")
}
