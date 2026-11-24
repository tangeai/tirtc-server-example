package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	adminUserCommandStream          = "thingconnect:admin:user-commands"
	adminUserCommandGroup           = "user-server"
	adminUserCommandDeliveryTimeout = 30 * time.Second
	adminUserCommandMinIdle         = adminUserCommandDeliveryTimeout + 30*time.Second
	adminUserCommandMaxDeliveries   = 5
	adminCommandFailureWindow       = time.Hour
)

type adminUserCommands struct {
	client                  *redis.Client
	stream, group, consumer string
	minIdle                 time.Duration
	deliver                 func(context.Context, string) error
}

func newAdminUserCommands(client *redis.Client, deliver func(context.Context, string) error) *adminUserCommands {
	host, _ := os.Hostname()
	return &adminUserCommands{client: client, stream: adminUserCommandStream, group: adminUserCommandGroup, consumer: host + "-" + strconv.Itoa(os.Getpid()), minIdle: adminUserCommandMinIdle, deliver: deliver}
}

func (c *adminUserCommands) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err()
		if err == nil || isBusyGroup(err) {
			err = c.consume(ctx)
		}
		if ctx.Err() == nil {
			log.Printf("admin user command stream: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *adminUserCommands) consume(ctx context.Context) error {
	for ctx.Err() == nil {
		c.reclaim(ctx)
		streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: c.group, Consumer: c.consumer, Streams: []string{c.stream, ">"}, Count: 1, Block: time.Second}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				c.process(ctx, message)
			}
		}
	}
	return ctx.Err()
}

// Record a terminal failure only while the pending delivery snapshot is current.
// Retain its stream ID for operators, without duplicating the email or reset code.
var exhaustAdminCommand = redis.NewScript(`
local id=ARGV[2]
local p=redis.call('XPENDING',KEYS[1],ARGV[1],id,id,1)
if #p==0 or p[1][2]~=ARGV[3] or p[1][3]<tonumber(ARGV[4]) or p[1][4]~=tonumber(ARGV[5]) then return 0 end
local t=redis.call('TIME')
local now=t[1]*1000+math.floor(t[2]/1000)
local window=tonumber(ARGV[6])
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now-window)
redis.call('ZADD',KEYS[2],now,id)
redis.call('PEXPIRE',KEYS[2],window)
redis.call('XACK',KEYS[1],ARGV[1],id)
redis.call('XDEL',KEYS[1],id)
return 1`)

func (c *adminUserCommands) reclaim(ctx context.Context) {
	pending, err := c.client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: c.stream, Group: c.group, Start: "-", End: "+", Count: 100}).Result()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("list pending admin user commands: %v", err)
		}
		return
	}
	for _, p := range pending {
		if ctx.Err() != nil {
			return
		}
		if p.Idle < c.minIdle {
			continue
		}
		if p.RetryCount >= adminUserCommandMaxDeliveries {
			n, err := exhaustAdminCommand.Run(ctx, c.client, []string{c.stream, c.stream + ":exhausted"}, c.group, p.ID, p.Consumer, c.minIdle.Milliseconds(), p.RetryCount, adminCommandFailureWindow.Milliseconds()).Int64()
			if err != nil {
				log.Printf("record exhausted admin command: %v", err)
			} else if n > 0 {
				slog.Warn("admin user command exhausted", "id", p.ID, "deliveries", p.RetryCount)
			}
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

var finishAdminCommand = redis.NewScript(`
local id=ARGV[2]
local p=redis.call('XPENDING',KEYS[1],ARGV[1],id,id,1)
if #p==0 or p[1][2]~=ARGV[3] then return 0 end
redis.call('XACK',KEYS[1],ARGV[1],id)
return redis.call('XDEL',KEYS[1],id)`)

func (c *adminUserCommands) process(ctx context.Context, message redis.XMessage) {
	if ctx.Err() != nil {
		return
	}
	commandType, _ := message.Values["type"].(string)
	email, _ := message.Values["email"].(string)
	if commandType == "password_reset_email" && email != "" {
		deliverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adminUserCommandDeliveryTimeout)
		err := c.deliver(deliverCtx, email)
		cancel()
		if err != nil {
			slog.Warn("admin password reset email failed, will retry", "id", message.ID, "timeout", errors.Is(err, context.DeadlineExceeded))
			return
		}
	}
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := finishAdminCommand.Run(ackCtx, c.client, []string{c.stream}, c.group, message.ID, c.consumer).Err(); err != nil {
		slog.Warn("admin user command acknowledgement failed", "id", message.ID, "err", err)
	}
}

func isBusyGroup(err error) bool { return err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") }
