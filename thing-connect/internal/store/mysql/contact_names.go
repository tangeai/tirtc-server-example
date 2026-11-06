package mysql

import (
	"context"
	"github.com/jmoiron/sqlx"
)

// ContactNamesStore exposes names only through an owned device's accepted contacts.
type ContactNamesStore struct{ db *sqlx.DB }

func NewContactNamesStore(db *sqlx.DB) *ContactNamesStore { return &ContactNamesStore{db: db} }
func (s *ContactNamesStore) VisibleContactNames(ctx context.Context, user int64, device string) (map[string]string, error) {
	var rows []struct {
		ID   string `db:"device_id"`
		Name string `db:"device_name"`
	}
	err := s.db.SelectContext(ctx, &rows, `SELECT peer.device_id,peer.device_name FROM device_bind self JOIN call_contact c ON (c.device_id_a=self.device_id OR c.device_id_b=self.device_id) AND c.status=1 JOIN device_bind peer ON peer.device_id=IF(c.device_id_a=self.device_id,c.device_id_b,c.device_id_a) WHERE self.device_id=? AND self.user_id=?`, device, user)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}
