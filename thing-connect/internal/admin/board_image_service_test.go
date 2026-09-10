package admin

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardImageServiceSavesContentAddressedImage(t *testing.T) {
	var source bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 12, G: 80, B: 120, A: 255})
	if err := png.Encode(&source, picture); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	service, err := NewBoardImageService(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Save(bytes.NewReader(source.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Save(bytes.NewReader(source.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first.URL, "/v1/board-images/") || first.Type != "image/png" {
		t.Fatalf("unexpected upload result: %+v / %+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(first.URL))); err != nil {
		t.Fatalf("saved image: %v", err)
	}
}

func TestBoardImageServiceRejectsInvalidAndOversizedFiles(t *testing.T) {
	service, err := NewBoardImageService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range [][]byte{[]byte("not an image"), make([]byte, MaxBoardImageBytes+1)} {
		if _, err := service.Save(bytes.NewReader(source)); !errors.Is(err, ErrInvalidBoardImage) {
			t.Fatalf("Save error = %v", err)
		}
	}
}
