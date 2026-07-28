package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
)

const (
	contactStatusPending  = 0
	contactStatusAccepted = 1
	contactStatusRejected = 2
	contactStatusDeleted  = 3
	maxContactRemarkChars = 64
)

var (
	errContactNotExist  = errors.New("联系人不存在")
	errContactDuplicate = errors.New("联系人已存在")
	errContactPending   = errors.New("联系人请求正在处理中")
	errContactProtected = errors.New("同一账号自动建立的联系人不能删除")
	errContactMax       = errors.New("联系人数量已达上限")
)

func validContactRemark(remark string) bool {
	return utf8.RuneCountInString(remark) <= maxContactRemarkChars
}

type deviceContact struct {
	ID        int64     `db:"id"`
	DeviceIDA string    `db:"device_id_a"`
	DeviceIDB string    `db:"device_id_b"`
	Source    string    `db:"source"`
	Initiator string    `db:"initiator"`
	UserIDA   int64     `db:"user_id_a"`
	UserIDB   int64     `db:"user_id_b"`
	Status    int8      `db:"status"`
	RemarkA   string    `db:"remark_a"`
	RemarkB   string    `db:"remark_b"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// normalizePair returns (idA, idB) in lexicographic order, matching the
// device_id_a < device_id_b convention (design doc §8.1).
func normalizePair(x, y string) (idA, idB string) {
	if x < y {
		return x, y
	}
	return y, x
}

// peerRemark returns the remark this contact row stores for the "self" side's
// view of the peer — i.e. remark_a is self's note about b when self==a, and
// vice versa. This is the opposite of what §6.1 calls "被叫对主叫的备注": from
// target's perspective, its remark about the caller.
func (c *deviceContact) remarkFor(self string) string {
	if self == c.DeviceIDA {
		return c.RemarkA
	}
	return c.RemarkB
}

func (c *deviceContact) peer(self string) string {
	if self == c.DeviceIDA {
		return c.DeviceIDB
	}
	return c.DeviceIDA
}

func (s *Server) getContactRow(ctx context.Context, a, b string) (*deviceContact, error) {
	idA, idB := normalizePair(a, b)
	var row deviceContact
	err := s.db.GetContext(ctx, &row,
		`SELECT * FROM call_contact WHERE device_id_a=? AND device_id_b=? LIMIT 1`, idA, idB)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getContactRow: %w", err)
	}
	return &row, nil
}

// ensureContact returns the contact row for (a,b), lazily materializing an
// accepted "auto" row if both devices are bound to the same account and no
// row exists yet. There's no bind/unbind event hook wired into call-server,
// so same-account contacts are created on first lookup instead of eagerly at
// bind time (see plan §联系人管理).
func (s *Server) ensureContact(ctx context.Context, a, b string) (*deviceContact, error) {
	row, err := s.getContactRow(ctx, a, b)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return row, nil
	}

	bindA, err := s.dev.GetBindByDeviceID(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("ensureContact: GetBindByDeviceID(%s): %w", a, err)
	}
	bindB, err := s.dev.GetBindByDeviceID(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("ensureContact: GetBindByDeviceID(%s): %w", b, err)
	}
	if bindA == nil || bindB == nil || bindA.UserID == 0 || bindB.UserID == 0 || bindA.UserID != bindB.UserID {
		return nil, nil // not contacts and not same-account
	}

	idA, idB := normalizePair(a, b)
	userIDA, userIDB := bindA.UserID, bindB.UserID
	if idA != a {
		userIDA, userIDB = bindB.UserID, bindA.UserID
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO call_contact (device_id_a, device_id_b, source, initiator, user_id_a, user_id_b, status)
		 VALUES (?, ?, 'auto', 'a', ?, ?, ?)
		 ON DUPLICATE KEY UPDATE updated_at = updated_at`,
		idA, idB, userIDA, userIDB, contactStatusAccepted); err != nil {
		return nil, fmt.Errorf("ensureContact: insert: %w", err)
	}
	return s.getContactRow(ctx, a, b)
}

