// Package conversations owns access to device conversation history.
package conversations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrForbidden = errors.New("无权查看该设备的会话记录，请刷新列表")
	ErrInvalid   = errors.New("会话记录参数不正确")
	ErrUpstream  = errors.New("会话服务暂不可用")
	ErrNotFound  = errors.New("会话已不存在，请刷新列表")
)

type Entry struct {
	ID          string  `json:"id"`
	DeviceID    string  `json:"device_id"`
	AssistantID string  `json:"assistant_id"`
	StartedAt   *string `json:"started_at"`
	Reference   string  `json:"reference,omitempty"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Time    string `json:"time"`
}
type Page struct {
	Items    []Entry `json:"items"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	HasMore  bool    `json:"has_more"`
}
type Ownership interface {
	UserOwnsDevice(context.Context, string, int64) (bool, error)
}
type Cloud interface {
	Scope() string
	ListConversations(context.Context, string, int, int) ([]Entry, error)
	ConversationDetail(context.Context, string) ([]Message, error)
	DeleteConversation(context.Context, string) error
}
type History struct {
	owners Ownership
	cloud  func() Cloud
	key    []byte
	now    func() time.Time
}

func New(owners Ownership, cloud func() Cloud, secret string) *History {
	return &History{owners: owners, cloud: cloud, key: []byte(secret), now: time.Now}
}
func (h *History) authorize(ctx context.Context, user int64, device string) error {
	if user <= 0 || device == "" || len(device) > 200 {
		return ErrInvalid
	}
	ok, err := h.owners.UserOwnsDevice(ctx, device, user)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func (h *History) List(ctx context.Context, user int64, device string, page, size int) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if page < 1 || page > 100000 || size < 1 || size > 50 {
		return Page{}, ErrInvalid
	}
	if err := h.authorize(ctx, user, device); err != nil {
		return Page{}, err
	}
	cloud := h.cloud()
	if cloud == nil || len(h.key) == 0 {
		return Page{}, ErrUpstream
	}
	items, err := cloud.ListConversations(ctx, device, page, size)
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	if len(items) > size {
		return Page{}, ErrUpstream
	}
	result := Page{Items: make([]Entry, 0, len(items)), Page: page, PageSize: size, HasMore: len(items) == size}
	for _, item := range items {
		if item.ID == "" || len(item.ID) > 200 || item.DeviceID != device {
			return Page{}, ErrUpstream
		}
		item.Reference = h.sign(receipt{User: user, Device: device, ID: item.ID, Scope: cloud.Scope(), Expires: h.now().Add(time.Hour).Unix()})
		result.Items = append(result.Items, item)
	}
	// Do not release history if the device was unbound during the cloud request.
	if err := h.authorize(ctx, user, device); err != nil {
		return Page{}, err
	}
	return result, nil
}

// The upstream detail/delete APIs only accept a conversation ID. A signed,
// short-lived receipt proves that ID came from this user's device listing,
// without trusting client-supplied IDs or scanning every page per operation.
type receipt struct {
	User    int64
	Device  string
	ID      string
	Scope   string
	Expires int64
}

func (h *History) signature(body string) []byte {
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte("conversation-reference-v1\x00" + body))
	return mac.Sum(nil)
}
func (h *History) sign(r receipt) string {
	b, _ := json.Marshal(r)
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + base64.RawURLEncoding.EncodeToString(h.signature(body))
}
func (h *History) verify(reference string, user int64, device, id, scope string) error {
	if len(reference) > 4096 || len(h.key) == 0 {
		return ErrForbidden
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 {
		return ErrForbidden
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, h.signature(parts[0])) {
		return ErrForbidden
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrForbidden
	}
	var r receipt
	if json.Unmarshal(b, &r) != nil || r.User != user || r.Device != device || r.ID != id || r.Scope != scope || r.Expires <= h.now().Unix() {
		return ErrForbidden
	}
	return nil
}
func (h *History) Detail(ctx context.Context, user int64, device, id, reference string) ([]Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cloud, err := h.access(ctx, user, device, id, reference)
	if err != nil {
		return nil, err
	}
	messages, err := cloud.ConversationDetail(ctx, id)
	if err != nil {
		return nil, cloudError(err)
	}
	if err := h.authorize(ctx, user, device); err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []Message{}
	}
	return messages, nil
}
func (h *History) Delete(ctx context.Context, user int64, device, id, reference string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cloud, err := h.access(ctx, user, device, id, reference)
	if err != nil {
		return err
	}
	err = cloud.DeleteConversation(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return cloudError(err)
}
func (h *History) access(ctx context.Context, user int64, device, id, reference string) (Cloud, error) {
	if err := h.authorize(ctx, user, device); err != nil {
		return nil, err
	}
	cloud := h.cloud()
	if cloud == nil {
		return nil, ErrUpstream
	}
	if err := h.verify(reference, user, device, id, cloud.Scope()); err != nil {
		return nil, err
	}
	return cloud, nil
}
func cloudError(err error) error {
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrUpstream, err)
}
