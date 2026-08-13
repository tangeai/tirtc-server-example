package cleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
)

// Target is one idempotent service-to-service cleanup endpoint.
type Target struct {
	Name        string
	URL         string
	InternalKey string
}

// Outbox persists cleanup notifications so transient failures and process
// restarts cannot silently lose them.
type Outbox struct {
	db      *sqlx.DB
	client  *http.Client
	targets map[string]Target
}

func NewOutbox(db *sqlx.DB, targets []Target) *Outbox {
	m := make(map[string]Target, len(targets))
	for _, target := range targets {
		if target.Name != "" && target.URL != "" {
			m[target.Name] = target
		}
	}
	return &Outbox{db: db, client: &http.Client{Timeout: 5 * time.Second}, targets: m}
}

// TargetNames returns the configured, de-duplicated delivery targets. It is
// used when an unbind transaction writes its outbox records before dispatch.
func TargetNames(targets []Target) []string {
	seen := make(map[string]struct{}, len(targets))
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Name == "" || target.URL == "" {
			continue
		}
		if _, exists := seen[target.Name]; exists {
			continue
		}
		seen[target.Name] = struct{}{}
		names = append(names, target.Name)
	}
	return names
}

// Enqueue records one task per configured target. Re-enqueueing an existing
// task makes it immediately eligible for another attempt.
func (o *Outbox) Enqueue(ctx context.Context, deviceID string) error {
	tx, err := o.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cleanup outbox begin: %w", err)
	}
	defer tx.Rollback()
	for name := range o.targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cleanup_outbox (device_id, target, next_attempt_at)
			VALUES (?, ?, NOW())
			ON DUPLICATE KEY UPDATE next_attempt_at=NOW(), last_error=''`, deviceID, name); err != nil {
			return fmt.Errorf("cleanup outbox enqueue %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cleanup outbox commit: %w", err)
	}
	return nil
}

type task struct {
	ID       int64  `db:"id"`
	DeviceID string `db:"device_id"`
	Target   string `db:"target"`
	Attempts int    `db:"attempts"`
}

// Run delivers queued tasks until ctx is cancelled.
func (o *Outbox) Run(ctx context.Context) {
	o.process(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.process(ctx)
		}
	}
}

func (o *Outbox) process(ctx context.Context) {
	var tasks []task
	if err := o.db.SelectContext(ctx, &tasks, `
		SELECT id, device_id, target, attempts
		FROM cleanup_outbox
		WHERE next_attempt_at <= NOW()
		ORDER BY id LIMIT 20`); err != nil {
		if ctx.Err() == nil {
			slog.Warn("cleanup outbox query failed", "err", err)
		}
		return
	}
	for _, current := range tasks {
		if err := o.deliver(ctx, current); err != nil {
			delay := retryDelay(current.Attempts + 1)
			_, updateErr := o.db.ExecContext(ctx, `
				UPDATE cleanup_outbox
				SET attempts=attempts+1, last_error=?, next_attempt_at=DATE_ADD(NOW(), INTERVAL ? SECOND)
				WHERE id=?`, truncateError(err), int(delay.Seconds()), current.ID)
			if updateErr != nil && ctx.Err() == nil {
				slog.Warn("cleanup outbox retry update failed", "id", current.ID, "err", updateErr)
			}
			continue
		}
		if _, err := o.db.ExecContext(ctx, `DELETE FROM cleanup_outbox WHERE id=?`, current.ID); err != nil && ctx.Err() == nil {
			slog.Warn("cleanup outbox delete failed", "id", current.ID, "err", err)
		}
	}
}

func (o *Outbox) deliver(ctx context.Context, current task) error {
	target, ok := o.targets[current.Target]
	if !ok {
		return fmt.Errorf("unknown cleanup target %q", current.Target)
	}
	body, _ := json.Marshal(map[string]string{"device_id": current.DeviceID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", target.InternalKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", current.Target, resp.StatusCode)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<attempt) * 5 * time.Second
}

func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > 1000 {
		return msg[:1000]
	}
	return msg
}
