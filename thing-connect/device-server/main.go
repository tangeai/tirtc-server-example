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

	devhandler "thing-connect/device-server/handler"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/service"
	"thing-connect/internal/servicestatus"
	"thing-connect/internal/store"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
)

// globalPendingGC periodically reconciles the global:pending_codes counter
// with the actual number of verify:* keys in Redis. This corrects drift
// caused by expired verification codes whose DelVerifyAndCode was never
// called (user didn't complete bind before TTL).
func globalPendingGC(ctx context.Context, cache store.CacheStore) {
	const interval = 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cache.ReconcileGlobalPending(context.Background()); err != nil {
				log.Printf("globalPendingGC: %v", err)
			}
		}
	}
}

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

	service.RegisterErrors()

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
	svcCfg := service.ServiceConfig{
		QuotaPerUser:               cfg.Service.QuotaPerUser,
		CodeTTL:                    cfg.Service.CodeTTL,
		RateLimitWindow:            cfg.Service.RateLimitWindow,
		RateLimitMaxHits:           cfg.Service.RateLimitMaxHits,
		IPRateLimitWindow:          cfg.Service.IPRateLimitWindow,
		IPRateLimitMaxFingerprints: cfg.Service.IPRateLimitMaxFingerprints,
		GlobalMaxPendingCodes:      cfg.Service.GlobalMaxPendingCodes,
		TokenExpiry:                cfg.Service.TokenExpiry,
		MQTTACKTimeout:             cfg.Service.MQTTACKTimeout,
	}

	devStore := mysqlstore.NewDeviceStore(sqlDB)
	cacheStore := mysqlstore.NewCacheStore(rdb)
	devSvc := service.NewDeviceService(devStore, cacheStore, cfg.JWTSecret, svcCfg)
	dynamicClient, dynamicRefs, err := deviceDynamicConfig(cfg, rdb, devSvc)
	if err != nil {
		log.Fatalf("dynamic config: %v", err)
	}
	configCtx, cancelConfig := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dynamicClient.ApplyInitial(configCtx, dynamicRefs); err != nil {
		cancelConfig()
		log.Fatalf("dynamic config: %v", err)
	}
	cancelConfig()

	// Periodically reconcile global:pending_codes counter to correct drift
	// caused by expired verification codes whose DelVerifyAndCode was never
	// called (user didn't complete bind before TTL).
	gcCtx, gcCancel := context.WithCancel(context.Background())
	go globalPendingGC(gcCtx, cacheStore)
	go dynamicClient.Run(gcCtx, dynamicRefs)

	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	r.Use(gin.Recovery(), logging.RequestID("device"), logging.BodyLog(), gin.Logger())
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb)}
	servicestatus.RegisterHealth(r, probes)
	devhandler.NewServer(devSvc).Register(r)
	statusCtx, statusCancel := context.WithCancel(context.Background())
	reporter, err := servicestatus.NewReporter(rdb, "device-server", probes, dynamicClient.Revisions)
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
		log.Printf("device-server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown: SIGINT/SIGTERM → drain HTTP → stop GC → close Redis → close DB.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("device-server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("device-server shutdown: %v", err)
	}
	gcCancel()
	statusCancel()
	if err := rdb.Close(); err != nil {
		log.Printf("device-server close redis: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("device-server close database: %v", err)
	}
	log.Printf("device-server stopped")
}
