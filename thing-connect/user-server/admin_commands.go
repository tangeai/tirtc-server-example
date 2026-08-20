package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/service"
)

const (
	adminUserCommandStream = "thingconnect:admin:user-commands"
	adminUserCommandGroup  = "user-server"
)

func runAdminUserCommands(ctx context.Context, client *redis.Client, users *service.UserService) {
	if err := client.XGroupCreateMkStream(ctx, adminUserCommandStream, adminUserCommandGroup, "0").Err(); err != nil && !isBusyGroup(err) {
		log.Printf("admin command group: %v", err)
		return
	}
	host, _ := os.Hostname()
	consumer := host + "-" + strconv.Itoa(os.Getpid())
	claimStart := "0-0"
	for {
		claimed, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: adminUserCommandStream, Group: adminUserCommandGroup, Consumer: consumer, MinIdle: 30 * time.Second, Start: claimStart, Count: 20}).Result()
		if err == nil {
			for _, message := range claimed {
				processAdminUserCommand(ctx, client, users, message)
			}
			claimStart = next
		}
		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: adminUserCommandGroup, Consumer: consumer, Streams: []string{adminUserCommandStream, ">"}, Count: 20, Block: 5 * time.Second}).Result()
		if errors.Is(err, context.Canceled) {
			return
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			log.Printf("read admin user commands: %v", err)
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				processAdminUserCommand(ctx, client, users, message)
			}
		}
	}
}

func processAdminUserCommand(ctx context.Context, client *redis.Client, users *service.UserService, message redis.XMessage) {
	commandType, _ := message.Values["type"].(string)
	email, _ := message.Values["email"].(string)
	if commandType != "password_reset_email" || email == "" {
		_ = client.XAck(ctx, adminUserCommandStream, adminUserCommandGroup, message.ID).Err()
		return
	}
	if err := users.DeliverPasswordResetCode(ctx, email); err != nil {
		log.Printf("admin password reset email: %v", err)
		return
	}
	_ = client.XAck(ctx, adminUserCommandStream, adminUserCommandGroup, message.ID).Err()
}

func isBusyGroup(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}
