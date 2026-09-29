package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestAdminDeviceViewsExposeOptionalProfileMetadata(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}

	deviceID := "admin-metadata-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := db.Exec(`INSERT INTO device_bind (device_id,device_name) VALUES (?,?)`, deviceID, "测试设备"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM device_profile WHERE device_id=?`, deviceID)
		_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
	})
	profile := `{"hardware":{"chip_model":"arm64","board_model":"board-a"},"firmware_version":"1.1.1","voip":{"audio_rate":8000}}`
	if _, err := db.Exec(`INSERT INTO device_profile (device_id,profile) VALUES (?,?)`, deviceID, profile); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	server := &HTTPServer{store: NewStore(db)}
	router.GET("/devices/:device_id", server.getDevice)
	router.GET("/voip/devices/:device_id/profile", server.voipDeviceProfile)

	for _, path := range []string{"/devices/" + deviceID, "/voip/devices/" + deviceID + "/profile"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		var response struct {
			Data struct {
				Hardware struct {
					ChipModel  string `json:"chip_model"`
					BoardModel string `json:"board_model"`
				} `json:"hardware"`
				FirmwareVersion string         `json:"firmware_version"`
				Attributes      map[string]any `json:"attributes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.Hardware.ChipModel != "arm64" || response.Data.Hardware.BoardModel != "board-a" || response.Data.FirmwareVersion != "1.1.1" {
			t.Fatalf("GET %s metadata=%+v firmware=%q", path, response.Data.Hardware, response.Data.FirmwareVersion)
		}
		if path == "/voip/devices/"+deviceID+"/profile" && response.Data.Attributes["audio_rate"] != float64(8000) {
			t.Fatalf("GET %s attributes=%v", path, response.Data.Attributes)
		}
	}
}
