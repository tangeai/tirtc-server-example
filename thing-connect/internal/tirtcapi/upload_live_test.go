//go:build live

package tirtcapi

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestUploadKnowledgeFile_Live 走完整的 UploadKnowledgeFile 代码路径，真实上传
// 一个探针文件，验证 multipart 签名修复后能通过（修复前 tange 返回 wrong signature）。
//
// 运行（凭据从环境变量读，不落盘）：
//
//	TANGE_BASE_URL=https://openapi-cn01.tange365.com \
//	TANGE_APP_ID=... TANGE_AK=... TANGE_SK=... \
//	go test -tags=live -run TestUploadKnowledgeFile_Live -v ./internal/tirtcapi/
func TestUploadKnowledgeFile_Live(t *testing.T) {
	base := os.Getenv("TANGE_BASE_URL")
	appID := os.Getenv("TANGE_APP_ID")
	ak := os.Getenv("TANGE_AK")
	sk := os.Getenv("TANGE_SK")
	if base == "" || appID == "" || ak == "" || sk == "" {
		t.Skip("需要 env: TANGE_BASE_URL / TANGE_APP_ID / TANGE_AK / TANGE_SK")
	}

	cfg := AgentAPIConfig{
		BaseURL:     base,
		AppID:       appID,
		AccessKeyID: ak,
		SecretKeyID: sk,
	}
	client := NewAgentAPIClient(cfg, &http.Client{Timeout: 30 * time.Second})

	resp, err := client.UploadKnowledgeFile(context.Background(), "sig-probe.txt", []byte("signature probe\n"))
	if err != nil {
		t.Fatalf("上传失败（签名应已修复）: %v", err)
	}
	t.Logf("上传成功，file_id=%s", resp.FileID)
}

// TestDeleteKnowledgeFile_Live 删除一个已上传的知识库文件，用于清理探针文件。
//
//	TANGE_BASE_URL=... TANGE_APP_ID=... TANGE_AK=... TANGE_SK=... \
//	TANGE_FILE_ID=file_xxx go test -tags=live -run TestDeleteKnowledgeFile_Live -v ./internal/tirtcapi/
func TestDeleteKnowledgeFile_Live(t *testing.T) {
	base := os.Getenv("TANGE_BASE_URL")
	appID := os.Getenv("TANGE_APP_ID")
	ak := os.Getenv("TANGE_AK")
	sk := os.Getenv("TANGE_SK")
	fileID := os.Getenv("TANGE_FILE_ID")
	if base == "" || fileID == "" {
		t.Skip("需要 env: TANGE_BASE_URL/TANGE_APP_ID/TANGE_AK/TANGE_SK/TANGE_FILE_ID")
	}

	cfg := AgentAPIConfig{BaseURL: base, AppID: appID, AccessKeyID: ak, SecretKeyID: sk}
	client := NewAgentAPIClient(cfg, &http.Client{Timeout: 30 * time.Second})

	if err := client.DeleteKnowledgeFile(context.Background(), fileID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	t.Logf("已删除 file_id=%s", fileID)
}
