// Package room owns persistent multi-device intercom assignments and leases.
package room

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"
)

var (
	ErrInvalid     = errors.New("房间号须为六位数字，密码须为空或四位数字")
	ErrForbidden   = errors.New("设备不属于当前账号，请刷新设备列表")
	ErrNotFound    = errors.New("房间不存在或已解散")
	ErrPassword    = errors.New("房间密码错误，请重新输入")
	ErrLocked      = errors.New("密码连续错误，请十分钟后重试")
	ErrLimited     = errors.New("操作过于频繁，请稍后重试")
	ErrFull        = errors.New("房间已满，请稍后重试")
	ErrStale       = errors.New("房间状态已变化，请重新同步")
	ErrConflict    = errors.New("请求标识已用于其他操作，请重新提交")
	ErrUnavailable = errors.New("对讲连接服务暂不可用，请稍后重试")
	ErrCodeTaken   = errors.New("room code occupied")
)

type Policy struct {
	EmptyTTL         time.Duration
	CodeCooldown     time.Duration
	Lease            time.Duration
	Heartbeat        time.Duration
	CommandTTL       time.Duration
	TokenTimeout     time.Duration
	ParticipantLimit int
}

func DefaultPolicy() Policy {
	return Policy{24 * time.Hour, 24 * time.Hour, 45 * time.Second, 15 * time.Second, 20 * time.Second, 5 * time.Second, 100}
}
func (p Policy) Validate() error {
	if p.EmptyTTL <= 0 || p.CodeCooldown <= 0 || p.Lease < 3*time.Second || p.Heartbeat < time.Second || p.Heartbeat*2 >= p.Lease || p.CommandTTL <= 0 || p.TokenTimeout <= 0 || p.TokenTimeout >= p.Lease || p.ParticipantLimit < 1 || p.ParticipantLimit > 100 {
		return errors.New("invalid room policy")
	}
	return nil
}

