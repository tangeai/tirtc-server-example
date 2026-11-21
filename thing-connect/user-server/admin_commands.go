package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	adminUserCommandStream = "thingconnect:admin:user-commands"
	adminUserCommandGroup  = "user-server"

	adminUserCommandDeliveryTimeout = 30 * time.Second
	// adminUserCommandMinIdle exceeds the longest delivery, so a command that
	// a live consumer is still sending is never taken over by another one.
	adminUserCommandMinIdle       = adminUserCommandDeliveryTimeout + 30*time.Second
	adminUserCommandMaxDeliveries = 5
)

// adminUserCommands consumes one command at a time: messages read in a batch
// would age while earlier ones are sent and could exceed MinIdle unprocessed.
type adminUserCommands struct {
	client   *redis.Client
	stream   string
	group    string
	consumer string
	minIdle  time.Duration
	deliver  func(ctx context.Context, email string) error
}

func runAdminUserCommands(ctx context.Context, client *redis.Client, deliver func(context.Context, string) error) {
	host, _ := os.Hostname()
	c := &adminUserCommands{client: client, stream: adminUserCommandStream, group: adminUserCommandGroup, consumer: host + "-" + strconv.Itoa(os.Getpid()), minIdle: adminUserCommandMinIdle, deliver: deliver}
	c.run(ctx)
}

func (c *adminUserCommands) run(ctx context.Context) {
	if err := c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err(); err != nil && !isBusyGroup(err) {
		log.Printf("admin command group: %v", err)
		return
	}
	for ctx.Err() == nil {
		c.reclaim(ctx)
		streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: c.group, Consumer: c.consumer, Streams: []string{c.stream, ">"}, Count: 1, Block: 5 * time.Second}).Result()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			log.Printf("read admin user commands: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				c.process(ctx, message)
			}
		}
	}
}

// reclaim takes over commands idle longer than minIdle, one at a time, and
// drops a command once it has been delivered adminUserCommandMaxDeliveries
// times. XCLAIM rechecks the idle time, so concurrent consumers claim a
// command at most once between them.
func (c *adminUserCommands) reclaim(ctx context.Context) {
	pending, err := c.client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: c.stream, Group: c.group, Start: "-", End: "+", Count: 100}).Result()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("list pending admin user commands: %v", err)
		}
		return
	}
	for _, p := range pending {
		if ctx.Err() != nil || p.Idle < c.minIdle {
			continue
		}
		if p.RetryCount >= adminUserCommandMaxDeliveries {
			slog.Warn("admin user command dropped after repeated delivery failures", "id", p.ID, "deliveries", p.RetryCount)
			_ = c.client.XAck(ctx, c.stream, c.group, p.ID).Err()
			continue
		}
		claimed, err := c.client.XClaim(ctx, &redis.XClaimArgs{Stream: c.stream, Group: c.group, Consumer: c.consumer, MinIdle: c.minIdle, Messages: []string{p.ID}}).Result()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("claim admin user command: %v", err)
			}
			return
		}
		for _, message := range claimed {
			c.process(ctx, message)
		}
	}
}

func (c *adminUserCommands) process(ctx context.Context, message redis.XMessage) {
	commandType, _ := message.Values["type"].(string)
	email, _ := message.Values["email"].(string)
	if commandType != "password_reset_email" || email == "" {
		_ = c.client.XAck(ctx, c.stream, c.group, message.ID).Err()
		return
	}
	deliverCtx, cancel := context.WithTimeout(ctx, adminUserCommandDeliveryTimeout)
	err := c.deliver(deliverCtx, email)
	cancel()
	if err != nil {
		slog.Warn("admin password reset email failed, will retry", "id", message.ID, "timeout", errors.Is(err, context.DeadlineExceeded))
		return
	}
	_ = c.client.XAck(ctx, c.stream, c.group, message.ID).Err()
}

func isBusyGroup(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}
