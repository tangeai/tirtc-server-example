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
		err error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		if err := probeRedis(probeCtx, draft.Redis); err != nil {
			results <- result{err: fmt.Errorf("%w: %v", ErrRedisUnavailable, err)}
			return
		}
		results <- result{}
	}()
	go func() {
		defer wait.Done()
		if err := probeMQTT(probeCtx, draft.MQTT, draft.OptionalServices); err != nil {
			results <- result{err: fmt.Errorf("%w: %v", ErrMQTTUnavailable, err)}
			return
		}
		results <- result{}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()
	for item := range results {
		if item.err != nil {
			return item.err
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

func probeMQTT(ctx context.Context, input MQTTInput, optional []string) error {
	auth, err := normalizeMQTTAuth(input, optional)
	if err != nil {
		return err
	}
	if auth.mode == mqttAuthUsername {
		clientID, err := randomSetupClientID()
		if err != nil {
			return err
		}
		return probeMQTTConnection(ctx, auth, clientID, auth.username)
	}
	services, err := enabledMQTTServices(optional)
	if err != nil {
		return err
	}
	for _, service := range services {
		clientID := auth.clientIDs[service.Name]
		if err := probeMQTTConnection(ctx, auth, clientID, clientID); err != nil {
			return fmt.Errorf("%s: %w", service.DisplayName, err)
		}
	}
	return nil
}

func randomSetupClientID() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "thingconnect-setup-" + hex.EncodeToString(random), nil
}

func probeMQTTConnection(ctx context.Context, auth normalizedMQTTAuth, clientID, username string) error {
	options := mqtt.NewClientOptions()
	options.AddBroker(auth.broker)
	options.SetClientID(clientID)
	options.SetUsername(username)
	options.SetPassword(auth.password)
	options.SetCleanSession(true)
	options.SetAutoReconnect(false)
	options.SetConnectTimeout(8 * time.Second)
	if strings.HasPrefix(auth.broker, "mqtts://") || strings.HasPrefix(auth.broker, "ssl://") {
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