type Assignment struct {
	DeviceID    string `json:"device_id"`
	Owner       int64  `json:"-"`
	RoomID      string `json:"room_id"`
	RoomCode    string `json:"room_code"`
	Desired     string `json:"desired_state"`
	Version     int64  `json:"assignment_version"`
	State       string `json:"state"`
	SessionID   string `json:"-"`
	PasswordSet bool   `json:"password_set"`
	OnlineCount int    `json:"online_count"`
	Online      bool   `json:"online"`
}
type Room struct {
	ID            string
	Code          string
	Owner         int64
	Verifier      []byte
	Status        string
	EmptyDeadline *time.Time
	ReusableAt    *time.Time
	ClosedAt      *time.Time
	CreatedAt     time.Time
	Limit         int
}
type Lease struct {
	DeviceID, RoomID, SessionID, State string
	Version                            int64
	Expires                            time.Time
}
type Operation struct {
	DeviceID string `json:"-"`
	UserID   int64  `json:"-"`
	Key      string `json:"-"`
	Kind     string `json:"-"`
	IP       string `json:"-"`
	Code     string `json:"room_code"`
	Password string `json:"password"`
	RoomID   string `json:"room_id"`
	Version  int64  `json:"assignment_version"`
}
type Presence struct {
	RoomID    string `json:"room_id"`
	SessionID string `json:"session_id"`
	Version   int64  `json:"assignment_version"`
	State     string `json:"state"`
}
type Credential struct {
	PeerID           string `json:"peer_id"`
	Token            string `json:"token"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	HeartbeatSeconds int64  `json:"heartbeat_seconds"`
	LeaseSeconds     int64  `json:"lease_seconds"`
}
type Event struct {
	ID, DeviceID string
	Version      int64
	Attempts     int
}

// Tx runs with device ownership/assignment and selected room rows locked. No
// method may perform network I/O. Business errors roll back unless returned by
// the outer callback after committing a password failure counter.
type Tx interface {
	Owner(string) (int64, error)
	Assignment(string) (Assignment, error)
	SaveAssignment(Assignment) error
	Room(string, bool) (Room, error)
	CreateRoom(Room) error
	SaveRoom(Room) error
	Leases(string) ([]Lease, error)
	Lease(string) (Lease, error)
	SaveLease(Lease) error
	PasswordFailures(string) (int, time.Time, error)
	SetPasswordFailures(string, int, time.Time) error
	Replay(string) (string, Assignment, bool, error)
	SaveReplay(string, string, Assignment) error
	Notify(Event) error
	CloseAssignments(string) error
}
type Repository interface {
	Transaction(context.Context, func(Tx) error) error
	DueRooms(context.Context, int) ([]string, error)
	Events(context.Context, int) ([]Event, error)
	CompleteEvent(context.Context, Event, bool) error
}
type TokenIssuer interface {
	Issue(context.Context, string, string) (Credential, error)
}
type Signals interface {
	Allow(context.Context, Operation) (bool, error)
	Online(context.Context, string) bool
	Notify(context.Context, Event, time.Duration) error
}
type Service struct {
	repo    Repository
	issuer  TokenIssuer
	signals Signals
	pepper  []byte
	policy  atomic.Pointer[Policy]
	now     func() time.Time
}

func New(repo Repository, issuer TokenIssuer, signals Signals, pepper []byte, policy Policy) (*Service, error) {
	if len(pepper) < 32 {
		return nil, errors.New("room pepper must contain at least 32 bytes")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	s := &Service{repo: repo, issuer: issuer, signals: signals, pepper: append([]byte(nil), pepper...), now: time.Now}
	s.policy.Store(&policy)
	return s, nil
}
func (s *Service) UpdatePolicy(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s.policy.Store(&p)
	return nil
}
func Digits(v string, n int) bool {
	if len(v) != n {
		return false
	}
	for i := range v {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) verifier(id, password string) []byte {
	if password == "" {
		return nil
	}
	h := hmac.New(sha256.New, s.pepper)
	h.Write([]byte(id + "\x00" + password))
	return h.Sum(nil)
}
func replayKey(o Operation) string {
	return fmt.Sprintf("%d:%s:%s:%s", o.UserID, o.DeviceID, o.Kind, o.Key)
}
func (s *Service) fingerprint(o Operation) string {
	h := hmac.New(sha256.New, s.pepper)
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d", o.Code, o.Password, o.RoomID, o.Version) // hash.Hash writes never fail.
	return hex.EncodeToString(h.Sum(nil))
}
func authorize(tx Tx, device string, user int64) (int64, error) {
	owner, err := tx.Owner(device)
	if err != nil {
		return 0, err
	}
	if owner == 0 || (user != 0 && owner != user) {
		return 0, ErrForbidden
	}
	return owner, nil
}

func (s *Service) Change(ctx context.Context, o Operation) (Assignment, error) {
	if o.DeviceID == "" || len(o.Key) < 8 || len(o.Key) > 64 || (o.Password != "" && !Digits(o.Password, 4)) || (o.Kind == "join" && !Digits(o.Code, 6)) || (o.Kind != "create" && o.Kind != "join" && o.Kind != "leave") {
		return Assignment{}, ErrInvalid
	}
	allowed, err := s.signals.Allow(ctx, o)
	if err != nil {
		return Assignment{}, err
	}
	if !allowed {
		return Assignment{}, ErrLimited
	}
	var result Assignment
	for attempt := 0; attempt < 20; attempt++ {
		var committedError error
		err = s.repo.Transaction(ctx, func(tx Tx) error {
			owner, e := authorize(tx, o.DeviceID, o.UserID)
			if e != nil {
				return e
			}
			a, e := tx.Assignment(o.DeviceID)
			if e != nil {
				return e
			}
			fingerprint, prior, found, e := tx.Replay(replayKey(o))
			if e != nil {
				return e
			}
			if found {
				if fingerprint != s.fingerprint(o) {
					return ErrConflict
				}
				result = prior
				return nil
			}
			now := s.now()
			p := s.policy.Load()
			var target Room
			switch o.Kind {
			case "create":
				id, e := randomID()
				if e != nil {
					return e
				}
				number, e := rand.Int(rand.Reader, big.NewInt(1000000))
				if e != nil {
					return e
				}
				deadline := now.Add(p.EmptyTTL)
				target = Room{ID: "xiaotai_room_" + id, Code: fmt.Sprintf("%06d", number.Int64()), Owner: owner, Status: "waiting_join", EmptyDeadline: &deadline, CreatedAt: now, Limit: p.ParticipantLimit}
				target.Verifier = s.verifier(target.ID, o.Password)
				if e = tx.CreateRoom(target); e != nil {
					return e
				}
			case "join":
				failures, locked, e := tx.PasswordFailures(o.DeviceID)
				if e != nil {
					return e
				}
				if locked.After(now) {
					return ErrLocked
				}
				if !locked.IsZero() {
					failures = 0
				}
				target, e = tx.Room(o.Code, true)
				if e != nil {
					return e
				}
				if e = s.refresh(tx, &target, now); e != nil {
					return e
				}
				if target.Status == "closed" {
					return ErrNotFound
				}
				if !hmac.Equal(target.Verifier, s.verifier(target.ID, o.Password)) {
					failures++
					until := time.Time{}
					committedError = ErrPassword
					if failures >= 5 {
						until = now.Add(10 * time.Minute)
						committedError = ErrLocked
					}
					return tx.SetPasswordFailures(o.DeviceID, failures, until)
				}
				if e = tx.SetPasswordFailures(o.DeviceID, 0, time.Time{}); e != nil {
					return e
				}
				leases, e := tx.Leases(target.ID)
				if e != nil {
					return e
				}
				count := 0
				for _, l := range leases {
					if l.DeviceID != o.DeviceID && l.State != "ended" && l.Expires.After(now) {
						count++
					}
				}
				if count >= target.Limit {
					return ErrFull
				}
			case "leave":
				if (o.RoomID != "" && o.RoomID != a.RoomID) || (o.Version != 0 && o.Version != a.Version) {
					return ErrStale
				}
			}
			if a.RoomID != "" {
				old, e := tx.Room(a.RoomID, false)
				if e != nil {
					return e
				}
				l, e := tx.Lease(o.DeviceID)
				if e != nil {
					return e
				}
				if l.RoomID == old.ID && l.State != "ended" {
					l.State = "ended"
					l.Expires = now
					if e = tx.SaveLease(l); e != nil {
						return e
					}
				}
				if e = s.refresh(tx, &old, now); e != nil {
					return e
				}
			}
			a.DeviceID = o.DeviceID
			a.Owner = owner
			a.Version++
			a.SessionID = ""
			a.OnlineCount = 0
			a.PasswordSet = false
			a.RoomID = ""
			a.RoomCode = ""
			a.Desired = "left"
			a.State = "left"
			if o.Kind != "leave" {
				a.RoomID = target.ID
				a.RoomCode = target.Code
				a.Desired = "joined"
				a.State = "assigned"
				a.PasswordSet = len(target.Verifier) > 0
			}
			if e = tx.SaveAssignment(a); e != nil {
				return e
			}
			id, e := randomID()
			if e != nil {
				return e
			}
			if e = tx.Notify(Event{ID: id, DeviceID: o.DeviceID, Version: a.Version}); e != nil {
				return e
			}
			result = a
			return tx.SaveReplay(replayKey(o), s.fingerprint(o), a)
		})
		if errors.Is(err, ErrCodeTaken) {
			continue
		}
		if err == nil {
			err = committedError
		}
		return result, err
	}
	return Assignment{}, ErrUnavailable
}

// refresh computes empty time from the last lease end, including after restart.
func (s *Service) refresh(tx Tx, r *Room, now time.Time) error {
	if r.Status == "closed" {
		return nil
	}
	leases, err := tx.Leases(r.ID)
	if err != nil {
		return err
	}
	latest := r.CreatedAt
	active := false
	for _, l := range leases {
		if l.Expires.After(latest) {
			latest = l.Expires
		}
		if l.State != "ended" && l.Expires.After(now) {
			active = true
		}
	}
	if active {
		r.Status = "active"
		r.EmptyDeadline = nil
	} else if r.EmptyDeadline == nil {
		deadline := latest.Add(s.policy.Load().EmptyTTL)
		r.EmptyDeadline = &deadline
		r.Status = "empty_grace"
	}
	if !active && r.EmptyDeadline != nil && !r.EmptyDeadline.After(now) {
		r.Status = "closed"
		r.ClosedAt = &now
		reuse := now.Add(s.policy.Load().CodeCooldown)
		r.ReusableAt = &reuse
		if err = tx.CloseAssignments(r.ID); err != nil {
			return err
		}
	}
	return tx.SaveRoom(*r)
}
func (s *Service) Current(ctx context.Context, device string, user int64) (Assignment, error) {
	var a Assignment
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		owner, e := authorize(tx, device, user)
		if e != nil {
			return e
		}
		a, e = tx.Assignment(device)
		if e != nil {
			return e
		}
		if a.Owner != 0 && a.Owner != owner {
			return ErrForbidden
		}
		if a.Desired != "joined" {
			return nil
		}
		r, e := tx.Room(a.RoomID, false)
		if e != nil {
			return e
		}
		if e = s.refresh(tx, &r, s.now()); e != nil {
			return e
		}
		if r.Status == "closed" {
			a.Desired = "left"
			a.State = "room_closed"
			a.Version++
			return nil
		}
		a.RoomCode = r.Code
		a.PasswordSet = len(r.Verifier) > 0
		leases, e := tx.Leases(r.ID)
		if e != nil {
			return e
		}
		for _, l := range leases {
			if l.State == "joined" && l.Expires.After(s.now()) {
				a.OnlineCount++
			}
		}
		l, e := tx.Lease(device)
		if e != nil {
			return e
		}
		if a.State == "joined" && (!l.Expires.After(s.now()) || l.State == "ended") {
			a.State = "connect_failed"
		}
		return nil
	})
	if err == nil {
		a.Online = s.signals.Online(ctx, device)
		if a.Desired == "joined" && !a.Online {
			a.State = "waiting_device"
		}
	}
	return a, err
}
func matches(a Assignment, p Presence) bool {
	return a.Desired == "joined" && a.RoomID == p.RoomID && a.Version == p.Version
}
func validSession(p Presence) bool {
	return p.RoomID != "" && len(p.RoomID) <= 64 && len(p.SessionID) >= 8 && len(p.SessionID) <= 64 && p.Version > 0
}
func (s *Service) Connect(ctx context.Context, device string, p Presence) (Credential, error) {
	if !validSession(p) {
		return Credential{}, ErrInvalid
	}
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		owner, e := authorize(tx, device, 0)
		if e != nil {
			return e
		}
		a, e := tx.Assignment(device)
		if e != nil {
			return e
		}
		if !matches(a, p) || a.Owner != owner {
			return ErrStale
		}
		r, e := tx.Room(p.RoomID, false)
		if e != nil {
			return e
		}
		now := s.now()
		if e = s.refresh(tx, &r, now); e != nil {
			return e
		}
		if r.Status == "closed" {
			return ErrNotFound
		}
		l, e := tx.Lease(device)
		if e != nil {
			return e
		}
		if l.State != "ended" && l.Expires.After(now) && l.SessionID != p.SessionID {
			return ErrStale
		}
		// A session ID is single-use after its lease ends; delayed token results
		// cannot resurrect a retired generation.
		if l.SessionID == p.SessionID && (!l.Expires.After(now) || l.State == "ended") {
			return ErrStale
		}
		leases, e := tx.Leases(r.ID)
		if e != nil {
			return e
		}
		count := 0
		for _, other := range leases {
			if other.DeviceID != device && other.State != "ended" && other.Expires.After(now) {
				count++
			}
		}
		if count >= r.Limit {
			return ErrFull
		}
		l = Lease{DeviceID: device, RoomID: r.ID, SessionID: p.SessionID, Version: p.Version, State: "connecting", Expires: now.Add(s.policy.Load().Lease)}
		if e = tx.SaveLease(l); e != nil {
			return e
		}
		a.SessionID = p.SessionID
		a.State = "connecting"
		if e = tx.SaveAssignment(a); e != nil {
			return e
		}
		r.Status = "active"
		r.EmptyDeadline = nil
		return tx.SaveRoom(r)
	})
	if err != nil {
		return Credential{}, err
	}
	tokenCtx, cancel := context.WithTimeout(ctx, s.policy.Load().TokenTimeout)
	defer cancel()
	credential, err := s.issuer.Issue(tokenCtx, p.RoomID, device)
	if err != nil {
		p.State = "connect_failed"
		cleanupCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = s.Report(cleanupCtx, device, p)
		return Credential{}, ErrUnavailable
	}
	// Revalidate after the remote call: a concurrent leave invalidates delivery.
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		owner, e := authorize(tx, device, 0)
		if e != nil {
			return e
		}
		a, e := tx.Assignment(device)
		if e != nil {
			return e
		}
		l, e := tx.Lease(device)
		if e != nil {
			return e
		}
		if !matches(a, p) || a.Owner != owner || l.SessionID != p.SessionID || l.State == "ended" || !l.Expires.After(s.now()) {
			return ErrStale
		}
		return nil
	})
	if err != nil {
		return Credential{}, err
	}
	credential.HeartbeatSeconds = int64(s.policy.Load().Heartbeat / time.Second)
	credential.LeaseSeconds = int64(s.policy.Load().Lease / time.Second)
	return credential, nil
}
func (s *Service) Report(ctx context.Context, device string, p Presence) error {
	if !validSession(p) {
		return ErrInvalid
	}
	switch p.State {
	case "joined", "connecting", "suspended", "left", "connect_failed":
	default:
		return ErrInvalid
	}
	return s.repo.Transaction(ctx, func(tx Tx) error {
		owner, e := authorize(tx, device, 0)
		if e != nil {
			return e
		}
		a, e := tx.Assignment(device)
		if e != nil {
			return e
		}
		if !matches(a, p) || a.Owner != owner {
			return ErrStale
		}
		r, e := tx.Room(p.RoomID, false)
		if e != nil {
			return e
		}
		if r.Status == "closed" {
			return ErrNotFound
		}
		l, e := tx.Lease(device)
		if e != nil {
			return e
		}
		if p.State == "suspended" && a.SessionID == "" {
			a.State = p.State
			return tx.SaveAssignment(a)
		}
		if l.SessionID != p.SessionID || l.Version != p.Version || l.RoomID != p.RoomID {
			return ErrStale
		}
		now := s.now()
		if l.State == "ended" && a.State == p.State && (p.State == "suspended" || p.State == "left" || p.State == "connect_failed") {
			return nil
		}
		if l.State == "ended" || !l.Expires.After(now) {
			return ErrStale
		}
		if p.State == "joined" {
			l.State = "joined"
			l.Expires = now.Add(s.policy.Load().Lease)
		} else if p.State != "connecting" {
			l.State = "ended"
			l.Expires = now
		}
		a.State = p.State
		if e = tx.SaveLease(l); e != nil {
			return e
		}
		if e = tx.SaveAssignment(a); e != nil {
			return e
		}
		return s.refresh(tx, &r, now)
	})
}
func (s *Service) Tick(ctx context.Context) error {
	ids, err := s.repo.DueRooms(ctx, 100)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.repo.Transaction(ctx, func(tx Tx) error {
			r, e := tx.Room(id, false)
			if e != nil {
				return e
			}
			return s.refresh(tx, &r, s.now())
		}); err != nil {
			return err
		}
	}
	events, err := s.repo.Events(ctx, 32)
	if err != nil {
		return err
	}
	for _, event := range events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e := s.signals.Notify(ctx, event, s.policy.Load().CommandTTL)
		if err = s.repo.CompleteEvent(ctx, event, e == nil); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				onError(err)
			}
		}
	}
}

// Unbind revokes an old owner's assignment. A delayed cleanup cannot revoke a
// new assignment already authorized for the currently bound owner.
func (s *Service) Unbind(ctx context.Context, device string) error {
	return s.repo.Transaction(ctx, func(tx Tx) error {
		owner, err := tx.Owner(device)
		if err != nil {
			return err
		}
		a, err := tx.Assignment(device)
		if err != nil {
			return err
		}
		if a.Desired != "joined" || (owner != 0 && owner == a.Owner) {
			return nil
		}
		r, err := tx.Room(a.RoomID, false)
		if err != nil {
			return err
		}
		l, err := tx.Lease(device)
		if err != nil {
			return err
		}
		if l.RoomID == r.ID {
			l.State = "ended"
			l.Expires = s.now()
			if err = tx.SaveLease(l); err != nil {
				return err
			}
		}
		a.Desired = "left"
		a.State = "left"
		a.Version++
		a.SessionID = ""
		if err = tx.SaveAssignment(a); err != nil {
			return err
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		if err = tx.Notify(Event{ID: id, DeviceID: device, Version: a.Version}); err != nil {
			return err
		}
		return s.refresh(tx, &r, s.now())
	})
}
