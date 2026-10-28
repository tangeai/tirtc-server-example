package admin

import (
	"errors"
	"github.com/gin-gonic/gin"
	"thing-connect/internal/apiresp"
	r "thing-connect/internal/recognition"
)

func (s *HTTPServer) SetRecognitionService(v *RecognitionService) { s.recognition = v }
func (s *HTTPServer) getRecognition(c *gin.Context) {
	v, e := s.recognition.State(c.Request.Context(), c.Param("device_id"))
	adminRecognitionResult(c, v, e)
}
func (s *HTTPServer) putRecognition(c *gin.Context) {
	var v struct {
		Allowed  *bool  `json:"allowed"`
		Limit    *int64 `json:"monthly_limit"`
		Revision *int64 `json:"expected_revision"`
		Reason   string `json:"reason"`
	}
	if c.ShouldBindJSON(&v) != nil || v.Allowed == nil || v.Limit == nil || v.Revision == nil {
		adminRecognitionResult(c, nil, r.ErrInvalid)
		return
	}
	out, e := s.recognition.Update(c.Request.Context(), c.Param("device_id"), *v.Allowed, *v.Limit, *v.Revision, boardMeta(c, v.Reason))
	adminRecognitionResult(c, out, e)
}
func (s *HTTPServer) recognitionSchemes(c *gin.Context) {
	v, e := s.recognition.Schemes(c.Request.Context())
	adminRecognitionResult(c, v, e)
}
func (s *HTTPServer) recognitionSave(c *gin.Context) {
	var v struct {
		Scheme   r.Scheme `json:"scheme"`
		Revision *int64   `json:"expected_revision"`
		Reason   string   `json:"reason"`
		Action   string   `json:"action"`
	}
	if c.ShouldBindJSON(&v) != nil || v.Revision == nil {
		adminRecognitionResult(c, nil, r.ErrInvalid)
		return
	}
	out, e := s.recognition.SaveScheme(c.Request.Context(), v.Scheme, v.Action, *v.Revision, boardMeta(c, v.Reason))
	adminRecognitionResult(c, out, e)
}
func (s *HTTPServer) recognitionRecords(c *gin.Context) {
	v, e := s.recognition.Records(c.Request.Context(), c.Query("device_id"))
	adminRecognitionResult(c, v, e)
}
func adminRecognitionResult(c *gin.Context, v any, e error) {
	switch {
	case e == nil:
		apiresp.OK(c, v)
	case errors.Is(e, r.ErrInvalid):
		apiresp.BadParam(c, "请检查名称、提示词、模型、返回内容、限制和修改原因")
	case errors.Is(e, ErrNotFound), errors.Is(e, r.ErrNotFound):
		c.JSON(404, apiresp.JSON{Code: 40400, Msg: "设备或识别类型不存在"})
	case errors.Is(e, ErrConflict), errors.Is(e, r.ErrConflict):
		c.JSON(409, apiresp.JSON{Code: 40901, Msg: "配置已变更，请刷新后重试"})
	default:
		apiresp.Internal(c, "读取或保存识别配置失败")
	}
}
