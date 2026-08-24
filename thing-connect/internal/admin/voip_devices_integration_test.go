package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestVoIPAppDevicesFiltersAndPaginates(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	if len(suffix) > 10 {
		suffix = suffix[len(suffix)-10:]
	}
	appID := "wx" + suffix
	activeDeviceID := "VA" + suffix
	invalidDeviceID := "VI" + suffix
	activeOpenID := "openid-active-" + suffix
	invalidOpenID := "openid-invalid-" + suffix
	email := "voip-list-" + suffix + "@example.com"

	userResult, err := db.Exec(`INSERT INTO users (email,password) VALUES (?,?)`, email, "unused")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := userResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM voip_device_profile WHERE device_id IN (?,?)`, activeDeviceID, invalidDeviceID)
		_, _ = db.Exec(`DELETE FROM voip_device_auth WHERE wx_app_id=?`, appID)
		_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id IN (?,?)`, activeDeviceID, invalidDeviceID)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, userID)
	})

	if _, err := db.Exec(`INSERT INTO device_bind (device_id,user_id,device_name) VALUES (?,?,?)`, activeDeviceID, userID, "测试设备"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO voip_device_auth (device_id,wx_open_id,wx_app_id,wx_model_id,authorized_device_name,auth_status,last_verified_at) VALUES (?,?,?,?,?,'active',NOW())`, activeDeviceID, activeOpenID, appID, "model-active", "客厅设备"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO voip_device_auth (device_id,wx_open_id,wx_app_id,wx_model_id,authorized_device_name,auth_status,invalid_reason) VALUES (?,?,?,?,?,'invalid','auth_revoked')`, invalidDeviceID, invalidOpenID, appID, "model-invalid", "失效设备"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO voip_device_profile (device_id,profile) VALUES (?,?)`, activeDeviceID, `{}`); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &HTTPServer{store: NewStore(db)}
	router.GET("/voip/apps/:app_id/devices", server.voipAppDevices)

	type deviceRow struct {
		DeviceID   string `json:"device_id"`
		OwnerEmail string `json:"owner_email"`
		AuthStatus string `json:"auth_status"`
	}
	type response struct {
		Code int `json:"code"`
		Data struct {
			Items    []deviceRow `json:"items"`
			Page     int         `json:"page"`
			PageSize int         `json:"page_size"`
			Total    int         `json:"total"`
		} `json:"data"`
	}
	request := func(query string) (int, response) {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/voip/apps/%s/devices?%s", appID, query), nil))
		var body response
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
		}
		return recorder.Code, body
	}

	status, all := request("page=1&page_size=1")
	if status != http.StatusOK || all.Code != 200 || all.Data.Total != 2 || len(all.Data.Items) != 1 || all.Data.PageSize != 1 {
		t.Fatalf("unexpected paginated response: status=%d body=%+v", status, all)
	}

	status, active := request("page=1&page_size=20&keyword=" + url.QueryEscape(email) + "&auth_status=active&profile_reported=true")
	if status != http.StatusOK || active.Data.Total != 1 || len(active.Data.Items) != 1 || active.Data.Items[0].DeviceID != activeDeviceID || active.Data.Items[0].OwnerEmail != email {
		t.Fatalf("unexpected active filter response: status=%d body=%+v", status, active)
	}

	status, invalid := request("page=1&page_size=20&keyword=" + url.QueryEscape(invalidOpenID) + "&auth_status=invalid&profile_reported=false")
	if status != http.StatusOK || invalid.Data.Total != 1 || len(invalid.Data.Items) != 1 || invalid.Data.Items[0].DeviceID != invalidDeviceID || invalid.Data.Items[0].AuthStatus != "invalid" {
		t.Fatalf("unexpected invalid filter response: status=%d body=%+v", status, invalid)
	}

	status, _ = request("auth_status=unknown")
	if status != http.StatusBadRequest {
		t.Fatalf("invalid auth status returned HTTP %d", status)
	}
}