// upsertAutoContact explicitly adds or restores a same-account contact. List
// reads use ensureContact and intentionally do not resurrect a row the user
// deleted; an explicit contact request is what restores that relationship.
func (s *Server) upsertAutoContact(ctx context.Context, a, b string, userA, userB int64) error {
	idA, idB := normalizePair(a, b)
	userIDA, userIDB := userA, userB
	if idA != a {
		userIDA, userIDB = userB, userA
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO call_contact (device_id_a, device_id_b, source, initiator, user_id_a, user_id_b, status)
		 VALUES (?, ?, 'auto', 'a', ?, ?, ?)
		 ON DUPLICATE KEY UPDATE source='auto', initiator='a', user_id_a=?, user_id_b=?, status=?`,
		idA, idB, userIDA, userIDB, contactStatusAccepted,
		userIDA, userIDB, contactStatusAccepted); err != nil {
		return fmt.Errorf("upsertAutoContact: %w", err)
	}
	return nil
}

// listPendingContacts returns pending requests whose non-initiating side is
// one of targetDeviceIDs. It is shared by the device and H5 APIs so both
// surfaces expose the same responder identity and authorization semantics.
func (s *Server) listPendingContacts(ctx context.Context, targetDeviceIDs []string) ([]deviceContact, error) {
	if len(targetDeviceIDs) == 0 {
		return []deviceContact{}, nil
	}
	query, args, err := sqlx.In(
		`SELECT * FROM call_contact WHERE status=? AND (device_id_a IN (?) OR device_id_b IN (?))`,
		contactStatusPending, targetDeviceIDs, targetDeviceIDs)
	if err != nil {
		return nil, fmt.Errorf("listPendingContacts query: %w", err)
	}
	query = s.db.Rebind(query)
	var candidates []deviceContact
	if err := s.db.SelectContext(ctx, &candidates, query, args...); err != nil {
		return nil, fmt.Errorf("listPendingContacts: %w", err)
	}

	targets := make(map[string]bool, len(targetDeviceIDs))
	for _, id := range targetDeviceIDs {
		targets[id] = true
	}
	rows := make([]deviceContact, 0, len(candidates))
	for _, row := range candidates {
		responder := row.DeviceIDB
		if row.Initiator == "b" {
			responder = row.DeviceIDA
		}
		if targets[responder] {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *Server) isAcceptedContact(ctx context.Context, a, b string) (bool, error) {
	row, err := s.ensureContact(ctx, a, b)
	if err != nil {
		return false, err
	}
	return row != nil && row.Status == contactStatusAccepted, nil
}

// countAcceptedContacts counts deviceID's accepted contacts, for the
// max_contacts_per_device cap.
func (s *Server) countAcceptedContacts(ctx context.Context, deviceID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM call_contact WHERE (device_id_a=? OR device_id_b=?) AND status=?`,
		deviceID, deviceID, contactStatusAccepted)
	if err != nil {
		return 0, fmt.Errorf("countAcceptedContacts: %w", err)
	}
	return n, nil
}

// listContacts returns deviceID's accepted contacts (peer id + deviceID's own
// remark for that peer), including same-account contacts materialized on the
// fly via ensureContact.
func (s *Server) listContacts(ctx context.Context, deviceID string) ([]deviceContact, error) {
	// Materialize same-account contacts for every sibling device under this
	// account before listing, so they show up even if never explicitly requested.
	bind, err := s.dev.GetBindByDeviceID(ctx, deviceID)
	if err == nil && bind != nil && bind.UserID != 0 {
		var siblings []string
		if err := s.db.SelectContext(ctx, &siblings,
			`SELECT device_id FROM device_bind WHERE user_id=? AND device_id<>?`, bind.UserID, deviceID); err == nil {
			for _, sib := range siblings {
				_, _ = s.ensureContact(ctx, deviceID, sib)
			}
		}
	}

	var rows []deviceContact
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT * FROM call_contact WHERE (device_id_a=? OR device_id_b=?) AND status=?`,
		deviceID, deviceID, contactStatusAccepted); err != nil {
		return nil, fmt.Errorf("listContacts: %w", err)
	}
	return rows, nil
}

// createRequest creates or renews a manual contact request from initiator to
// target. Same-account pairs are rejected here — those are auto-linked lazily
// by ensureContact/listContacts instead of going through the request flow.
func (s *Server) createRequest(ctx context.Context, initiator, target string, initiatorUserID, targetUserID int64) (*deviceContact, error) {
	existing, err := s.getContactRow(ctx, initiator, target)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		switch existing.Status {
		case contactStatusAccepted:
			return nil, errContactDuplicate
		case contactStatusPending:
			return nil, errContactPending
		}
		// rejected/deleted — fall through to re-request (reset to pending).
	}

	idA, idB := normalizePair(initiator, target)
	initiatorSide := "a"
	userIDA, userIDB := initiatorUserID, targetUserID
	if idA != initiator {
		initiatorSide = "b"
		userIDA, userIDB = targetUserID, initiatorUserID
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO call_contact (device_id_a, device_id_b, source, initiator, user_id_a, user_id_b, status)
		 VALUES (?, ?, 'manual', ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE source='manual', initiator=?, user_id_a=?, user_id_b=?, status=?, remark_a='', remark_b=''`,
		idA, idB, initiatorSide, userIDA, userIDB, contactStatusPending,
		initiatorSide, userIDA, userIDB, contactStatusPending); err != nil {
		return nil, fmt.Errorf("createRequest: %w", err)
	}
	return s.getContactRow(ctx, initiator, target)
}

