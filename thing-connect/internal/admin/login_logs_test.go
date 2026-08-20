package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/db"
	"thing-connect/internal/testenv"
)

func TestLoginLogsFallbackToAdminEmail(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	defer sqlDB.Close()
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}

	email := fmt.Sprintf("login-log-%d@example.com", time.Now().UnixNano())
	result, err := sqlDB.Exec(`INSERT INTO admin_users (email,password,nick_name) VALUES (?,?,?)`, email, "unused", "测试管理员")
	if err != nil {
		t.Fatal(err)
	}
	adminID, _ := result.LastInsertId()
	defer sqlDB.Exec(`DELETE FROM admin_users WHERE id=?`, adminID)
	if _, err := sqlDB.Exec(`INSERT INTO admin_login_log (admin_user_id,email,status,message) VALUES (?,'',0,'invalid MFA')`, adminID); err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Exec(`DELETE FROM admin_login_log WHERE admin_user_id=?`, adminID)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &HTTPServer{store: NewStore(sqlDB)}
	router.GET("/login-logs", server.loginLogs)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/login-logs?email="+email, nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Items []struct {
				Email string `json:"email"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].Email != email {
		t.Fatalf("unexpected login logs: %#v", response.Data.Items)
	}
}
