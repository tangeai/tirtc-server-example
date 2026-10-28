package recognition

import (
	"fmt"
	"time"
)

type Snapshot struct {
	Task     Task      `json:"task"`
	Schemes  []Scheme  `json:"schemes"`
	Internal []*Result `json:"internal"`
}

func (v Snapshot) PublicTask() Task {
	t := v.Task
	t.Results = append([]Item(nil), t.Results...)
	for i := range t.Results {
		if t.Results[i].OutputMode == "audio" {
			t.Results[i].Result = nil
		}
	}
	return t
}

func (v *Snapshot) Aggregate() {
	good, bad := 0, 0
	for _, x := range v.Task.Results {
		if x.Status == "unknown" || x.Speech.Status == "unknown" {
			v.Task.Status = "unknown"
			return
		}
		if x.Status == "queued" || x.Status == "processing" || x.Speech.Status == "pending" || x.Speech.Status == "synthesizing" {
			v.Task.Status = "queued"
			return
		}
		if x.Status == "succeeded" && (x.Speech.Status == "ready" || x.Speech.Status == "skipped") {
			good++
		} else {
			if x.Status == "succeeded" {
				good++
			}
			bad++
		}
	}
	switch {
	case bad == 0:
		v.Task.Status = "succeeded"
	case good > 0:
		v.Task.Status = "partial_failed"
	default:
		v.Task.Status = "failed"
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
	v := Snapshot{Task: Task{ID: req.ID, DeviceID: device, Status: "queued", Revision: 1, Results: []Item{}, CreatedAt: now}, Schemes: schemes, Internal: make([]*Result, len(schemes))}
	for i, x := range schemes {
		speech := "pending"
		if x.OutputMode == "text" {
			speech = "skipped"
		}
		v.Task.Results = append(v.Task.Results, Item{ID: fmt.Sprintf("item_%d", i+1), Order: i + 1, SchemeID: x.ID, SchemeVersion: x.Version, OutputMode: x.OutputMode, Status: "queued", Speech: Speech{Status: speech}})
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

func (v *Snapshot) Cancel() {
	v.Task.Status = "cancelled"
	for i := range v.Task.Results {
		if v.Task.Results[i].Status == "queued" {
			v.Task.Results[i].Status = "cancelled"
			v.Task.Results[i].Speech.Status = "skipped"
		}
	}
}

func (v *Snapshot) DisableItem(i int) {
	x := &v.Task.Results[i]
	x.Status = "cancelled"
	x.Speech.Status = "skipped"
	x.Error = &Failure{Code: 40901, Message: "识别类型已停用"}
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
	x := v.Task.Results[i]
	if x.Status == "queued" {
		return "analysis"
	}
	if x.Status == "succeeded" && x.Speech.Status == "pending" {
		return "tts"
	}
	return ""
}
