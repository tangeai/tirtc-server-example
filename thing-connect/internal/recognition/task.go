package recognition

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type Snapshot struct {
	// Cancelled records whole-task cancellation independently of partial output.
	Cancelled bool      `json:"cancelled,omitempty"`
	Task      Task      `json:"task"`
	Schemes   []Scheme  `json:"schemes"`
	Internal  []*Result `json:"internal"`
}

// PublicTask projects legacy snapshots without changing persisted data or
// consulting the current type catalogue. HTTP and outbox use this projection.
func (v Snapshot) PublicTask() Task {
	v.Task.Results = append([]Item(nil), v.Task.Results...)
	if v.Cancelled || v.Task.Status == "cancelled" {
		v.Cancelled = true
		for i := range v.Task.Results {
			v.cancelItem(i)
		}
	}
	v.Aggregate()
	for i := range v.Task.Results {
		x := &v.Task.Results[i]
		if x.Name == "" {
			for _, scheme := range v.Schemes {
				if scheme.ID == x.SchemeID && scheme.Version == x.SchemeVersion {
					x.Name = scheme.Name
					break
				}
			}
		}
		if x.OutputMode == "audio" {
			x.Result = nil
		}
	}
	return v.Task
}

func (x Item) deliverable() bool {
	return (x.OutputMode != "audio" && x.Result != nil) || x.Speech.Status == "ready"
}

func (x Item) overallStatus() string {
	if x.Status == "unknown" || x.Speech.Status == "unknown" {
		return "unknown"
	}
	if x.Status == "cancelled" || (x.Speech.Status == "skipped" && x.Speech.Error != nil && x.Speech.Error.Code == 40901) {
		if x.deliverable() {
			return "partial_failed"
		}
		return "cancelled"
	}
	if x.Status != "succeeded" {
		return x.Status
	}
	if x.OutputMode == "text" || x.Speech.Status == "ready" {
		return "succeeded"
	}
	switch x.Speech.Status {
	case "pending", "synthesizing":
		return "processing"
	default:
		if x.deliverable() {
			return "partial_failed"
		}
		return "failed"
	}
}

func (v *Snapshot) Aggregate() {
	good, bad, cancelled := 0, 0, 0
	started, pending, unknown := v.Task.Status == "processing", false, false
	wholeCancelled := v.Cancelled || v.Task.Status == "cancelled"
	for i := range v.Task.Results {
		x := &v.Task.Results[i]
		x.OverallStatus = x.overallStatus()
		if x.deliverable() {
			good++
		}
		if x.Status == "processing" || x.Status == "succeeded" || x.Status == "failed" {
			started = true
		}
		switch x.OverallStatus {
		case "unknown":
			unknown = true
		case "queued", "processing":
			pending = true
		case "failed", "partial_failed":
			bad++
		case "cancelled":
			cancelled++
		}
	}
	switch {
	case wholeCancelled:
		v.Task.Status = "cancelled"
		if good > 0 {
			v.Task.Status = "partial_failed"
		}
	case unknown:
		v.Task.Status = "unknown"
	case pending:
		v.Task.Status = "queued"
		if started {
			v.Task.Status = "processing"
		}
	case bad == 0 && cancelled == 0:
		v.Task.Status = "succeeded"
	case good > 0:
		v.Task.Status = "partial_failed"
	case bad > 0:
		v.Task.Status = "failed"
	default:
		v.Task.Status = "cancelled"
	}
}

// ValidatePolicy checks a submission against the locked device policy.
func ValidatePolicy(p Policy, revision int64) error {
	if !p.Enabled || !p.Allowed {
		return ErrDisabled
	}
	if p.Revision != revision {
		return ErrConflict
	}
	return nil
}

// NewSnapshot validates the locked policy/quota snapshot and fixes the execution order.
// The adapter must persist this decision while holding the same device lock.
func NewSnapshot(device string, req Request, p Policy, schemes []Scheme, usage Usage, now time.Time) (Snapshot, error) {
	if err := ValidatePolicy(p, req.Revision); err != nil {
		return Snapshot{}, err
	}
	if len(schemes) == 0 {
		return Snapshot{}, ErrDisabled
	}
	for _, scheme := range schemes {
		if !scheme.Enabled {
			return Snapshot{}, ErrConflict
		}
		if !ValidAudio(req.Data, scheme.Limits) {
			return Snapshot{}, ErrInvalid
		}
	}
	if usage.Used+int64(len(schemes)) > p.MonthlyLimit {
		return Snapshot{}, ErrQuota
	}
	v := Snapshot{Task: Task{ID: req.ID, DeviceID: device, Status: "queued", Revision: 1, Results: []Item{}, CreatedAt: now}, Schemes: append([]Scheme(nil), schemes...), Internal: make([]*Result, len(schemes))}
	for i, x := range schemes {
		speech := "pending"
		if x.OutputMode == "text" {
			speech = "skipped"
		}
		v.Task.Results = append(v.Task.Results, Item{Name: x.Name, OverallStatus: "queued", ID: fmt.Sprintf("item_%d", i+1), Order: i + 1, SchemeID: x.ID, SchemeVersion: x.Version, OutputMode: x.OutputMode, Status: "queued", Speech: Speech{Status: speech}})
	}
	return v, nil
}

