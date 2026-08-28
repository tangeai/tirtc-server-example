package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	voipapiresp "thing-connect/voip-server/apiresp"
	voiphandler "thing-connect/voip-server/handler"
)

func TestVoipDeviceCallSeparatesJWTAndBusinessErrors(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)

	router := gin.New()
	voiphandler.NewServer(cfg, s.sqlDB, s.rdb, newFakeBroker()).Register(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	suffix := fmt.Sprintf("%017d", time.Now().UnixNano()%100000000000000000)
	deviceID := "V" + suffix
	wxAppID := "wx-call-error-test-" + suffix
	wxOpenID := "openid-call-error-test-" + suffix
	t.Cleanup(func() {
		_, _ = s.sqlDB.Exec(`DELETE FROM voip_device_auth WHERE device_id=?`, deviceID)
		_, _ = s.sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
	})

	body, err := json.Marshal(map[string]any{
		"device_id":       deviceID,
		"wx_app_id":       wxAppID,
		"wx_user_openid":  wxOpenID,
		"wx_room_type":    "voice",
		"wx_version_type": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token string) *apiResp {
		t.Helper()
		req, err := http.NewRequest(
			http.MethodPost,
			server.URL+"/v1/voip/device/call",
			bytes.NewReader(body),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return parseResp(t, resp)
	}

	jwtFailure := call("invalid.jwt.token")
	if jwtFailure.HTTPStatus != http.StatusUnauthorized ||
		jwtFailure.Code != voipapiresp.ErrAuth {
		t.Fatalf(
			"invalid JWT: HTTP=%d code=%d, want HTTP=401 code=401",
			jwtFailure.HTTPStatus,
			jwtFailure.Code,
		)
	}

	token := deviceJWT(t, cfg.JWTSecret, deviceID)
	unbound := call(token)
	if unbound.HTTPStatus != http.StatusOK ||
		unbound.Code != voipapiresp.ErrDeviceUnbound {
		t.Fatalf(
			"unbound device: HTTP=%d code=%d, want HTTP=200 code=%d",
			unbound.HTTPStatus,
			unbound.Code,
			voipapiresp.ErrDeviceUnbound,
		)
	}

	userID := time.Now().UnixNano()%1000000000 + 900000
	if _, err := s.sqlDB.Exec(
		`INSERT INTO device_bind
		    (device_id, mac, chip_uid, device_rand, user_id, last_user_id, bind_time)
		 VALUES (?, ?, '', '', ?, ?, ?)`,
		deviceID,
		uniqueMAC(),
		userID,
		userID,
		time.Now(),
	); err != nil {
		t.Fatalf("seed bound device: %v", err)
	}

	missingVoipAuth := call(token)
	if missingVoipAuth.HTTPStatus != http.StatusOK ||
		missingVoipAuth.Code != voipapiresp.ErrVoipAuthInvalid {
		t.Fatalf(
			"missing VoIP auth: HTTP=%d code=%d, want HTTP=200 code=%d",
			missingVoipAuth.HTTPStatus,
			missingVoipAuth.Code,
			voipapiresp.ErrVoipAuthInvalid,
		)
	}
}

func TestVoipContactRemarkIsGlobalAndLastWriteWins(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)

	router := gin.New()
	broker := newFakeBroker()
	voiphandler.NewServer(cfg, s.sqlDB, s.rdb, broker).Register(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	suffix := fmt.Sprintf("%017d", time.Now().UnixNano()%100000000000000000)
	device1 := "R" + suffix + "01"
	device2 := "R" + suffix + "02"
	device3 := "R" + suffix + "03"
	userID := time.Now().UnixNano()%1000000000 + 600000
	now := time.Now()
	const (
		wxAppID  = "wx-global-remark-test"
		wxOpenID = "openid-global-remark"
	)
	t.Cleanup(func() {
		_, _ = s.sqlDB.Exec(
			`DELETE FROM voip_device_auth WHERE device_id IN (?, ?, ?)`,
			device1, device2, device3,
		)
		_, _ = s.sqlDB.Exec(
			`DELETE FROM device_bind WHERE device_id IN (?, ?, ?)`,
			device1, device2, device3,
		)
		_, _ = s.sqlDB.Exec(
			`DELETE FROM voip_user_profile WHERE wx_open_id=? AND wx_app_id=?`,
			wxOpenID, wxAppID,
		)
		s.rdb.Del(context.Background(), fmt.Sprintf("voip:wx-login:%d:%s", userID, wxAppID))
	})

	for _, deviceID := range []string{device1, device2, device3} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO device_bind
			    (device_id, mac, chip_uid, device_rand, user_id, last_user_id, bind_time)
			 VALUES (?, ?, '', '', ?, ?, ?)`,
			deviceID, uniqueMAC(), userID, userID, now,
		); err != nil {
			t.Fatalf("seed device %s: %v", deviceID, err)
		}
	}
	for _, row := range []struct {
		deviceID string
		remark   string
	}{
		{device1, "设备一旧名称"},
		{device2, "设备二旧名称"},
	} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO voip_device_auth
			    (device_id, wx_open_id, wx_app_id, wx_model_id, remark, created_at)
			 VALUES (?, ?, ?, 'model-test', ?, ?)`,
			row.deviceID, wxOpenID, wxAppID, row.remark, now,
		); err != nil {
			t.Fatalf("seed auth %s: %v", row.deviceID, err)
		}
	}

	loginKey := fmt.Sprintf("voip:wx-login:%d:%s", userID, wxAppID)
	if err := s.rdb.Set(context.Background(), loginKey, wxOpenID, time.Minute).Err(); err != nil {
		t.Fatalf("seed wechat login: %v", err)
	}
	token := userJWT(t, cfg.JWTSecret, userID)
	requestJSON := func(method, path string, body any) *apiResp {
		t.Helper()
		var requestBody *bytes.Reader
		if body == nil {
			requestBody = bytes.NewReader(nil)
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			requestBody = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(method, server.URL+path, requestBody)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return parseResp(t, resp)
	}

	updated := requestJSON(http.MethodPut, "/v1/voip/user/contact-remark", map[string]any{
		"wx_app_id": wxAppID,
		"remark":    "右上角统一名称",
	})
	if updated.Code != 0 {
		t.Fatalf("PUT contact-remark: code=%d msg=%s", updated.Code, updated.Msg)
	}

	assertRemarks := func(want string, deviceIDs ...string) {
		t.Helper()
		for _, deviceID := range deviceIDs {
			var got string
			if err := s.sqlDB.Get(&got,
				`SELECT remark FROM voip_device_auth
				  WHERE device_id=? AND wx_open_id=? AND wx_app_id=?`,
				deviceID, wxOpenID, wxAppID); err != nil || got != want {
				t.Fatalf("device %s remark=%q err=%v, want %q", deviceID, got, err, want)
			}
		}
	}
	assertRemarks("右上角统一名称", device1, device2)

	gotRemark := requestJSON(
		http.MethodGet,
		"/v1/voip/user/contact-remark?wx_app_id="+wxAppID,
		nil,
	)
	if gotRemark.Code != 0 {
		t.Fatalf("GET contact-remark: code=%d msg=%s", gotRemark.Code, gotRemark.Msg)
	}
	var remarkData struct {
		WxOpenID string `json:"wx_open_id"`
		Remark   string `json:"remark"`
	}
	if err := json.Unmarshal(gotRemark.Data, &remarkData); err != nil {
		t.Fatal(err)
	}
	if remarkData.WxOpenID != wxOpenID || remarkData.Remark != "右上角统一名称" {
		t.Fatalf("contact-remark=%+v", remarkData)
	}

	reported := requestJSON(http.MethodPost, "/v1/voip/user/report-auth", map[string]any{
		"device_id":   device1,
		"wx_app_id":   wxAppID,
		"wx_model_id": "model-test",
		"wx_open_id":  wxOpenID,
		"remark":      "小程序最后写入",
	})
	if reported.Code != 0 {
		t.Fatalf("report-auth global write: code=%d msg=%s", reported.Code, reported.Msg)
	}
	assertRemarks("小程序最后写入", device1, device2)

	broker.mu.Lock()
	publishedBeforeRepeat := len(broker.published)
	broker.mu.Unlock()
	repeated := requestJSON(http.MethodPost, "/v1/voip/user/report-auth", map[string]any{
		"device_id":   device1,
		"wx_app_id":   wxAppID,
		"wx_model_id": "model-test",
		"wx_open_id":  wxOpenID,
		"remark":      "小程序最后写入",
	})
	if repeated.Code != 0 {
		t.Fatalf("repeat report-auth: code=%d msg=%s", repeated.Code, repeated.Msg)
	}
	broker.mu.Lock()
	publishedAfterRepeat := len(broker.published)
	broker.mu.Unlock()
	if publishedAfterRepeat != publishedBeforeRepeat {
		t.Fatalf("no-op report-auth published callers_update: before=%d after=%d",
			publishedBeforeRepeat, publishedAfterRepeat)
	}

	inherited := requestJSON(http.MethodPost, "/v1/voip/user/report-auth", map[string]any{
		"device_id":   device3,
		"wx_app_id":   wxAppID,
		"wx_model_id": "model-test",
		"wx_open_id":  wxOpenID,
	})
	if inherited.Code != 0 {
		t.Fatalf("report-auth inherit: code=%d msg=%s", inherited.Code, inherited.Msg)
	}
	assertRemarks("小程序最后写入", device1, device2, device3)
}

func TestVoipReleaseOutgoingCallGuards(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)
	server := voiphandler.NewServer(cfg, s.sqlDB, s.rdb, nil)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	deviceID := "guard-device-" + suffix
	wxAppID := "guard-app-" + suffix
	wxOpenID := "guard-openid-" + suffix
	deviceKey := "voip:device-call:" + deviceID
	contactKey := "voip:contact-call:" + wxAppID + ":" + wxOpenID
	ctx := context.Background()
	t.Cleanup(func() {
		s.rdb.Del(ctx, deviceKey, contactKey)
	})

	if err := s.rdb.Set(ctx, deviceKey, "call-id", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.rdb.Set(ctx, contactKey, "call-id", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	server.ReleaseOutgoingCallGuards(ctx, wxAppID, deviceID, wxOpenID, "call-id")

	if remaining, err := s.rdb.Exists(ctx, deviceKey, contactKey).Result(); err != nil {
		t.Fatal(err)
	} else if remaining != 0 {
		t.Fatalf("outgoing call guards not released: remaining=%d", remaining)
	}

	if err := s.rdb.Set(ctx, deviceKey, "newer-call-id", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.rdb.Set(ctx, contactKey, "newer-call-id", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	server.ReleaseOutgoingCallGuards(ctx, wxAppID, deviceID, wxOpenID, "old-call-id")
	if remaining, err := s.rdb.Exists(ctx, deviceKey, contactKey).Result(); err != nil {
		t.Fatal(err)
	} else if remaining != 2 {
		t.Fatalf("stale callback deleted newer call guards: remaining=%d", remaining)
	}
}

func TestVoipNotificationDedupeTracksProcessingAndCompletion(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)
	server := voiphandler.NewServer(cfg, s.sqlDB, s.rdb, nil)
	ctx := context.Background()
	wxAppID := "dedupe-app"
	roomID := fmt.Sprintf("dedupe-room-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		server.ReleaseVoipNotification(ctx, wxAppID, roomID)
	})

	acquired, err := server.AcquireVoipNotification(ctx, wxAppID, roomID)
	if err != nil || !acquired {
		t.Fatalf("first acquire: acquired=%v err=%v", acquired, err)
	}
	if complete, err := server.IsVoipNotificationComplete(ctx, wxAppID, roomID); err != nil {
		t.Fatal(err)
	} else if complete {
		t.Fatal("processing notification must not be reported complete")
	}

	acquired, err = server.AcquireVoipNotification(ctx, wxAppID, roomID)
	if err != nil || acquired {
		t.Fatalf("processing duplicate: acquired=%v err=%v", acquired, err)
	}
	if err := server.CompleteVoipNotification(ctx, wxAppID, roomID); err != nil {
		t.Fatal(err)
	}
	if complete, err := server.IsVoipNotificationComplete(ctx, wxAppID, roomID); err != nil {
		t.Fatal(err)
	} else if !complete {
		t.Fatal("completed notification was not reported complete")
	}
}

func TestVoipUserAuthListReturnsCurrentWechatRemarksForOwnedDevices(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)

	router := gin.New()
	voiphandler.NewServer(cfg, s.sqlDB, s.rdb, nil).Register(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	suffix := fmt.Sprintf("%017d", time.Now().UnixNano()%100000000000000000)
	device1 := "V" + suffix + "01"
	device2 := "V" + suffix + "02"
	otherDevice := "V" + suffix + "03"
	userID := time.Now().UnixNano()%1000000000 + 200000
	otherUserID := userID + 1
	now := time.Now()
	t.Cleanup(func() {
		if _, err := s.sqlDB.Exec(
			`DELETE FROM voip_device_auth WHERE device_id IN (?, ?, ?)`,
			device1, device2, otherDevice,
		); err != nil {
			t.Errorf("cleanup auth rows: %v", err)
		}
		if _, err := s.sqlDB.Exec(
			`DELETE FROM device_bind WHERE device_id IN (?, ?, ?)`,
			device1, device2, otherDevice,
		); err != nil {
			t.Errorf("cleanup device rows: %v", err)
		}
	})

	for _, row := range []struct {
		deviceID string
		userID   int64
	}{
		{device1, userID},
		{device2, userID},
		{otherDevice, otherUserID},
	} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO device_bind
			    (device_id, mac, chip_uid, device_rand, user_id, last_user_id, bind_time)
			 VALUES (?, ?, '', '', ?, ?, ?)`,
			row.deviceID, uniqueMAC(), row.userID, row.userID, now,
		); err != nil {
			t.Fatalf("seed device %s: %v", row.deviceID, err)
		}
	}

	wxAppID := "wx-auth-list-" + suffix
	wxOpenID := "openid-current-" + suffix
	otherOpenID := "openid-other-" + suffix
	otherAppID := "wx-other-app-" + suffix
	t.Cleanup(func() {
		_, _ = s.sqlDB.Exec(
			`DELETE FROM voip_user_profile
			  WHERE (wx_open_id=? AND wx_app_id=?)
			     OR (wx_open_id=? AND wx_app_id=?)
			     OR (wx_open_id=? AND wx_app_id=?)`,
			wxOpenID, wxAppID,
			otherOpenID, wxAppID,
			wxOpenID, otherAppID,
		)
	})
	if _, err := s.sqlDB.Exec(
		`INSERT INTO voip_user_profile (wx_open_id, wx_app_id, remark)
		 VALUES (?, ?, '统一名称')`,
		wxOpenID, wxAppID); err != nil {
		t.Fatalf("seed global profile: %v", err)
	}
	for _, row := range []struct {
		deviceID string
		openID   string
		appID    string
		remark   string
	}{
		{device1, wxOpenID, wxAppID, "妈妈"},
		{device2, wxOpenID, wxAppID, "爸爸"},
		{otherDevice, wxOpenID, wxAppID, "不应返回的其他用户设备"},
		{device1, otherOpenID, wxAppID, "不应返回的其他微信"},
		{device1, wxOpenID, otherAppID, "不应返回的其他小程序"},
	} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO voip_device_auth
			    (device_id, wx_open_id, wx_app_id, wx_model_id, remark, created_at)
			 VALUES (?, ?, ?, '', ?, ?)`,
			row.deviceID, row.openID, row.appID, row.remark, now,
		); err != nil {
			t.Fatalf("seed auth %s/%s/%s: %v", row.deviceID, row.openID, row.appID, err)
		}
	}

	loginKey := fmt.Sprintf("voip:wx-login:%d:%s", userID, wxAppID)
	if err := s.rdb.Set(context.Background(), loginKey, wxOpenID, time.Minute).Err(); err != nil {
		t.Fatalf("seed wechat login: %v", err)
	}
	t.Cleanup(func() {
		s.rdb.Del(context.Background(), loginKey)
	})

	req, _ := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/voip/user/auth-list?wx_app_id="+wxAppID,
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+userJWT(t, cfg.JWTSecret, userID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET auth-list: %v", err)
	}
	result := parseResp(t, resp)
	if result.Code != 0 {
		t.Fatalf("auth-list: code=%d msg=%s", result.Code, result.Msg)
	}

	var data struct {
		List []struct {
			DeviceID string `json:"device_id"`
			Remark   string `json:"remark"`
		} `json:"list"`
	}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		t.Fatalf("decode auth-list: %v", err)
	}
	got := make(map[string]string, len(data.List))
	for _, item := range data.List {
		got[item.DeviceID] = item.Remark
	}
	if len(got) != 2 || got[device1] != "统一名称" || got[device2] != "统一名称" {
		t.Fatalf("auth-list=%v, want only current user's two authorizations with global name", got)
	}

	req, _ = http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/voip/user/auth-list?wx_app_id="+wxAppID,
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+userJWT(t, cfg.JWTSecret, otherUserID))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET auth-list without wechat login: %v", err)
	}
	result = parseResp(t, resp)
	if result.Code != voipapiresp.ErrWechatLoginInvalid {
		t.Fatalf(
			"auth-list without wechat login: code=%d, want %d",
			result.Code,
			voipapiresp.ErrWechatLoginInvalid,
		)
	}
}

