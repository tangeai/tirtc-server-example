package admin

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"thing-connect/internal/db"
	"thing-connect/internal/testenv"
)

func TestDeviceImportResultContainsEveryInputRow(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "jobs")
	service, err := NewJobService(sqlDB, root, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, err := RandomToken(18)
	if err != nil {
		t.Fatal(err)
	}
	inputRelative := filepath.ToSlash(filepath.Join("imports", "all-rows.csv"))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(inputRelative)), []byte("device_id,device_key\n"+deviceID+",device-key\n,missing-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := sqlDB.Exec(`INSERT INTO admin_jobs (job_type,status,source_name,input_file,worker_id,lease_until,created_by) VALUES ('device_pool_import',1,'all-rows.csv',?,?,NOW()+INTERVAL 5 MINUTE,0)`, inputRelative, service.workerID)
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := result.LastInsertId()
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_job_items WHERE job_id=?`, jobID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_jobs WHERE id=?`, jobID)
		_, _ = sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, deviceID)
	})

	service.runDeviceImport(context.Background(), AdminJob{ID: jobID, InputFile: inputRelative})
	resultFile, err := os.Open(filepath.Join(root, "results", fmt.Sprintf("%d.csv", jobID)))
	if err != nil {
		t.Fatal(err)
	}
	defer resultFile.Close()
	records, err := csv.NewReader(resultFile).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1][2] != "成功" || records[2][2] != "失败" {
		t.Fatalf("result CSV must contain header and every input row: %#v", records)
	}
}

func TestDeviceImportRowIsPersistedWithItsJobMarker(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatal(err)
	}
	service, err := NewJobService(sqlDB, filepath.Join(t.TempDir(), "jobs"), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, err := RandomToken(18)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sqlDB.Exec(`INSERT INTO admin_jobs (job_type,status,source_name,input_file,created_by) VALUES ('device_pool_import',1,'test.csv','imports/test.csv',0)`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := result.LastInsertId()
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_job_items WHERE job_id=?`, jobID)
		_, _ = sqlDB.Exec(`DELETE FROM admin_jobs WHERE id=?`, jobID)
		_, _ = sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, deviceID)
	})

	status, message, err := service.persistImportRow(context.Background(), jobID, 2, deviceID, "test-device-key", "")
	if err != nil || status != 1 || message != "" {
		t.Fatalf("persistImportRow status=%d message=%q err=%v", status, message, err)
	}
	var poolCount, markerCount int
	if err := sqlDB.Get(&poolCount, `SELECT COUNT(*) FROM device_pool WHERE device_id=?`, deviceID); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Get(&markerCount, `SELECT COUNT(*) FROM admin_job_items WHERE job_id=? AND row_no=2 AND status=1 AND resource_id=?`, jobID, deviceID); err != nil {
		t.Fatal(err)
	}
	if poolCount != 1 || markerCount != 1 {
		t.Fatalf("pool=%d marker=%d", poolCount, markerCount)
	}
}

func TestRecoverExpiredJobsLeavesActiveLeasesUntouched(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatal(err)
	}
	insert := func(offset time.Duration) int64 {
		result, err := sqlDB.Exec(`INSERT INTO admin_jobs (job_type,status,source_name,input_file,worker_id,lease_until,created_by) VALUES ('device_pool_import',1,'test.csv','imports/test.csv','worker',DATE_ADD(NOW(), INTERVAL ? SECOND),0)`, int(offset/time.Second))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	expiredID := insert(-time.Minute)
	activeID := insert(time.Minute)
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_jobs WHERE id IN (?,?)`, expiredID, activeID)
	})
	if err := recoverExpiredJobs(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	var expiredStatus, activeStatus int
	_ = sqlDB.Get(&expiredStatus, `SELECT status FROM admin_jobs WHERE id=?`, expiredID)
	_ = sqlDB.Get(&activeStatus, `SELECT status FROM admin_jobs WHERE id=?`, activeID)
	if expiredStatus != JobPending || activeStatus != JobRunning {
		t.Fatalf("expired=%d active=%d", expiredStatus, activeStatus)
	}
}

func TestClaimLeaseUsesDatabaseClockAcrossTimezones(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	dsn, err := mysqldriver.ParseDSN(cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn.Loc = time.UTC
	if dsn.Params == nil {
		dsn.Params = make(map[string]string)
	}
	dsn.Params["time_zone"] = "'+08:00'"
	databaseConfig := cfg.Database
	databaseConfig.DSN = dsn.FormatDSN()
	sqlDB := testenv.OpenDatabaseOrSkip(t, databaseConfig)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM admin_jobs WHERE created_by=0 AND source_name IN ('test.csv','timezone.csv')`); err != nil {
		t.Fatal(err)
	}
	service, err := NewJobService(sqlDB, filepath.Join(t.TempDir(), "jobs"), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sqlDB.Exec(`INSERT INTO admin_jobs (job_type,status,source_name,input_file,created_by) VALUES ('device_pool_import',0,'timezone.csv','imports/timezone.csv',0)`)
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := result.LastInsertId()
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_jobs WHERE id=?`, jobID)
	})
	job, err := service.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != jobID {
		t.Fatalf("claimed job=%+v, want id=%d", job, jobID)
	}
	if err := recoverExpiredJobs(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	var status int
	if err := sqlDB.Get(&status, `SELECT status FROM admin_jobs WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if status != JobRunning {
		t.Fatalf("freshly claimed job status=%d, want running=%d", status, JobRunning)
	}
}