// respondRequest accepts or rejects a pending request where responder is the
// non-initiating side.
func (s *Server) respondRequest(ctx context.Context, responder, peer string, accept bool) (*deviceContact, error) {
	idA, idB := normalizePair(responder, peer)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("respondRequest begin: %w", err)
	}
	defer tx.Rollback()

	// Serialize accepts involving either device. Without this lock, multiple
	// pending requests can be accepted concurrently and bypass the limit.
	var locked []string
	if err := tx.SelectContext(ctx, &locked,
		`SELECT device_id FROM device_bind
		 WHERE device_id IN (?, ?) ORDER BY device_id FOR UPDATE`, idA, idB); err != nil {
		return nil, fmt.Errorf("respondRequest lock devices: %w", err)
	}

	var row deviceContact
	err = tx.GetContext(ctx, &row,
		`SELECT * FROM call_contact
		 WHERE device_id_a=? AND device_id_b=? LIMIT 1 FOR UPDATE`, idA, idB)
	if err == sql.ErrNoRows {
		return nil, errContactNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("respondRequest get contact: %w", err)
	}
	if row.Status != contactStatusPending {
		return nil, errContactNotExist
	}
	initiatorIsResponder := (row.Initiator == "a" && row.DeviceIDA == responder) || (row.Initiator == "b" && row.DeviceIDB == responder)
	if initiatorIsResponder {
		return nil, errContactNotExist // the initiator can't respond to their own request
	}
	newStatus := contactStatusRejected
	if accept {
		if len(locked) != 2 {
			return nil, errContactNotExist
		}
		for _, deviceID := range []string{responder, peer} {
			var n int
			if err := tx.GetContext(ctx, &n,
				`SELECT COUNT(*) FROM call_contact
				 WHERE (device_id_a=? OR device_id_b=?) AND status=?`,
				deviceID, deviceID, contactStatusAccepted); err != nil {
				return nil, fmt.Errorf("respondRequest count contacts: %w", err)
			}
			if n >= s.cfg.Service.MaxContactsPerDevice {
				return nil, errContactMax
			}
		}
		newStatus = contactStatusAccepted
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE call_contact SET status=? WHERE id=?`, newStatus, row.ID); err != nil {
		return nil, fmt.Errorf("respondRequest: %w", err)
	}
	row.Status = int8(newStatus)
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("respondRequest commit: %w", err)
	}
	return &row, nil
}

func (s *Server) setRemark(ctx context.Context, self, peer, remark string) error {
	idA, _ := normalizePair(self, peer)
	col := "remark_b"
	if idA == self {
		col = "remark_a"
	}
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE call_contact SET %s=? WHERE device_id_a=? AND device_id_b=? AND status=?`, col),
		remark, minStr(self, peer), maxStr(self, peer), contactStatusAccepted)
	if err != nil {
		return fmt.Errorf("setRemark: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists bool
		if err := s.db.GetContext(ctx, &exists,
			`SELECT EXISTS(
			   SELECT 1 FROM call_contact
			   WHERE device_id_a=? AND device_id_b=? AND status=?
			 )`, minStr(self, peer), maxStr(self, peer), contactStatusAccepted); err != nil {
			return fmt.Errorf("setRemark verify: %w", err)
		}
		if !exists {
			return errContactNotExist
		}
	}
	return nil
}

