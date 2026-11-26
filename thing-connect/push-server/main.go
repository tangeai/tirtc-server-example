package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	"thing-connect/internal/logging"
	"thing-connect/internal/push"
	"thing-connect/internal/pushwechat"
	"thing-connect/internal/queuehealth"
	"thing-connect/internal/requestguard"
	"thing-connect/internal/servicestatus"
	"thing-connect/internal/store/mysql"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/userauth"
	"thing-connect/push-server/handler"
)

func main() {
	cfg := config.Load(config.ParseFlags())
	logging.Init(cfg.Log.Level, cfg.Log.Format)
	if cfg.JWTSecret == "" || cfg.Internal.Key == "" {
		log.Fatal("缺少内部或用户鉴权配置")
	}
	database, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatal("数据库连接失败")
	}
	defer func() { _ = database.Close() }()
	if err = migrate.RequireSchemaCurrent(database); err != nil {
		log.Fatal(err)
	}
	redis, err := cache.New(cfg.Redis)
	if err != nil {
		log.Fatal("状态上报初始化失败")
	}
	defer func() { _ = redis.Close() }()
	store := mysql.NewPushStore(database)
	app := cfg.Wechat.Apps[cfg.Wechat.DefaultAppID]
	if app.Enabled != nil && !*app.Enabled {
		app.Secret = ""
	}
	channel := pushwechat.Channel{AppID: cfg.Wechat.DefaultAppID, Secret: app.Secret, Config: cfg.Push, Refresh: cache.WeChatRefreshGate(redis)}
	service := push.New(store, channel, channel.AppID, cfg.Push.TemplateID)
	router := gin.New()
	if err = router.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		log.Fatal(err)
	}
	router.Use(gin.Recovery(), logging.RequestID("push"), userauth.EnforceState(database, cfg.JWTSecret))
	probes := map[string]servicestatus.DependencyProbe{"database": servicestatus.SQLProbe(database)}
	servicestatus.RegisterHealth(router, probes)
	handler.Register(router, service, cfg.Internal.Key, cfg.JWTSecret)
	backlog := queuehealth.NewWithMaxAge("push_delivery", mysql.NewPushBacklog(database, false), 5*time.Minute)
	metrics := new(push.DeliveryMetrics)
	status := map[string]servicestatus.DependencyProbe{"database": probes["database"], "delivery": backlog.Health, "wechat": func(context.Context) error {
		if !channel.Ready("crying") && !channel.Ready("quarrel") {
			return errors.New("微信模板未配置")
		}
		return metrics.Health(context.Background())
	}}
	reporter, err := servicestatus.NewReporter(redis, "push-server", status, nil)
	if err != nil {
		log.Fatal(err)
	}
	guard, err := requestguard.New(cfg.Server.RequestLimits)
	if err != nil {
		log.Fatal(err)
	}
	reporter.SetMetrics(servicestatus.MergeMetrics(guard.Metrics, metrics.Metrics, backlog.Metrics, servicestatus.PoolMetrics(database, redis)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var wg sync.WaitGroup
	start := func(run func(context.Context)) { wg.Add(1); go func() { defer wg.Done(); run(ctx) }() }
	start(reporter.Run)
	start(backlog.Run)
	start(func(ctx context.Context) { push.RunMaintenance(ctx, store) })
	for i := 0; i < 8; i++ {
		start(func(ctx context.Context) { push.RunDelivery(ctx, store, channel, metrics) })
	}
	server := http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.HTTPPort), Handler: guard.Wrap(router), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: max(20*time.Second, guard.MaxTimeout()+5*time.Second)}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("推送监听失败: %v", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	wg.Wait()
}
