package mysql_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/service"
	mysqlstore "thing-connect/internal/store/mysql"
)

func TestDeviceMediaReportsPersistAndIsolateScenes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := uniqueDevID()
	_, err := db.Exec(`INSERT INTO device_bind(device_id,user_id) VALUES(?,123)`, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM device_profile WHERE device_id=?`, id) })
	svc := service.NewDeviceMediaService(mysqlstore.NewDeviceMediaStore(db))
	report := func(scene, raw string) error {
		return svc.Report(ctx, id, map[string]json.RawMessage{scene: json.RawMessage(raw)})
	}
	if err := report("stream", `{"camera_rotation":90,"hor_mirror":true}`); err != nil {
		t.Fatal(err)
	}
	var created time.Time
	if err = db.Get(&created, `SELECT created_at FROM device_profile WHERE device_id=?`, id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for scene, raw := range map[string]string{"stream": `{"camera_rotation":0,"hor_mirror":false}`, "call": `{"down_audio_mt":["opus","amr"]}`} {
		wg.Add(1)
		go func(scene, raw string) { defer wg.Done(); failures <- report(scene, raw) }(scene, raw)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := mysqlstore.NewUserStore(db).GetDeviceList(ctx, 123)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MediaProfiles == nil {
		t.Fatalf("rows=%+v", rows)
	}
	var profiles map[string]map[string]json.RawMessage
	if err = json.Unmarshal([]byte(*rows[0].MediaProfiles), &profiles); err != nil {
		t.Fatal(err)
	}
	if string(profiles["stream"]["camera_rotation"]) != "0" || string(profiles["stream"]["hor_mirror"]) != "false" {
		t.Fatal(profiles)
	}
	var formats []string
	_ = json.Unmarshal(profiles["call"]["down_audio_mt"], &formats)
	if len(formats) != 2 || formats[0] != "opus" || formats[1] != "amr" {
		t.Fatal(formats)
	}
	if err = report("stream", `{}`); err != nil {
		t.Fatal(err)
	}
	if err = report("stream", `{}`); err != nil {
		t.Fatal("identical retry failed", err)
	}
	var audit struct {
		Created time.Time `db:"created_at"`
		Updated time.Time `db:"updated_at"`
		Profile string    `db:"profile"`
	}
	if err = db.Get(&audit, `SELECT created_at,updated_at,profile FROM device_profile WHERE device_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if !audit.Created.Equal(created) || !audit.Updated.After(created) {
		t.Fatalf("audit=%+v created=%v", audit, created)
	}
	_ = json.Unmarshal([]byte(audit.Profile), &profiles)
	if len(profiles["stream"]) != 0 || len(profiles["call"]) == 0 {
		t.Fatal("scene replacement lost other scene", profiles)
	}
	_, err = db.Exec(`UPDATE device_bind SET user_id=0 WHERE device_id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = report("stream", `{"audio_rate":8000}`); !errors.Is(err, service.ErrDeviceReset) {
		t.Fatal(err)
	}
	var after string
	_ = db.Get(&after, `SELECT profile FROM device_profile WHERE device_id=?`, id)
	if after != audit.Profile {
		t.Fatal("unbound report mutated profile")
	}
	if err = svc.Report(ctx, "missing", map[string]json.RawMessage{"call": json.RawMessage(`{}`)}); !errors.Is(err, service.ErrDeviceReset) {
		t.Fatal(err)
	}
}

func TestDeviceProfileCancelledAndUnboundTransactionsDoNotWrite(t *testing.T) {
	db := openTestDB(t)
	id := uniqueDevID()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id) VALUES(?,123)`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM device_profile WHERE device_id=?`, id) })
	svc := service.NewDeviceMediaService(mysqlstore.NewDeviceMediaStore(db))
	input := map[string]json.RawMessage{"call": json.RawMessage(`{"audio_rate":8000}`)}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var owner int64
	if err := tx.Get(&owner, `SELECT user_id FROM device_bind WHERE device_id=? FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err = svc.Report(timeout, id, input)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked report did not respect context: %v", err)
	}
	if _, err := tx.Exec(`UPDATE device_bind SET user_id=0 WHERE device_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Report(ctx, id, input); !errors.Is(err, service.ErrDeviceReset) {
		t.Fatal("unbind was not observed", err)
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM device_profile WHERE device_id=?`, id); err != nil || count != 0 {
		t.Fatalf("cancelled/unbound report wrote %d rows: %v", count, err)
	}
}
