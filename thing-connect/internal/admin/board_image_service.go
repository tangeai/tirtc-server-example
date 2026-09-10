package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const MaxBoardImageBytes int64 = 5 << 20

var ErrInvalidBoardImage = errors.New("admin: invalid board image")

type BoardImageService struct{ dir string }

type BoardImage struct {
	URL  string `json:"url"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

func NewBoardImageService(dir string) (*BoardImageService, error) {
	if dir == "" {
		return nil, fmt.Errorf("board image directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create board image directory: %w", err)
	}
	return &BoardImageService{dir: dir}, nil
}

func (s *BoardImageService) Save(reader io.Reader) (BoardImage, error) {
	content, err := io.ReadAll(io.LimitReader(reader, MaxBoardImageBytes+1))
	if err != nil {
		return BoardImage{}, fmt.Errorf("read board image: %w", err)
	}
	if len(content) == 0 || int64(len(content)) > MaxBoardImageBytes {
		return BoardImage{}, ErrInvalidBoardImage
	}
	mimeType := http.DetectContentType(content)
	extension := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}[mimeType]
	if extension == "" {
		return BoardImage{}, ErrInvalidBoardImage
	}
	digest := sha256.Sum256(content)
	name := hex.EncodeToString(digest[:]) + "." + extension
	target := filepath.Join(s.dir, name)
	if _, err := os.Stat(target); err == nil {
		return BoardImage{URL: "/v1/board-images/" + name, Size: int64(len(content)), Type: mimeType}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return BoardImage{}, fmt.Errorf("inspect board image: %w", err)
	}
	temporary, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return BoardImage{}, fmt.Errorf("create board image: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return BoardImage{}, fmt.Errorf("set board image permissions: %w", err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(content)); err != nil {
		_ = temporary.Close()
		return BoardImage{}, fmt.Errorf("write board image: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return BoardImage{}, fmt.Errorf("sync board image: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return BoardImage{}, fmt.Errorf("close board image: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return BoardImage{}, fmt.Errorf("publish board image: %w", err)
	}
	return BoardImage{URL: "/v1/board-images/" + name, Size: int64(len(content)), Type: mimeType}, nil
}
