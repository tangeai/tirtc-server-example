package voip

import (
	"context"
	"fmt"
	"time"
)

type Contact struct {
	AppID  string `db:"wx_app_id"`
	OpenID string `db:"wx_open_id"`
}

type CleanupStore interface {
	Contacts(context.Context, string) ([]Contact, error)
	DeleteAuthorizations(context.Context, string) error
}

type CallGuards interface {
	ReleaseDevice(context.Context, string, []Contact) error
}

type AuthNotifier interface {
	AuthChanged(context.Context, string) error
}

// Cleanup consumes the durable binding-cleanup outbox. Every failure is returned
// to its caller so the outbox retains the work for retry after process restart.
type Cleanup struct {
	store    CleanupStore
	guards   CallGuards
	notifier AuthNotifier
}

func NewCleanup(store CleanupStore, guards CallGuards, notifier AuthNotifier) *Cleanup {
	return &Cleanup{store: store, guards: guards, notifier: notifier}
}

func (s *Cleanup) Unbind(ctx context.Context, deviceID string) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	contacts, err := s.store.Contacts(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("read VoIP cleanup contacts: %w", err)
	}
	// Keep contacts durable until their guards are released; deleting first loses
	// the contact keys when Redis fails and the request is retried.
	if err := s.guards.ReleaseDevice(ctx, deviceID, contacts); err != nil {
		return fmt.Errorf("release VoIP cleanup guards: %w", err)
	}
	if err := s.store.DeleteAuthorizations(ctx, deviceID); err != nil {
		return fmt.Errorf("delete VoIP authorizations: %w", err)
	}
	if err := s.notifier.AuthChanged(ctx, deviceID); err != nil {
		return fmt.Errorf("notify VoIP cleanup: %w", err)
	}
	return nil
}
