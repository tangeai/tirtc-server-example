package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
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
	callservice "thing-connect/internal/call"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/logging"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/queuehealth"
	"thing-connect/internal/requestguard"
	"thing-connect/internal/room"
	roomsignals "thing-connect/internal/room/signals"
	"thing-connect/internal/servicestatus"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/tirtcapi"
	"thing-connect/internal/userauth"
	"thing-connect/internal/voip"
	"thing-connect/internal/weblanguage"
	"thing-connect/internal/webui"
)

//go:embed handler/call-i18n.json
var languageMessages string

func main() {
	cfgPath := config.ParseFlags()
	cfg := config.Load(cfgPath)

	logging.Init(cfg.Log.Level, cfg.Log.Format)

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
	dynamicClient, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, rdb)
	if err != nil {
		log.Fatalf("dynamic config: %v", err)
	}
	startupConfigCtx, cancelStartupConfig := context.WithTimeout(context.Background(), 10*time.Second)
	mqttConfig, _, err := dynamicconfig.ResolveMQTT(startupConfigCtx, dynamicClient, "call-server", cfg.MQTT)
	cancelStartupConfig()
	if err != nil {
		log.Fatalf("mqtt config: %v", err)
	}
	broker, err := mqttc.New(mqttConfig, rdb)
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

	callRooms := cache.NewCallRooms(rdb)
	calls := callservice.NewService(callRooms, mqttc.NewCallNotifier(broker))
	callHTTP := callhandler.NewServer(cfg, sqlDB, calls, broker, devStore, mysqlstore.NewCallCleanupStore(sqlDB))
	remarks := voip.NewRemarks(mysqlstore.NewVoipRemarkStore(sqlDB), mqttc.NewRemarkNotifier(broker))
	callHTTP.SetRemarks(remarks)
	callHTTP.SetContactNames(mysqlstore.NewContactNamesStore(sqlDB))
	callHTTP.Register(r)
	dynamicClient, dynamicRefs, err := callDynamicConfig(dynamicClient, cfg.Tirtc, callHTTP)
	if err != nil {
		log.Fatalf("dynamic config: %v", err)
	}
	language, err := weblanguage.New(cfg.Web.Language)
	if err != nil {
		log.Fatalf("web language: %v", err)
	}
	dynamicRefs = append(dynamicRefs, dynamicconfig.Ref{Namespace: "call-server", Key: "web.language", Apply: func(snapshot dynamicconfig.Snapshot) error { return language.Apply(snapshot.Value, snapshot.Revision) }})
	statusCtx, statusCancel := context.WithCancel(context.Background())
	configCtx, cancelConfig := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dynamicClient.ApplyInitial(configCtx, dynamicRefs); err != nil {
		cancelConfig()
		log.Fatalf("dynamic config: %v", err)
	}
	cancelConfig()
	webui.Register(r, "/v1/call", language, languageMessages)
	roomPolicy, err := cfg.Room.Policy()
	if err != nil {
		log.Fatal(err)
	}
	pepper := hmac.New(sha256.New, []byte(cfg.Internal.Key))
	pepper.Write([]byte("thingconnect/intercom/password/v1"))
	rtc := callHTTP.Config().Tirtc
	rooms, err := room.New(mysqlstore.NewRoomStore(sqlDB), tirtcapi.NewRoomClient(tirtcapi.AgentAPIConfig{BaseURL: "https://api-tirtc.tange365.com", AppID: rtc.AppID, AccessKeyID: rtc.AccessKeyID, SecretKeyID: rtc.SecretKeyID}, nil), &roomsignals.Adapter{Redis: rdb, Broker: broker}, pepper.Sum(nil), roomPolicy)
	if err != nil {
		log.Fatalf("room: %v", err)
	}
	roomRefs := []dynamicconfig.Ref{{Namespace: "call-server", Key: "room.policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
		if snapshot.Revision == 0 {
			return rooms.UpdatePolicy(roomPolicy)
		}
		p, e := room.ParseConfig(snapshot.Value)
		if e != nil {
			return e
		}
		return rooms.UpdatePolicy(p)
	}}}
	roomConfigCtx, roomConfigCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dynamicClient.ApplyInitial(roomConfigCtx, roomRefs); err != nil {
		log.Fatalf("room config: %v", err)
	}
	roomConfigCancel()
	dynamicRefs = append(dynamicRefs, roomRefs...)
	callHTTP.SetIntercom(rooms)
	callhandler.RegisterIntercom(r, cfg.JWTSecret, rooms)
	callDone := make(chan struct{})
	go func() { defer close(callDone); calls.Run(statusCtx) }()
	remarksDone := make(chan struct{})
	go func() { defer close(remarksDone); remarks.Run(statusCtx) }()
	roomDone := make(chan struct{})
	go func() {
		defer close(roomDone)
		rooms.Run(statusCtx, func(e error) { log.Printf("room worker: %v", e) })
	}()
	go dynamicClient.Run(statusCtx, dynamicRefs)
	backlog := queuehealth.New("remark", mysqlstore.NewRemarkBacklog(sqlDB))
	callBacklog := queuehealth.New("call_notification", callRooms)
	statusProbes := make(map[string]servicestatus.DependencyProbe, len(probes)+2)
	for name, probe := range probes {
		statusProbes[name] = probe
	}
	statusProbes["remark_outbox"] = backlog.Health
	statusProbes["call_notification_outbox"] = callBacklog.Health
	guard, guardErr := requestguard.New(cfg.Server.RequestLimits)
	if guardErr != nil {
		log.Fatal(guardErr)
	}
	reporter, err := servicestatus.NewReporter(rdb, "call-server", statusProbes, dynamicClient.Revisions)
	if err != nil {
		log.Fatalf("service status: %v", err)
	}
	reporter.SetMetrics(servicestatus.MergeMetrics(broker.Metrics, backlog.Metrics, callBacklog.Metrics, guard.Metrics, servicestatus.PoolMetrics(sqlDB, rdb)))
	backlogDone := make(chan struct{})
	go func() { defer close(backlogDone); backlog.Run(statusCtx) }()
	callBacklogDone := make(chan struct{})
	go func() { defer close(callBacklogDone); callBacklog.Run(statusCtx) }()
	go reporter.Run(statusCtx)

	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	srv := &http.Server{
		Addr: addr, Handler: guard.Wrap(r),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       max(30*time.Second, guard.MaxTimeout()),
		WriteTimeout:      max(60*time.Second, guard.MaxTimeout()+5*time.Second),
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
	<-backlogDone
	<-callBacklogDone
	<-callDone
	<-roomDone
	<-remarksDone
	broker.Close()
	if err := rdb.Close(); err != nil {
		log.Printf("call-server close redis: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("call-server close database: %v", err)
	}
	log.Printf("call-server stopped")
}
