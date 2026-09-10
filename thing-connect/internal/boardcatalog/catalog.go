// Package boardcatalog owns the public board catalog model and read use cases.
package boardcatalog

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const PublicLimit = 100

var ErrNotFound = errors.New("board catalog: not found")

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$`)
var managedImagePattern = regexp.MustCompile(`^/v1/board-images/[a-f0-9]{64}\.(?:jpg|png|webp)$`)

type Board struct {
	ID               string     `db:"id" json:"id"`
	Vendor           string     `db:"vendor" json:"vendor"`
	Name             string     `db:"name" json:"name"`
	Model            string     `db:"model" json:"model"`
	Chip             string     `db:"chip" json:"chip"`
	Summary          string     `db:"summary" json:"summary"`
	Capabilities     []string   `db:"-" json:"capabilities"`
	AdaptationStatus string     `db:"adaptation_status" json:"adaptation_status"`
	ImageURL         string     `db:"image_url" json:"image_url"`
	PurchaseURL      string     `db:"purchase_url" json:"purchase_url,omitempty"`
	RepositoryURL    string     `db:"repository_url" json:"repository_url,omitempty"`
	FirmwareURL      string     `db:"firmware_url" json:"firmware_url,omitempty"`
	FlashingGuideURL string     `db:"flashing_guide_url" json:"flashing_guide_url,omitempty"`
	EffectVideoURL   string     `db:"effect_video_url" json:"effect_video_url,omitempty"`
	DetailSlug       string     `db:"detail_slug" json:"detail_slug"`
	SortOrder        int        `db:"sort_order" json:"sort_order"`
	PublishStatus    string     `db:"publish_status" json:"publish_status"`
	Revision         int64      `db:"revision" json:"revision,omitempty"`
	PublishedAt      *time.Time `db:"published_at" json:"published_at,omitempty"`
	CreatedBy        int64      `db:"created_by" json:"created_by,omitempty"`
	UpdatedBy        int64      `db:"updated_by" json:"updated_by,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at" json:"updated_at"`
}

func Normalize(board Board) Board {
	board.ID = strings.TrimSpace(board.ID)
	board.Vendor = strings.TrimSpace(board.Vendor)
	board.Name = strings.TrimSpace(board.Name)
	board.Model = strings.TrimSpace(board.Model)
	board.Chip = strings.TrimSpace(board.Chip)
	board.Summary = strings.TrimSpace(board.Summary)
	board.AdaptationStatus = strings.TrimSpace(board.AdaptationStatus)
	board.ImageURL = strings.TrimSpace(board.ImageURL)
	board.PurchaseURL = strings.TrimSpace(board.PurchaseURL)
	board.RepositoryURL = strings.TrimSpace(board.RepositoryURL)
	board.FirmwareURL = strings.TrimSpace(board.FirmwareURL)
	board.FlashingGuideURL = strings.TrimSpace(board.FlashingGuideURL)
	board.EffectVideoURL = strings.TrimSpace(board.EffectVideoURL)
	board.DetailSlug = strings.ToLower(strings.TrimSpace(board.DetailSlug))
	board.PublishStatus = strings.TrimSpace(board.PublishStatus)
	for index := range board.Capabilities {
		board.Capabilities[index] = strings.TrimSpace(board.Capabilities[index])
	}
	return board
}

func Validate(board Board) error {
	for _, field := range []string{board.Vendor, board.Name, board.Model, board.Chip, board.Summary, board.AdaptationStatus, board.ImageURL, board.DetailSlug, board.PublishStatus} {
		if field == "" {
			return errors.New("开发板名称、型号、芯片、厂商、介绍、图片、状态和详情标识不能为空")
		}
	}
	for value, maximum := range map[string]int{board.ID: 36, board.Vendor: 80, board.Name: 80, board.Model: 120, board.Chip: 80} {
		if utf8.RuneCountInString(value) > maximum {
			return errors.New("开发板名称、型号、芯片、厂商或标识超过长度限制")
		}
	}
	if utf8.RuneCountInString(board.Summary) > 160 {
		return errors.New("开发板介绍不能超过 160 个字符")
	}
	if !slugPattern.MatchString(board.DetailSlug) {
		return errors.New("详情地址标识只允许小写字母、数字和连字符，长度为 1–80 个字符")
	}
	if board.SortOrder < 0 {
		return errors.New("展示顺序不能小于 0")
	}
	if board.AdaptationStatus != "ready" && board.AdaptationStatus != "adapting" && board.AdaptationStatus != "planned" {
		return errors.New("适配状态必须为已适配、适配中或计划中")
	}
	if board.PublishStatus != "draft" && board.PublishStatus != "published" && board.PublishStatus != "offline" {
		return errors.New("上架状态必须为草稿、已上架或已下架")
	}
	if !validHTTPS(board.ImageURL) && !managedImagePattern.MatchString(board.ImageURL) {
		return errors.New("产品图片只允许后台上传地址或有效的 HTTPS 地址")
	}
	for _, link := range []string{board.PurchaseURL, board.RepositoryURL, board.FirmwareURL, board.FlashingGuideURL, board.EffectVideoURL} {
		if link != "" && !validHTTPS(link) {
			return errors.New("资源链接只允许有效的 HTTPS 地址")
		}
	}
	if len(board.Capabilities) > 10 {
		return errors.New("能力标签最多 10 个")
	}
	seen := make(map[string]bool, len(board.Capabilities))
	for _, capability := range board.Capabilities {
		key := strings.ToLower(capability)
		if capability == "" || utf8.RuneCountInString(capability) > 20 {
			return errors.New("能力标签须为 1–20 个字符")
		}
		if seen[key] {
			return errors.New("能力标签不能重复")
		}
		seen[key] = true
	}
	return nil
}

func validHTTPS(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil &&
		len(raw) <= 2048 && !strings.ContainsAny(raw, "\\\r\n\t ")
}

type Reader interface {
	ListPublished(ctx context.Context, limit int) ([]Board, error)
	PublishedBySlug(ctx context.Context, slug string) (Board, error)
}

type Service struct{ reader Reader }

func New(reader Reader) *Service { return &Service{reader: reader} }

func (s *Service) List(ctx context.Context) ([]Board, error) {
	boards, err := s.reader.ListPublished(ctx, PublicLimit)
	if err != nil {
		return nil, err
	}
	for index := range boards {
		boards[index] = publicBoard(boards[index])
	}
	return boards, nil
}

func (s *Service) BySlug(ctx context.Context, slug string) (Board, error) {
	board, err := s.reader.PublishedBySlug(ctx, strings.TrimSpace(slug))
	if err != nil {
		return Board{}, err
	}
	return publicBoard(board), nil
}

func publicBoard(board Board) Board {
	board.Revision, board.CreatedBy, board.UpdatedBy = 0, 0, 0
	return board
}
