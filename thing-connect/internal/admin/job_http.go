package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
)

func (s *HTTPServer) createDeviceImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.jobs.maxBytes+1024*1024)
	file, err := c.FormFile("file")
	if err != nil {
		apiresp.BadParam(c, "请选择不超过限制的 CSV 文件")
		return
	}
	reason := strings.TrimSpace(c.PostForm("reason"))
	if reason == "" {
		apiresp.BadParam(c, "导入原因不能为空")
		return
	}
	identity, _ := identityFromContext(c)
	id, err := s.jobs.CreateDevicePoolImport(c, file, identity.UserID)
	if err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	_ = s.store.Audit(c, requestAudit(c, identity, "device.import", "admin_job", strconv.FormatInt(id, 10), reason, "", `{"queued":true}`))
	c.JSON(http.StatusAccepted, apiresp.JSON{Code: 200, Msg: "ok", Data: gin.H{"job_id": id}})
}

func (s *HTTPServer) listJobs(c *gin.Context) {
	page, size := pageParams(c)
	where, args := ` WHERE 1=1`, []any{}
	if status := c.Query("status"); status != "" {
		parsed, err := strconv.Atoi(status)
		if err != nil || parsed < 0 || parsed > JobFailed {
			apiresp.BadParam(c, "任务状态无效")
			return
		}
		where += ` AND status=?`
		args = append(args, parsed)
	}
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*) FROM admin_jobs`+where, args...)
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	var jobs []AdminJob
	if err := s.store.db.SelectContext(c, &jobs, `SELECT `+adminJobColumns+` FROM admin_jobs`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, queryArgs...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	for i := range jobs {
		jobs[i].ResultAvailable = jobs[i].ResultFile != ""
	}
	var cleanupPending, configPending int
	_ = s.store.db.GetContext(c, &cleanupPending, `SELECT COUNT(*) FROM cleanup_outbox`)
	_ = s.store.db.GetContext(c, &configPending, `SELECT COUNT(*) FROM config_publish_outbox WHERE delivered_at IS NULL`)
	apiresp.OK(c, gin.H{"jobs": pageResult{Items: jobs, Page: page, PageSize: size, Total: total}, "related_queues": gin.H{"cleanup_pending": cleanupPending, "config_publish_pending": configPending}})
}

func (s *HTTPServer) getJob(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "任务 ID 无效")
		return
	}
	var job AdminJob
	if err := s.store.db.GetContext(c, &job, `SELECT `+adminJobColumns+` FROM admin_jobs WHERE id=?`, id); err != nil {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "任务不存在"})
		return
	}
	job.ResultAvailable = job.ResultFile != ""
	page, size := pageParams(c)
	type item struct {
		ID           int64  `db:"id" json:"id"`
		RowNo        int    `db:"row_no" json:"row_no"`
		Status       int8   `db:"status" json:"status"`
		ResourceID   string `db:"resource_id" json:"resource_id"`
		ErrorCode    string `db:"error_code" json:"error_code"`
		ErrorMessage string `db:"error_message" json:"error_message"`
	}
	var items []item
	_ = s.store.db.SelectContext(c, &items, `SELECT id,row_no,status,resource_id,error_code,error_message FROM admin_job_items WHERE job_id=? ORDER BY row_no LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	apiresp.OK(c, gin.H{"job": job, "items": items, "page": page, "page_size": size})
}

func (s *HTTPServer) jobResult(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "任务 ID 无效")
		return
	}
	var job AdminJob
	if err := s.store.db.GetContext(c, &job, `SELECT `+adminJobColumns+` FROM admin_jobs WHERE id=?`, id); err != nil {
		c.JSON(404, apiresp.JSON{Code: 404, Msg: "任务不存在"})
		return
	}
	path, err := s.jobs.ResultPath(job)
	if errors.Is(err, ErrNotFound) {
		c.JSON(404, apiresp.JSON{Code: 404, Msg: "任务结果尚未生成"})
		return
	}
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="device-import-%d-result.csv"`, id))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.File(path)
}

func (s *HTTPServer) retryJob(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "重试请求无效")
		return
	}
	result, err := s.store.db.ExecContext(c, `UPDATE admin_jobs SET status=0,worker_id='',lease_until=NULL,next_attempt_at=NOW(),last_error='' WHERE id=? AND status IN (3,4)`, id)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 409, Msg: "只有部分成功或失败的任务可以重试"})
		return
	}
	identity, _ := identityFromContext(c)
	_ = s.store.Audit(c, requestAudit(c, identity, "job.retry", "admin_job", strconv.FormatInt(id, 10), request.Reason, "", `{"status":0}`))
	apiresp.OK(c, gin.H{"id": id, "queued": true})
}
