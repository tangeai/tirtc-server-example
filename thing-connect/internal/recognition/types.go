// Package recognition owns versioned media analysis, ordered asynchronous jobs and output policy.
package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("识别参数无效")
	ErrUnbound     = errors.New("设备已解绑或重置")
	ErrForbidden   = errors.New("无权访问设备")
	ErrDisabled    = errors.New("设备识别未启用")
	ErrQuota       = errors.New("本月识别额度已用完")
	ErrConflict    = errors.New("配置或请求已变更")
	ErrUnavailable = errors.New("识别服务暂不可用")
	ErrNotFound    = errors.New("识别记录不存在")
)

const MaxUploadBytes int64 = 6 << 20
const MaxTypes = 8

type Actor struct {
	UserID   int64
	DeviceID string
}
type Limits struct {
	MaxFileBytes    int64 `json:"max_file_bytes"`
	MaxAudioSeconds int   `json:"max_audio_seconds"`
}
type Scheme struct {
	ID          string `json:"scheme_id"`
	Version     int64  `json:"scheme_version"`
	Revision    int64  `json:"revision"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MediaType   string `json:"media_type"`
	Model       string `json:"model"`
	Prompt      string `json:"prompt,omitempty"`
	OutputMode  string `json:"output_mode"`
	Voice       string `json:"voice"`
	Limits      Limits `json:"limits"`
	Enabled     bool   `json:"enabled"`
}
type Binding struct {
	ID      string `json:"scheme_id"`
	Version int64  `json:"scheme_version"`
}
type Policy struct {
	Enabled      bool      `json:"enabled"`
	Allowed      bool      `json:"allowed"`
	MonthlyLimit int64     `json:"monthly_limit"`
	Revision     int64     `json:"revision"`
	Schemes      []Binding `json:"schemes"`
}

func DefaultPolicy() Policy { return Policy{Allowed: true, MonthlyLimit: 100, Schemes: []Binding{}} }

type Usage struct {
	Month         string `json:"month"`
	Used          int64  `json:"used"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	Unknown       int64  `json:"unknown_calls"`
	TTSCharacters int64  `json:"tts_characters"`
}
type State struct {
	Policy           Policy   `json:"policy"`
	Schemes          []Scheme `json:"schemes"`
	Usage            Usage    `json:"usage"`
	EffectiveEnabled bool     `json:"effective_enabled"`
	Limits           Limits   `json:"upload_limits"`
}
type Request struct {
	// RejectNew gates creation only; previously accepted requests remain replayable.
	RejectNew   bool
	ID          string
	Revision    int64
	MediaType   string
	AudioFormat string
	SampleRate  int
	Channels    int
	Data        []byte
}
type Result struct {
	Summary string         `json:"summary"`
	Alarm   bool           `json:"alarm"`
	Labels  []string       `json:"labels"`
	Details map[string]any `json:"details"`
}
type Failure struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}
type Speech struct {
	Status string   `json:"status"`
	URL    string   `json:"url,omitempty"`
	Format string   `json:"format,omitempty"`
	Error  *Failure `json:"error"`
}
type Item struct {
	ID            string   `json:"item_id"`
	Order         int      `json:"order"`
	SchemeID      string   `json:"scheme_id"`
	SchemeVersion int64    `json:"scheme_version"`
	OutputMode    string   `json:"output_mode"`
	Status        string   `json:"status"`
	Result        *Result  `json:"result"`
	Error         *Failure `json:"error"`
	Speech        Speech   `json:"speech"`
}
type Task struct {
	Cursor    int64     `json:"cursor,omitempty"`
	ID        string    `json:"request_id"`
	DeviceID  string    `json:"device_id"`
	Status    string    `json:"status"`
	Revision  int64     `json:"status_revision"`
	Results   []Item    `json:"results"`
	Error     *Failure  `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}
type CallUsage struct {
	Model      string          `json:"model"`
	Input      *int64          `json:"input_tokens"`
	Output     *int64          `json:"output_tokens"`
	Total      *int64          `json:"total_tokens"`
	Characters *int64          `json:"characters"`
	ProviderID string          `json:"provider_request_id"`
	Raw        json.RawMessage `json:"usage,omitempty"`
}
type Work struct {
	TaskKey        int64
	ItemIndex      int
	Item           Item
	Scheme         Scheme
	Data           []byte
	Stage          string
	InternalResult *Result
	Lease          string
}
type Completion struct {
	Work    Work
	Result  *Result
	Audio   []byte
	Usage   CallUsage
	Failed  bool
	Unknown bool
}
type Event struct {
	ID       int64
	DeviceID string
	Payload  []byte
}
type Provider interface {
	Analyze(context.Context, Config, Scheme, []byte) (Result, CallUsage, error)
	Synthesize(context.Context, Config, Scheme, string) ([]byte, CallUsage, error)
}
type Notifier interface {
	Publish(context.Context, string, []byte) error
}

// Store atomically checks ownership, versions and quota under the device lock.
// A lease expires to unknown, never to queued: upstream calls must not be repeated blindly.
type Store interface {
	State(context.Context, Actor, string) (State, error)
	SetSelection(context.Context, Actor, string, bool, []Binding, int64) (State, error)
	Types(context.Context) ([]Scheme, error)
	Submit(context.Context, Actor, string, Request, string) (Task, bool, error)
	Get(context.Context, Actor, string, string) (Task, error)
	List(context.Context, Actor, string, int64) ([]Task, error)
	Audio(context.Context, Actor, string, string, string) ([]byte, error)
	Claim(context.Context) (*Work, error)
	Complete(context.Context, Completion) error
	Recover(context.Context) error
	Events(context.Context) ([]Event, error)
	Deliver(context.Context, Event, bool) error
}
