package admin

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

const (
	JobPending = iota
	JobRunning
	JobSucceeded
	JobPartial
	JobFailed
	maxDeviceImportRows = 100000
	jobLeaseDuration    = 2 * time.Minute
)

const adminJobColumns = `id,job_type,status,source_name,input_file,result_file,total_count,succeeded_count,failed_count,attempts,next_attempt_at,started_at,worker_id,lease_until,finished_at,last_error,created_by,created_at,updated_at`

type AdminJob struct {
	ID              int64      `db:"id" json:"id"`
	JobType         string     `db:"job_type" json:"job_type"`
	Status          int8       `db:"status" json:"status"`
	SourceName      string     `db:"source_name" json:"source_name"`
	InputFile       string     `db:"input_file" json:"-"`
	ResultFile      string     `db:"result_file" json:"-"`
	ResultAvailable bool       `db:"-" json:"result_available"`
	TotalCount      int        `db:"total_count" json:"total_count"`
	SucceededCount  int        `db:"succeeded_count" json:"succeeded_count"`
	FailedCount     int        `db:"failed_count" json:"failed_count"`
	Attempts        int        `db:"attempts" json:"attempts"`
	NextAttemptAt   time.Time  `db:"next_attempt_at" json:"next_attempt_at"`
	StartedAt       *time.Time `db:"started_at" json:"started_at,omitempty"`
	WorkerID        string     `db:"worker_id" json:"-"`
	LeaseUntil      *time.Time `db:"lease_until" json:"-"`
	FinishedAt      *time.Time `db:"finished_at" json:"finished_at,omitempty"`
	LastError       string     `db:"last_error" json:"last_error"`
	CreatedBy       int64      `db:"created_by" json:"created_by"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
}

type JobService struct {
	db       *sqlx.DB
	root     string
	maxBytes int64
	workerID string
}

func NewJobService(db *sqlx.DB, root string, maxBytes int64) (*JobService, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "imports"), 0o700); err != nil {
		return nil, fmt.Errorf("create job storage: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absolute, "results"), 0o700); err != nil {
		return nil, fmt.Errorf("create job result storage: %w", err)
	}
	workerID, err := RandomToken(18)
	if err != nil {
		return nil, fmt.Errorf("create job worker id: %w", err)
	}
	return &JobService{db: db, root: absolute, maxBytes: maxBytes, workerID: workerID}, nil
}

func (s *JobService) CreateDevicePoolImport(ctx context.Context, header *multipart.FileHeader, createdBy int64) (int64, error) {
	if header == nil || header.Size <= 0 || header.Size > s.maxBytes {
		return 0, errors.New("CSV 文件为空或超过上传大小限制")
	}
	source, err := header.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = source.Close() }()
	token, err := RandomToken(18)
	if err != nil {
		return 0, err
	}
	relative := filepath.Join("imports", token+".csv")
	destination, err := os.OpenFile(filepath.Join(s.root, relative), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, s.maxBytes+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil || written > s.maxBytes {
		_ = os.Remove(filepath.Join(s.root, relative))
		return 0, errors.New("保存导入文件失败，请检查文件大小和存储空间")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO admin_jobs (job_type,status,source_name,input_file,created_by) VALUES ('device_pool_import',0,?,?,?)`, filepath.Base(header.Filename), filepath.ToSlash(relative), createdBy)
	if err != nil {
		_ = os.Remove(filepath.Join(s.root, relative))
		return 0, err
	}
	return result.LastInsertId()
}

