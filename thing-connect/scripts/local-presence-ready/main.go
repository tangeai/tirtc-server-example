// Command local-presence-ready waits for device-list availability after a local fleet restart.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/presence"
	"thing-connect/internal/presence/redisview"
)

func main() {
	os.Exit(run())
}

func run() int {
	path := flag.String("config", "", "user-server configuration")
	flag.Parse()
	cfg, err := config.LoadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法读取用户服务配置")
		return 1
	}
	client, err := cache.New(cfg.Redis)
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法连接用户服务使用的 Redis")
		return 1
	}
	defer func() {
		if err := client.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "关闭 Redis 客户端失败")
		}
	}()
	// Exercise the same missing-projection branch as an account with an offline
	// device. A colon is outside the device ID alphabet. This probe is read-only.
	view := redisview.DeviceList{Client: client}
	ctx, cancel := context.WithTimeout(context.Background(), presence.TTL+time.Minute)
	defer cancel()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if _, err := view.DeviceOnline(ctx, []string{":local-deployment-check:"}); err == nil {
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "设备列表仍不可读，请检查 Device 的 MQTT、Redis 和在线状态消费心跳")
			return 1
		case <-tick.C:
		}
	}
}
