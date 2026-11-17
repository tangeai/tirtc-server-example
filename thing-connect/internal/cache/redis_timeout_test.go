package cache

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/config"
)

// hungRedis accepts connections and never answers.
func hungRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestRedisCallsReturnAtCallerDeadline(t *testing.T) {
	opts := options(config.RedisCfg{Addr: hungRedis(t)})
	opts.PoolSize = 1
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	measure := func(name string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := client.Get(ctx, "k").Err()
		if elapsed := time.Since(start); elapsed > time.Second || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: returned after %v err=%v, want the 300ms caller deadline", name, elapsed, err)
		}
	}
	measure("hung read")

	held := make(chan struct{})
	go func() {
		close(held)
		_ = client.Get(context.Background(), "hold").Err()
	}()
	<-held
	time.Sleep(50 * time.Millisecond)
	measure("pool wait")
}
