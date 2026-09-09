package tests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Exercise both real HTTP servers and MySQL/Redis: device reports must become
// visible only to its owner, and invalid/unbound reports must not change data.
func TestDeviceProfileReportToUserDeviceList(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)
	newUser := func() int64 {
		r, err := s.sqlDB.Exec(`INSERT INTO users(email,password,bind_quota) VALUES(?,?,10)`, uniqueEmail(), "profile-test-hash")
		if err != nil {
			t.Fatal(err)
		}
		id, err := r.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.sqlDB.Exec(`DELETE FROM users WHERE id=?`, id) })
		return id
	}
	owner, other := newUser(), newUser()
	device := fmt.Sprintf("PROF%x", time.Now().UnixNano())
	if _, err := s.sqlDB.Exec(`INSERT INTO device_bind(device_id,user_id,device_name,bind_time) VALUES(?,?,?,NOW())`, device, owner, "上报验收设备"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.sqlDB.Exec(`DELETE FROM device_profile WHERE device_id=?`, device)
		s.sqlDB.Exec(`DELETE FROM voip_device_profile WHERE device_id=?`, device)
		s.sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	if _, err := s.sqlDB.Exec(`INSERT INTO voip_device_profile(device_id,profile) VALUES(?,?)`, device, `{"down_audio_mt":"amr","camera_rotation":90}`); err != nil {
		t.Fatal(err)
	}
	deviceToken := deviceJWT(t, cfg.JWTSecret, device)
	ownerToken := userJWT(t, cfg.JWTSecret, owner)
	otherToken := userJWT(t, cfg.JWTSecret, other)
	post := func(body any) *apiResp { return doPost(t, s.devSrv.URL+"/v1/device/profile", deviceToken, body) }
	type deviceInfo struct {
		DeviceID string                                `json:"device_id"`
		Profiles map[string]map[string]json.RawMessage `json:"profiles"`
	}
	list := func(token string) []deviceInfo {
		r := s.usrGET(t, "/v1/user/device/list", token)
		if r.Code != 200 || r.HTTPStatus != 200 {
			t.Fatalf("list status=%d code=%d", r.HTTPStatus, r.Code)
		}
		var rows []deviceInfo
		if err := json.Unmarshal(r.Data, &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	snapshot := map[string]any{"profiles": map[string]any{
		"stream": map[string]any{"up_audio_mt": []string{"pcm"}, "up_video_mt": []string{}, "camera_rotation": 0, "hor_mirror": false},
		"call":   map[string]any{"down_audio_mt": []string{"opus", "amr"}, "audio_rate": 16000},
	}}
	for i := 0; i < 2; i++ {
		r := post(snapshot)
		if r.HTTPStatus != 200 || r.Code != 200 {
			t.Fatalf("report attempt %d: %+v", i, r)
		}
	}
	rows := list(ownerToken)
	if len(rows) != 1 || rows[0].DeviceID != device {
		t.Fatalf("owner devices=%+v", rows)
	}
	profiles := rows[0].Profiles
	if string(profiles["stream"]["hor_mirror"]) != "false" || string(profiles["stream"]["camera_rotation"]) != "0" {
		t.Fatal("explicit values lost", profiles)
	}
	if string(profiles["voip"]["down_audio_mt"]) != `"amr"` {
		t.Fatal("legacy VoIP source lost")
	}
	var formats []string
	_ = json.Unmarshal(profiles["call"]["down_audio_mt"], &formats)
	if len(formats) != 2 || formats[0] != "opus" || formats[1] != "amr" {
		t.Fatal("codec preference changed", formats)
	}
	if len(list(otherToken)) != 0 {
		t.Fatal("other owner can see device profile")
	}
	var count int
	if err := s.sqlDB.Get(&count, `SELECT COUNT(*) FROM device_profile WHERE device_id=?`, device); err != nil || count != 1 {
		t.Fatalf("duplicate report rows=%d err=%v", count, err)
	}
	if err := s.sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='device_media_profile'`); err != nil || count != 0 {
		t.Fatal("obsolete table exists", count, err)
	}
	var before string
	_ = s.sqlDB.Get(&before, `SELECT profile FROM device_profile WHERE device_id=?`, device)
	invalid := post(map[string]any{"profiles": map[string]any{"stream": map[string]any{"device_key": "not-allowed"}}})
	if invalid.HTTPStatus != 400 || invalid.Code != 40000 {
		t.Fatal("invalid field accepted", invalid)
	}
	var after string
	_ = s.sqlDB.Get(&after, `SELECT profile FROM device_profile WHERE device_id=?`, device)
	if after != before {
		t.Fatal("invalid report partially committed")
	}
	denied := doPost(t, s.devSrv.URL+"/v1/device/profile", ownerToken, snapshot)
	if denied.HTTPStatus != 401 || denied.Code != 401 {
		t.Fatal("user token accepted for device report", denied)
	}
	cleared := post(map[string]any{"profiles": map[string]any{"stream": map[string]any{}, "voip": map[string]any{}}})
	if cleared.Code != 200 {
		t.Fatal(cleared)
	}
	profiles = list(ownerToken)[0].Profiles
	if len(profiles["stream"]) != 0 || len(profiles["voip"]) != 0 || len(profiles["call"]) == 0 {
		t.Fatal("scene replacement/fallback semantics broken", profiles)
	}
	if _, err := s.sqlDB.Exec(`UPDATE device_bind SET user_id=0,unbind_time=NOW() WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	unbound := post(snapshot)
	if unbound.HTTPStatus != 410 || unbound.Code != 6006 {
		t.Fatal("unbound report accepted", unbound)
	}
	if len(list(ownerToken)) != 0 {
		t.Fatal("unbound device visible")
	}
	var audit struct {
		Created time.Time `db:"created_at"`
		Updated time.Time `db:"updated_at"`
	}
	if err := s.sqlDB.Get(&audit, `SELECT created_at,updated_at FROM device_profile WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	if audit.Created.IsZero() || audit.Updated.Before(audit.Created) {
		t.Fatal("invalid audit timestamps", audit)
	}
}