// Complete advances one leased stage; SQL lease checks fence late completions before calling it.
func (v *Snapshot) Complete(done Completion) string {
	x := &v.Task.Results[done.Work.ItemIndex]
	status := "succeeded"
	if done.Failed {
		status = "failed"
	}
	if done.Unknown {
		status = "unknown"
	}
	failure := &Failure{Code: 50200, Message: "模型调用未完成，请查询任务状态"}
	if done.Work.Stage == "analysis" {
		x.Status = status
		if done.Failed {
			x.Error = failure
			x.Speech.Status = "skipped"
		} else {
			x.Result = done.Result
			v.Internal[done.Work.ItemIndex] = done.Result
		}
	} else if done.Failed {
		x.Speech.Status = status
		x.Speech.Error = failure
	} else {
		x.Speech = Speech{Status: "ready", Format: "wav", URL: fmt.Sprintf("/v1/recognition/requests/%s/audio/%s", v.Task.ID, x.ID)}
	}
	v.Aggregate()
	return status
}

// NotificationTypes classifies a completed analysis using its published scheme snapshot.
func (v Snapshot) NotificationTypes(index int, stage string) []string {
	if stage != "analysis" || index < 0 || index >= len(v.Task.Results) || index >= len(v.Schemes) || index >= len(v.Internal) || v.Task.Results[index].Status != "succeeded" {
		return nil
	}
	result := v.Internal[index]
	if result == nil || !result.Alarm {
		return nil
	}
	var kinds []string
	for _, kind := range []string{"crying", "quarrel"} {
		for _, label := range result.Labels {
			if slices.Contains(v.Schemes[index].EventLabels[kind], strings.TrimSpace(label)) {
				kinds = append(kinds, kind)
				break
			}
		}
	}
	return kinds
}

func (v *Snapshot) Cancel() {
	v.Cancelled = true
	for i := range v.Task.Results {
		v.cancelItem(i)
	}
	v.Aggregate()
}

func (v *Snapshot) cancelItem(i int) {
	x := &v.Task.Results[i]
	failure := &Failure{Code: 40901, Message: "识别处理已取消"}
	if x.Status == "queued" || x.Status == "processing" {
		x.Status = "cancelled"
		x.Error = failure
	}
	if x.Speech.Status == "pending" || x.Speech.Status == "synthesizing" {
		x.Speech.Status = "skipped"
		x.Speech.Error = failure
	}
}

func (v *Snapshot) DisableItem(i int) {
	v.cancelItem(i)
	v.Aggregate()
}

func (v *Snapshot) Start(i int, stage string) {
	x := &v.Task.Results[i]
	if stage == "analysis" {
		x.Status = "processing"
	} else {
		x.Speech.Status = "synthesizing"
	}
	v.Task.Status = "processing"
	v.Task.Revision++
}

func (v *Snapshot) Expire() {
	v.Task.Status = "unknown"
	for i := range v.Task.Results {
		x := &v.Task.Results[i]
		if x.Status == "processing" {
			x.Status = "unknown"
			x.Error = &Failure{Code: 50200, Message: "调用结果待确认，不会自动重复调用"}
			x.Speech.Status = "skipped"
		}
		if x.Speech.Status == "synthesizing" {
			x.Speech.Status = "unknown"
			x.Speech.Error = &Failure{Code: 50200, Message: "语音合成结果待确认"}
		}
	}
}

// NextStage preserves the configured type order, including speech synthesis.
func (v Snapshot) NextStage(i int) string {
	if v.Cancelled || v.Task.Status == "cancelled" || v.Task.Status == "unknown" {
		return ""
	}
	x := v.Task.Results[i]
	if x.Status == "queued" {
		return "analysis"
	}
	if x.Status == "succeeded" && x.Speech.Status == "pending" {
		return "tts"
	}
	return ""
}
