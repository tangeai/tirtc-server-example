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
	"thing-connect/internal/push"
	"thing-connect/internal/queuehealth"
	"thing-connect/internal/recognition"
	"thing-connect/internal/recognitionprovider"
	"thing-connect/internal/requestguard"
	"thing-connect/internal/servicestatus"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/userauth"
	"thing-connect/recognition-server/handler"
	"time"
)

type notifier struct{ broker *mqttc.Broker }

func (n notifier) Publish(ctx context.Context, id string, payload []byte) error {
	return n.broker.PublishContext(ctx, "device/sn_"+id+"/notify", 1, json.RawMessage(payload))
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
	var dc *dynamicconfig.Client
	var revisions func() map[string]int64
	mqttCfg := cfg.MQTT
	if cfg.Admin.ServerURL != "" {
		dc, e = dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, rdb)
		if e != nil {
			log.Fatal(e)
		}
		revisions = dc.Revisions
		startup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		mqttCfg, _, e = dynamicconfig.ResolveMQTT(startup, dc, "recognition-server", cfg.MQTT)
		cancel()
	} else {
		e = dynamicconfig.ValidateMQTT(mqttCfg)
	}
	if e != nil {
		log.Fatal(e)
	}
	broker, e := mqttc.New(mqttCfg, rdb)
	if e != nil {
		log.Fatal(e)
	}
	defer broker.Close()
	store := mysqlstore.NewRecognitionStore(sqlDB)
	service := recognition.New(store, recognitionprovider.New(), cfg.Recognition)
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
	if dc != nil {
		startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e = dc.ApplyInitial(startup, refs)
		cancel()
		if e != nil {
			log.Fatal(e)
		}
	}
	refs = append(refs, dynamicconfig.Ref{Namespace: "push-server", Key: "event.delivery_policy", Apply: func(s dynamicconfig.Snapshot) error {
		p := cfg.Push.Delivery()
		if s.Revision != 0 {
			var err error
			p, err = push.ParseDeliveryPolicy(s.Value)
			if err != nil {
				return err
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return store.ConfigureSource(ctx, p, s.Revision, cfg.Push.ServerURL != "")
	}})
	if dc == nil {
		for _, ref := range refs {
			if e = ref.Apply(dynamicconfig.Snapshot{}); e != nil {
				log.Fatal(e)
			}
		}
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
	backlog := queuehealth.New("recognition", mysqlstore.NewRecognitionBacklog(sqlDB))
	statusProbes := make(map[string]servicestatus.DependencyProbe, len(probes)+1)
	for name, probe := range probes {
		statusProbes[name] = probe
	}
	statusProbes["recognition_outbox"] = backlog.Health
	guard, guardErr := requestguard.New(cfg.Server.RequestLimits)
	if guardErr != nil {
		log.Fatal(guardErr)
	}
	queueSample := mysqlstore.NewPushBacklog(sqlDB, true)
	userBacklog := queuehealth.NewWithMaxAge("recognition_user_event", queueSample, 5*time.Minute)
	statusProbes["source_target"] = store.SourceHealth
	statusProbes["source_deadline"] = queueSample.TimeBudgetHealth
	statusProbes["recognition_user_event_outbox"] = userBacklog.Health
	reporter, e := servicestatus.NewReporter(rdb, "recognition-server", statusProbes, revisions)
	if e != nil {
		log.Fatal(e)
	}
	reporter.SetMetrics(servicestatus.MergeMetrics(store.NotificationMetrics, queueSample.Metrics, userBacklog.Metrics, broker.Metrics, backlog.Metrics, guard.Metrics, servicestatus.PoolMetrics(sqlDB, rdb)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var wg sync.WaitGroup
	runners := []func(context.Context){service.Run, reporter.Run, backlog.Run, userBacklog.Run}
	if dc != nil {
		runners = append(runners, func(c context.Context) { dc.Run(c, refs) })
	}
	runners = append(runners, func(ctx context.Context) {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			if err := store.CleanUserEvents(ctx); err != nil {
				log.Print("来源队列维护失败")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	if cfg.Push.ServerURL != "" {
		publisher := recognition.Publisher{Queue: store, BaseURL: cfg.Push.ServerURL, InternalKey: cfg.Internal.Key}
		for i := 0; i < 2; i++ {
			runners = append(runners, publisher.Run)
		}
	}
	for _, run := range runners {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(run)
	}
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.HTTPPort), Handler: guard.Wrap(router), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: max(30*time.Second, guard.MaxTimeout()), WriteTimeout: max(30*time.Second, guard.MaxTimeout()+5*time.Second), IdleTimeout: 90 * time.Second}
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
