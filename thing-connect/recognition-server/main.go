package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/logging"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/recognition"
	"thing-connect/internal/recognitionprovider"
	"thing-connect/internal/servicestatus"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/userauth"
	"thing-connect/recognition-server/handler"
	"time"
)

type notifier struct{ broker *mqttc.Broker }

func (n notifier) Publish(ctx context.Context, id string, payload []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return n.broker.Publish("device/sn_"+id+"/notify", 1, json.RawMessage(payload))
}
func main() {
	cfg := config.Load(config.ParseFlags())
	logging.Init(cfg.Log.Level, cfg.Log.Format)
	sqlDB, e := db.Open(cfg.Database)
	if e != nil {
		log.Fatal(e)
	}
	defer func() { _ = sqlDB.Close() }()
	if e = mysqlmigrate.RequireSchemaCurrent(sqlDB); e != nil {
		log.Fatal(e)
	}
	rdb, e := cache.New(cfg.Redis)
	if e != nil {
		log.Fatal(e)
	}
	defer func() { _ = rdb.Close() }()
	dc, e := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, rdb)
	if e != nil {
		log.Fatal(e)
	}
	startup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	mqttCfg, _, e := dynamicconfig.ResolveMQTT(startup, dc, "recognition-server", cfg.MQTT)
	cancel()
	if e != nil {
		log.Fatal(e)
	}
	broker, e := mqttc.New(mqttCfg, rdb)
	if e != nil {
		log.Fatal(e)
	}
	defer broker.Close()
	service := recognition.New(mysqlstore.NewRecognitionStore(sqlDB), recognitionprovider.New(), cfg.Recognition)
	service.SetNotifier(notifier{broker})
	refs := []dynamicconfig.Ref{{Namespace: "recognition-server", Key: "recognition.models", Apply: func(s dynamicconfig.Snapshot) error {
		if s.Revision == 0 {
			return service.Configure(cfg.Recognition)
		}
		v, e := recognition.DecodeConfig(s.Value)
		if e != nil {
			return e
		}
		var secret struct {
			APIKey string `json:"api_key"`
		}
		if len(s.Secrets) > 0 {
			if e = json.Unmarshal(s.Secrets, &secret); e != nil {
				return e
			}
		}
		v.APIKey = secret.APIKey
		return service.Configure(v)
	}}}
	startup, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	e = dc.ApplyInitial(startup, refs)
	cancel()
	if e != nil {
		log.Fatal(e)
	}
	router := gin.New()
	if e = router.SetTrustedProxies(cfg.Server.TrustedProxies); e != nil {
		log.Fatal(e)
	}
	router.Use(gin.Recovery(), logging.RequestID("recognition"), gin.Logger(), userauth.EnforceState(sqlDB, cfg.JWTSecret))
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(sqlDB), "redis": servicestatus.RedisProbe(rdb), "mqtt": func(context.Context) error {
		if !broker.Connected() {
			return errors.New("mqtt unavailable")
		}
		return nil
	}}
	servicestatus.RegisterHealth(router, probes)
	handler.Register(router, cfg.JWTSecret, service)
	reporter, e := servicestatus.NewReporter(rdb, "recognition-server", probes, dc.Revisions)
	if e != nil {
		log.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){service.Run, reporter.Run, func(c context.Context) { dc.Run(c, refs) }} {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(run)
	}
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.HTTPPort), Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
	go func() {
		log.Printf("recognition-server listening on %s", srv.Addr)
		if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Printf("recognition HTTP stopped: %v", e)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = srv.Shutdown(shutdown); e != nil {
		log.Printf("shutdown: %v", e)
	}
	wg.Wait()
}