func (s *Server) deleteContact(ctx context.Context, self, peer string) error {
	row, err := s.getContactRow(ctx, self, peer)
	if err != nil {
		return err
	}
	if row == nil || row.Status != contactStatusAccepted {
		return errContactNotExist
	}
	if row.Source == "auto" {
		return errContactProtected
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE call_contact SET status=? WHERE id=? AND status=?`,
		contactStatusDeleted, row.ID, contactStatusAccepted)
	if err != nil {
		return fmt.Errorf("deleteContact: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errContactNotExist
	}
	return nil
}

// contactPeers returns every device that has a non-deleted contact
// relationship with deviceID — used to fan out callers_update on unbind.
func (s *Server) contactPeers(ctx context.Context, deviceID string) ([]string, error) {
	var rows []deviceContact
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT * FROM call_contact WHERE (device_id_a=? OR device_id_b=?) AND status<>?`,
		deviceID, deviceID, contactStatusDeleted); err != nil {
		return nil, fmt.Errorf("contactPeers: %w", err)
	}
	peers := make([]string, 0, len(rows))
	for _, row := range rows {
		peers = append(peers, row.peer(deviceID))
	}
	return peers, nil
}

// deleteAllContacts permanently removes every contact row touching deviceID.
// This is reserved for device unbind; the normal contact-delete API keeps its
// soft-delete behavior so a manually removed relationship can be re-added.
func (s *Server) deleteAllContacts(ctx context.Context, deviceID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM call_contact WHERE device_id_a=? OR device_id_b=?`,
		deviceID, deviceID); err != nil {
		return fmt.Errorf("deleteAllContacts: %w", err)
	}
	return nil
}

// voipContact represents a WeChat mini-program user authorized to call a device.
type voipContact struct {
	ID        int64  `db:"id"`
	WxOpenID  string `db:"wx_open_id"`
	WxAppID   string `db:"wx_app_id"`
	WxModelID string `db:"wx_model_id"`
	Remark    string `db:"remark"`
}

// listVoipContacts returns all VoIP-authorized callers for deviceID.
func (s *Server) listVoipContacts(ctx context.Context, deviceID string) ([]voipContact, error) {
	var rows []voipContact
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT auth.id, auth.wx_open_id, auth.wx_app_id, auth.wx_model_id,
		        COALESCE(profile.remark, auth.remark) AS remark
		   FROM voip_device_auth auth
		   LEFT JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
			  WHERE auth.device_id=? AND auth.auth_status='active'
		  ORDER BY auth.created_at DESC`,
		deviceID); err != nil {
		return nil, fmt.Errorf("listVoipContacts: %w", err)
	}
	return rows, nil
}

// setVoipRemark changes the contact name of one mini-program user globally.
// Every authorization for the same (wx_open_id, wx_app_id) receives the same
// value, regardless of which device/H5 entry performed the last write.
func (s *Server) setVoipRemark(ctx context.Context, id int64, remark string) ([]string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("setVoipRemark begin: %w", err)
	}
	defer tx.Rollback()

	var identity struct {
		WxOpenID string `db:"wx_open_id"`
		WxAppID  string `db:"wx_app_id"`
	}
	if err := tx.GetContext(ctx, &identity,
		`SELECT wx_open_id, wx_app_id FROM voip_device_auth WHERE id=? FOR UPDATE`, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, errContactNotExist
		}
		return nil, fmt.Errorf("setVoipRemark identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO voip_user_profile (wx_open_id, wx_app_id, remark)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE remark=VALUES(remark)`,
		identity.WxOpenID, identity.WxAppID, remark); err != nil {
		return nil, fmt.Errorf("setVoipRemark profile: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE voip_device_auth SET remark=? WHERE wx_open_id=? AND wx_app_id=?`,
		remark, identity.WxOpenID, identity.WxAppID); err != nil {
		return nil, fmt.Errorf("setVoipRemark authorizations: %w", err)
	}

	var deviceIDs []string
	if err := tx.SelectContext(ctx, &deviceIDs,
		`SELECT DISTINCT device_id FROM voip_device_auth WHERE wx_open_id=? AND wx_app_id=?`,
		identity.WxOpenID, identity.WxAppID); err != nil {
		return nil, fmt.Errorf("setVoipRemark devices: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("setVoipRemark commit: %w", err)
	}
	return deviceIDs, nil
}

func minStr(a, b string) string {
	x, _ := normalizePair(a, b)
	return x
}

func maxStr(a, b string) string {
	_, y := normalizePair(a, b)
	return y
}
