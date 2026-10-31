package mysql

import (
	"context"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/voip"
)

type voipCleanupStore struct{ db *sqlx.DB }

func NewVoipCleanupStore(db *sqlx.DB) voip.CleanupStore { return &voipCleanupStore{db: db} }

func (s *voipCleanupStore) Contacts(ctx context.Context, deviceID string) ([]voip.Contact, error) {
	var contacts []voip.Contact
	err := s.db.SelectContext(ctx, &contacts, `SELECT wx_app_id,wx_open_id FROM voip_device_auth WHERE device_id=?`, deviceID)
	return contacts, err
}

func (s *voipCleanupStore) DeleteAuthorizations(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM voip_device_auth WHERE device_id=?`, deviceID)
	return err
}