func (s *JobService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if job, err := s.claim(ctx); err == nil && job != nil {
			s.runDeviceImport(ctx, *job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *JobService) claim(ctx context.Context) (*AdminJob, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := recoverExpiredJobs(ctx, tx); err != nil {
		return nil, err
	}
	var job AdminJob
	err = tx.GetContext(ctx, &job, `SELECT `+adminJobColumns+` FROM admin_jobs WHERE status=0 AND next_attempt_at<=NOW() ORDER BY id LIMIT 1 FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_jobs SET status=1,attempts=attempts+1,started_at=NOW(),worker_id=?,lease_until=DATE_ADD(NOW(), INTERVAL ? SECOND),finished_at=NULL,last_error='' WHERE id=?`, s.workerID, int(jobLeaseDuration/time.Second), job.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	job.WorkerID = s.workerID
	return &job, nil
}

func recoverExpiredJobs(ctx context.Context, executor sqlx.ExtContext) error {
	_, err := executor.ExecContext(ctx, `UPDATE admin_jobs SET status=0,worker_id='',lease_until=NULL,next_attempt_at=NOW(),last_error='任务执行实例失联，已自动重新排队' WHERE status=1 AND (lease_until IS NULL OR lease_until<NOW())`)
	return err
}

func (s *JobService) runDeviceImport(ctx context.Context, job AdminJob) {
	stopHeartbeat := s.startHeartbeat(ctx, job.ID)
	defer stopHeartbeat()
	input, err := s.safePath(job.InputFile, "imports")
	if err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	file, err := os.Open(input)
	if err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	defer func() { _ = file.Close() }()
	if err := validateDeviceImportFile(file, s.maxBytes); err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	reader := csv.NewReader(io.LimitReader(file, s.maxBytes+1))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil || len(header) < 2 || strings.TrimSpace(strings.ToLower(strings.TrimPrefix(header[0], "\ufeff"))) != "device_id" || strings.TrimSpace(strings.ToLower(header[1])) != "device_key" {
		s.failJob(ctx, job.ID, errors.New("CSV 表头前两列必须是 device_id,device_key"))
		return
	}
	type completedItem struct {
		RowNo      int    `db:"row_no"`
		ResourceID string `db:"resource_id"`
	}
	var completedRows []completedItem
	if err := s.db.SelectContext(ctx, &completedRows, `SELECT row_no,resource_id FROM admin_job_items WHERE job_id=? AND status=1`, job.ID); err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	completed := make(map[int]string, len(completedRows))
	for _, row := range completedRows {
		completed[row.RowNo] = row.ResourceID
	}
	resultName := fmt.Sprintf("results/%d.csv", job.ID)
	resultPath, _ := s.safePath(resultName, "results")
	resultFile, err := os.OpenFile(resultPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		s.failJob(ctx, job.ID, err)
		return
	}
	writer := csv.NewWriter(resultFile)
	_ = writer.Write([]string{"行号", "设备 ID", "结果", "失败原因"})
	total, succeeded, failed, rowNo := 0, 0, 0, 1
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		rowNo++
		total++
		deviceID, message := "", ""
		if completedDeviceID, ok := completed[rowNo]; ok {
			succeeded++
			_ = writer.Write([]string{fmt.Sprint(rowNo), safeCSVCell(completedDeviceID), "成功", ""})
			continue
		}
		deviceKey := ""
		if readErr != nil || len(record) < 2 {
			message = "CSV 行格式错误或缺少必填列"
		} else {
			deviceID = strings.TrimSpace(record[0])
			deviceKey = strings.TrimSpace(record[1])
			if deviceID == "" || len(deviceID) > 64 || deviceKey == "" || len(deviceKey) > 64 {
				message = "设备 ID 或设备密钥为空、过长或格式无效"
			}
		}
		_, message, err = s.persistImportRow(ctx, job.ID, rowNo, deviceID, deviceKey, message)
		if err != nil {
			_ = resultFile.Close()
			s.failJob(ctx, job.ID, err)
			return
		}
		if message != "" {
			failed++
			_ = writer.Write([]string{fmt.Sprint(rowNo), safeCSVCell(deviceID), "失败", safeCSVCell(message)})
		} else {
			succeeded++
			_ = writer.Write([]string{fmt.Sprint(rowNo), safeCSVCell(deviceID), "成功", ""})
		}
	}
	writer.Flush()
	closeErr := resultFile.Close()
	if err := writer.Error(); err != nil || closeErr != nil {
		s.failJob(ctx, job.ID, errors.New("生成导入结果文件失败"))
		return
	}
	status := JobSucceeded
	if failed > 0 && succeeded > 0 {
		status = JobPartial
	} else if failed > 0 {
		status = JobFailed
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE admin_jobs SET status=?,result_file=?,total_count=?,succeeded_count=?,failed_count=?,worker_id='',lease_until=NULL,finished_at=NOW(),last_error='' WHERE id=? AND status=1 AND worker_id=?`, status, filepath.ToSlash(resultName), total, succeeded, failed, job.ID, s.workerID)
}

func (s *JobService) persistImportRow(ctx context.Context, jobID int64, rowNo int, deviceID, deviceKey, validationMessage string) (int8, string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, validationMessage, err
	}
	defer tx.Rollback()
	message := validationMessage
	if message == "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO device_pool (device_id,device_key,status) VALUES (?,?,0)`, deviceID, deviceKey)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				message = "设备 ID 已存在"
			} else {
				return 0, message, err
			}
		}
	}
	status := int8(1)
	errorCode := ""
	if message != "" {
		status = 2
		errorCode = "INVALID_ROW"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_job_items (job_id,row_no,status,resource_id,error_code,error_message) VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),resource_id=VALUES(resource_id),error_code=VALUES(error_code),error_message=VALUES(error_message)`, jobID, rowNo, status, deviceID, errorCode, message); err != nil {
		return 0, message, err
	}
	if err := tx.Commit(); err != nil {
		return 0, message, err
	}
	return status, message, nil
}

func (s *JobService) startHeartbeat(ctx context.Context, jobID int64) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(jobLeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_, _ = s.db.ExecContext(heartbeatCtx, `UPDATE admin_jobs SET lease_until=DATE_ADD(NOW(), INTERVAL ? SECOND) WHERE id=? AND status=1 AND worker_id=?`, int(jobLeaseDuration/time.Second), jobID, s.workerID)
			}
		}
	}()
	return cancel
}

func validateDeviceImportFile(file *os.File, maxBytes int64) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(io.LimitReader(file, maxBytes+1))
	scanner.Buffer(make([]byte, 64*1024), int(maxBytes)+1)
	lines := 0
	for scanner.Scan() {
		if !utf8.Valid(scanner.Bytes()) {
			return errors.New("CSV 文件必须使用 UTF-8 或带 BOM 的 UTF-8 编码")
		}
		lines++
		if lines > maxDeviceImportRows+1 {
			return fmt.Errorf("CSV 文件超过 %d 行数据上限", maxDeviceImportRows)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 CSV 文件失败: %w", err)
	}
	if lines < 2 {
		return errors.New("CSV 文件必须包含表头和至少一行设备数据")
	}
	return nil
}

func safeCSVCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func (s *JobService) failJob(ctx context.Context, id int64, err error) {
	_, _ = s.db.ExecContext(ctx, `UPDATE admin_jobs SET status=4,worker_id='',lease_until=NULL,finished_at=NOW(),last_error=? WHERE id=? AND worker_id=?`, truncateString(err.Error(), 1024), id, s.workerID)
}

func (s *JobService) ResultPath(job AdminJob) (string, error) {
	if job.ResultFile == "" {
		return "", ErrNotFound
	}
	return s.safePath(job.ResultFile, "results")
}

func (s *JobService) safePath(relative, requiredDir string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.Dir(clean) != requiredDir {
		return "", errors.New("任务文件路径无效")
	}
	absolute := filepath.Join(s.root, clean)
	if !strings.HasPrefix(absolute, s.root+string(filepath.Separator)) {
		return "", errors.New("任务文件路径超出存储目录")
	}
	return absolute, nil
}
