package boardcatalog

import (
	"strings"
	"testing"
)

func TestNormalizeAndValidate(t *testing.T) {
	board := Normalize(Board{Vendor: " 厂商 ", Name: " 开发板 ", Model: " BOARD-1 ", Chip: " ESP32-S3 ", Summary: " 介绍 ", Capabilities: []string{" 音视频 "}, AdaptationStatus: "ready", ImageURL: "https://cdn.example.com/board.webp", DetailSlug: " Board-1 ", PublishStatus: "draft"})
	if board.Vendor != "厂商" || board.DetailSlug != "board-1" || board.Capabilities[0] != "音视频" {
		t.Fatalf("normalized board = %+v", board)
	}
	if err := Validate(board); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidBoard(t *testing.T) {
	board := Normalize(Board{Vendor: "厂商", Name: "开发板", Model: "BOARD-1", Chip: "ESP32-S3", Summary: "介绍", AdaptationStatus: "ready", ImageURL: "http://cdn.example.com/board.webp", DetailSlug: "board-1", PublishStatus: "published"})
	if err := Validate(board); err == nil {
		t.Fatal("insecure image URL accepted")
	}
}

func TestValidateAcceptsManagedBoardImage(t *testing.T) {
	board := Normalize(Board{Vendor: "厂商", Name: "开发板", Model: "BOARD-1", Chip: "ESP32-S3", Summary: "介绍", AdaptationStatus: "ready", ImageURL: "/v1/board-images/" + strings.Repeat("a", 64) + ".webp", DetailSlug: "board-1", PublishStatus: "published"})
	if err := Validate(board); err != nil {
		t.Fatal(err)
	}
}
