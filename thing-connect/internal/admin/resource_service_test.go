package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type readStore struct {
	ResourceReadStore
	query  ResourceQuery
	device ManagedDevice
	err    error
}

func (s *readStore) Users(_ context.Context, q ResourceQuery) ([]ManagedUser, int, error) {
	s.query = q
	return []ManagedUser{{ID: 7, RegistrationIP: "192.0.2.1"}}, 1, s.err
}
func (s *readStore) Device(context.Context, string) (ManagedDevice, error) { return s.device, s.err }

type resourcePresence struct {
	fail bool
	ids  []string
}

func (p *resourcePresence) Read(_ context.Context, ids []string) (map[string]DevicePresence, error) {
	p.ids = ids
	if p.fail {
		return nil, errors.New("redis unavailable")
	}
	at := time.Now()
	return map[string]DevicePresence{"d1": {Online: true, LastSeenAt: &at}}, nil
}
func TestResourceServiceValidatesAndBoundsQueries(t *testing.T) {
	store := &readStore{}
	svc := NewResourceService(store, nil)
	result, err := svc.Users(context.Background(), ResourceFilter{Page: -1, PageSize: 999, Keyword: " test ", CreatedFrom: "2026-01-01", CreatedTo: "2026-01-02", SortOrder: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Page != 1 || result.PageSize != 100 || store.query.Keyword != "test" || store.query.Desc || store.query.Until.Day() != 3 {
		t.Fatalf("query=%+v", store.query)
	}
	for _, f := range []ResourceFilter{{SortBy: "id DESC; DROP TABLE users"}, {SortOrder: "bad"}, {CreatedFrom: "bad"}} {
		if _, err := svc.Users(context.Background(), f); !errors.Is(err, ErrInvalidResourceQuery) {
			t.Fatalf("invalid query accepted: %v", err)
		}
	}
}
func TestResourceServicePreservesUnknownPresenceAndFiltersProfiles(t *testing.T) {
	profile := `{"stream":{"audio_rate":16000},"credentials":{"token":"secret"},"hardware":{"chip_model":"arm64"}}`
	store := &readStore{device: ManagedDevice{DeviceID: "d1", Profile: &profile, LastOnlineIP: "192.0.2.1"}}
	reader := &resourcePresence{fail: true}
	svc := NewResourceService(store, reader)
	row, err := svc.Device(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	if row.PresenceKnown || row.Online {
		t.Fatal("Redis failure treated as authoritative offline")
	}
	raw, _ := json.Marshal(row)
	if strings.Contains(string(raw), "secret") || row.Hardware == nil {
		t.Fatal("profile not projected safely")
	}
	reader.fail = false
	row, err = svc.Device(context.Background(), "d1")
	if err != nil || !row.PresenceKnown || !row.Online || row.LastSeenAt == nil {
		t.Fatal("presence not enriched")
	}
	store.err = ErrNotFound
	if _, err = svc.Device(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestDeviceHTTPUsesResourceModule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := &readStore{device: ManagedDevice{DeviceID: "d1", LastOnlineIP: "2001:db8::1"}}
	server := &HTTPServer{resources: NewResourceService(store, nil)}
	r.GET("/devices/:device_id", server.getDevice)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/devices/d1", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"last_online_ip":"2001:db8::1"`) {
		t.Fatal(response.Body.String())
	}
	store.err = ErrNotFound
	response = httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/devices/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal(response.Code)
	}
}

func TestIPFieldsHTTPSerialization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	at := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known_%v", known), func(t *testing.T) {
			user := ManagedUser{ID: 7}
			device := ManagedDevice{DeviceID: "d1"}
			if known {
				user.RegistrationIP = "192.0.2.1"
				user.LastLoginIP = "2001:db8::1"
				user.LastLoginAt = &at
				device.LastOnlineIP = "2001:db8::2"
				device.LastOnlineAt = &at
			}
			store := &ipHTTPStore{user: user, device: device}
			server := &HTTPServer{resources: NewResourceService(store, nil)}
			r := gin.New()
			r.GET("/users", server.listUsers)
			r.GET("/users/:id", server.getUser)
			r.GET("/devices", server.listDevices)
			r.GET("/devices/:device_id", server.getDevice)
			for _, path := range []string{"/users", "/users/7", "/devices", "/devices/d1"} {
				response := httptest.NewRecorder()
				r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s: %s", path, response.Body.String())
				}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				var object map[string]json.RawMessage
				if err := json.Unmarshal(envelope.Data, &object); err != nil {
					t.Fatal(err)
				}
				if path == "/users" || path == "/devices" {
					var items []map[string]json.RawMessage
					if err := json.Unmarshal(object["items"], &items); err != nil || len(items) != 1 {
						t.Fatalf("bad items %s", response.Body.String())
					}
					object = items[0]
				} else if path == "/users/7" {
					var nested map[string]json.RawMessage
					if err := json.Unmarshal(object["user"], &nested); err != nil {
						t.Fatal(err)
					}
					object = nested
				}
				ips := map[string]string{"last_online_ip": device.LastOnlineIP}
				timestamp := "last_online_at"
				if strings.HasPrefix(path, "/users") {
					ips = map[string]string{"registration_ip": user.RegistrationIP, "last_login_ip": user.LastLoginIP}
					timestamp = "last_login_at"
				}
				for field, want := range ips {
					var got string
					if err := json.Unmarshal(object[field], &got); err != nil || got != want {
						t.Fatalf("%s %s=%s want %s", path, field, object[field], want)
					}
				}
				raw, exists := object[timestamp]
				if exists != known {
					t.Fatalf("%s unknown timestamp must be omitted: %s", path, raw)
				}
				if known {
					var got time.Time
					if err := json.Unmarshal(raw, &got); err != nil || !got.Equal(at) {
						t.Fatalf("incorrect time: %s", raw)
					}
				}
			}
		})
	}
}

type ipHTTPStore struct {
	user   ManagedUser
	device ManagedDevice
}

func (s *ipHTTPStore) Users(context.Context, ResourceQuery) ([]ManagedUser, int, error) {
	return []ManagedUser{s.user}, 1, nil
}
func (s *ipHTTPStore) User(context.Context, int64) (UserDetail, error) {
	return UserDetail{User: s.user}, nil
}
func (s *ipHTTPStore) Devices(context.Context, ResourceQuery) ([]ManagedDevice, int, error) {
	return []ManagedDevice{s.device}, 1, nil
}
func (s *ipHTTPStore) Device(context.Context, string) (ManagedDevice, error) { return s.device, nil }