func TestVoipContactsKeepsDeviceAndH5RoutesSeparate(t *testing.T) {
	s := newSuite(t)
	cfg := loadConfig(t)

	router := gin.New()
	voiphandler.NewServer(cfg, s.sqlDB, s.rdb, nil).Register(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	suffix := fmt.Sprintf("%017d", time.Now().UnixNano()%100000000000000000)
	deviceID := "C" + suffix + "01"
	otherDeviceID := "C" + suffix + "02"
	userID := time.Now().UnixNano()%1000000000 + 400000
	otherUserID := userID + 1
	now := time.Now()
	t.Cleanup(func() {
		if _, err := s.sqlDB.Exec(
			`DELETE FROM voip_device_auth WHERE device_id IN (?, ?)`,
			deviceID, otherDeviceID,
		); err != nil {
			t.Errorf("cleanup auth rows: %v", err)
		}
		if _, err := s.sqlDB.Exec(
			`DELETE FROM device_bind WHERE device_id IN (?, ?)`,
			deviceID, otherDeviceID,
		); err != nil {
			t.Errorf("cleanup device rows: %v", err)
		}
	})

	for _, row := range []struct {
		deviceID string
		userID   int64
	}{
		{deviceID, userID},
		{otherDeviceID, otherUserID},
	} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO device_bind
			    (device_id, mac, chip_uid, device_rand, user_id, last_user_id, bind_time)
			 VALUES (?, ?, '', '', ?, ?, ?)`,
			row.deviceID, uniqueMAC(), row.userID, row.userID, now,
		); err != nil {
			t.Fatalf("seed device %s: %v", row.deviceID, err)
		}
	}

	for _, row := range []struct {
		deviceID string
		openID   string
		remark   string
	}{
		{deviceID, "openid-b", "小程序B"},
		{deviceID, "openid-c", "小程序C"},
		{otherDeviceID, "openid-other", "其他设备联系人"},
	} {
		if _, err := s.sqlDB.Exec(
			`INSERT INTO voip_device_auth
			    (device_id, wx_open_id, wx_app_id, wx_model_id, remark, created_at)
			 VALUES (?, ?, 'wx-contacts-test', 'model-test', ?, ?)`,
			row.deviceID, row.openID, row.remark, now,
		); err != nil {
			t.Fatalf("seed auth %s/%s: %v", row.deviceID, row.openID, err)
		}
	}

	get := func(path, token string) *apiResp {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return parseResp(t, resp)
	}
	decodeContacts := func(result *apiResp, field string) []struct {
		WxOpenID string `json:"wx_open_id"`
		Remark   string `json:"remark"`
	} {
		t.Helper()
		var data map[string]json.RawMessage
		if err := json.Unmarshal(result.Data, &data); err != nil {
			t.Fatalf("decode contacts data: %v", err)
		}
		var contacts []struct {
			WxOpenID string `json:"wx_open_id"`
			Remark   string `json:"remark"`
		}
		if err := json.Unmarshal(data[field], &contacts); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
		return contacts
	}

	deviceResult := get(
		"/v1/voip/device/contacts",
		deviceJWT(t, cfg.JWTSecret, deviceID),
	)
	if deviceResult.Code != 0 {
		t.Fatalf("device contacts: code=%d msg=%s", deviceResult.Code, deviceResult.Msg)
	}
	if contacts := decodeContacts(deviceResult, "contacts"); len(contacts) != 2 {
		t.Fatalf("device contacts=%v, want two VoIP contacts", contacts)
	}

	h5Result := get(
		"/v1/voip/user/contacts?device_id="+deviceID,
		userJWT(t, cfg.JWTSecret, userID),
	)
	if h5Result.Code != 0 {
		t.Fatalf("H5 contacts: code=%d msg=%s", h5Result.Code, h5Result.Msg)
	}
	if contacts := decodeContacts(h5Result, "contacts"); len(contacts) != 2 {
		t.Fatalf("H5 contacts=%v, want two VoIP contacts", contacts)
	}

	denied := get(
		"/v1/voip/user/contacts?device_id="+deviceID,
		userJWT(t, cfg.JWTSecret, otherUserID),
	)
	if denied.Code != voipapiresp.ErrForbidden {
		t.Fatalf(
			"other user contacts: code=%d, want %d",
			denied.Code,
			voipapiresp.ErrForbidden,
		)
	}

	legacy := get(
		"/v1/voip/device/callers",
		deviceJWT(t, cfg.JWTSecret, deviceID),
	)
	if legacy.Code != 0 {
		t.Fatalf("legacy callers: code=%d msg=%s", legacy.Code, legacy.Msg)
	}
	if callers := decodeContacts(legacy, "list"); len(callers) != 2 {
		t.Fatalf("legacy callers=%v, want two VoIP contacts", callers)
	}
}
