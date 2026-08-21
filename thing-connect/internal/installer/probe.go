package installer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/redis/go-redis/v9"
)

type StandardProber struct{}

func NewStandardProber() *StandardProber { return &StandardProber{} }

func (p *StandardProber) Probe(ctx context.Context, draft Draft) error {
	if err := validateDraft(draft); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		results <- result{name: "Redis", err: probeRedis(probeCtx, draft.Redis)}
	}()
	go func() {
		defer wait.Done()
		results <- result{name: "MQTT", err: probeMQTT(probeCtx, draft.MQTT)}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()
	for item := range results {
		if item.err != nil {
			return fmt.Errorf("%s 连接检查失败: %w", item.name, item.err)
		}
	}
	return nil
}

func probeRedis(ctx context.Context, input RedisInput) error {
	client := redis.NewClient(&redis.Options{
		Addr: net.JoinHostPort(input.Host, strconv.Itoa(input.Port)), Password: input.Password, DB: input.DB,
	})
	defer func() { _ = client.Close() }()
	return client.Ping(ctx).Err()
}

func probeMQTT(ctx context.Context, input MQTTInput) error {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	options := mqtt.NewClientOptions()
	options.AddBroker(input.Broker)
	options.SetClientID("thingconnect-setup-" + hex.EncodeToString(random))
	options.SetUsername(input.Username)
	options.SetPassword(input.Password)
	options.SetCleanSession(true)
	options.SetAutoReconnect(false)
	options.SetConnectTimeout(8 * time.Second)
	if strings.HasPrefix(input.Broker, "mqtts://") || strings.HasPrefix(input.Broker, "ssl://") {
		options.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	client := mqtt.NewClient(options)
	token := client.Connect()
	done := make(chan struct{})
	go func() {
		token.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		if err := token.Error(); err != nil {
			return err
		}
		client.Disconnect(100)
		return nil
	}
}
