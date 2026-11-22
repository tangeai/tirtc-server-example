package recognition

import (
	"encoding/json"
	"testing"
)

func TestPublicItemOverallStatus(t *testing.T) {
	cases := []struct{ mode, analysis, speech, want string }{
		{"text", "queued", "skipped", "queued"}, {"text", "succeeded", "skipped", "succeeded"},
		{"text_audio", "succeeded", "pending", "processing"}, {"text_audio", "succeeded", "synthesizing", "processing"},
		{"text_audio", "succeeded", "ready", "succeeded"}, {"text_audio", "succeeded", "failed", "partial_failed"},
		{"audio", "succeeded", "pending", "processing"}, {"audio", "succeeded", "ready", "succeeded"},
		{"audio", "succeeded", "failed", "failed"}, {"text", "failed", "skipped", "failed"},
		{"audio", "unknown", "skipped", "unknown"}, {"text_audio", "succeeded", "unknown", "unknown"},
		{"text", "cancelled", "skipped", "cancelled"},
	}
	for _, c := range cases {
		t.Run(c.mode+"/"+c.analysis+"/"+c.speech, func(t *testing.T) {
			v := taskFixture(t, c.mode)
			x := &v.Task.Results[0]
			x.Status = c.analysis
			x.Speech.Status = c.speech
			if c.analysis == "succeeded" {
				x.Result = &Result{Summary: "结果"}
			}
			got := v.PublicTask()
			if got.Results[0].OverallStatus != c.want || got.Status != c.want {
				t.Fatalf("got %+v, want %s", got, c.want)
			}
			if got.Results[0].Status != c.analysis {
				t.Fatal("analysis status changed")
			}
			if c.mode == "audio" && got.Results[0].Result != nil {
				t.Fatal("private analysis leaked")
			}
		})
	}
}

func TestHistoricalSnapshotProjectionDoesNotMutateOrReexecute(t *testing.T) {
	raw := []byte(`{"task":{"request_id":"old","status":"partial_failed","status_revision":7,"results":[{"item_id":"item_1","scheme_id":"old-type","scheme_version":1,"output_mode":"audio","status":"succeeded","result":{"summary":"private"},"speech":{"status":"failed"}}]},"schemes":[{"scheme_id":"old-type","scheme_version":1,"name":"历史名称"}]}`)
	var v Snapshot
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(v)
	got := v.PublicTask()
	after, _ := json.Marshal(v)
	if string(before) != string(after) {
		t.Fatal("read changed stored snapshot")
	}
	if got.Status != "failed" || got.Results[0].Name != "历史名称" || got.Revision != 7 || v.NextStage(0) != "" {
		t.Fatalf("bad history: %+v", got)
	}
	// Never substitute the name of a different published version.
	v.Schemes[0].Version = 2
	if v.PublicTask().Results[0].Name != "" {
		t.Fatal("unrelated version name used")
	}
}

func TestCancellationPreservesDeliverableText(t *testing.T) {
	for _, mode := range []string{"text", "audio", "text_audio"} {
		t.Run(mode, func(t *testing.T) {
			v := taskFixture(t, mode, "text")
			v.Start(0, "analysis")
			v.Complete(Completion{Work: Work{Stage: "analysis"}, Result: &Result{Summary: "可用文字"}})
			v.Cancel()
			got := v.PublicTask()
			want := "partial_failed"
			if mode == "audio" {
				want = "cancelled"
			}
			if got.Status != want || got.Results[1].OverallStatus != "cancelled" || v.NextStage(0) != "" || v.NextStage(1) != "" {
				t.Fatalf("%+v", got)
			}
			if got.Results[0].Status != "succeeded" {
				t.Fatal("completed analysis overwritten")
			}
			raw, _ := json.Marshal(v)
			var restored Snapshot
			json.Unmarshal(raw, &restored)
			if restored.PublicTask().Status != want {
				t.Fatal("cancellation lost on restart")
			}
		})
	}
}

func TestDisabledSpeechPreservesAnalysisAndUnknownPausesFollowingTypes(t *testing.T) {
	v := taskFixture(t, "text_audio", "text")
	v.Complete(Completion{Work: Work{Stage: "analysis"}, Result: &Result{Summary: "文字"}})
	v.DisableItem(0)
	got := v.PublicTask()
	if got.Results[0].Status != "succeeded" || got.Results[0].OverallStatus != "partial_failed" || got.Status != "processing" {
		t.Fatalf("%+v", got)
	}
	v = taskFixture(t, "audio", "text")
	v.Start(0, "analysis")
	v.Expire()
	if v.NextStage(1) != "" || v.PublicTask().Status != "unknown" {
		t.Fatal("unknown must stop paid work")
	}
}

func TestAggregateDeliverableAndFailurePrecedence(t *testing.T) {
	cases := []struct{ first, second, want string }{
		{"succeeded", "succeeded", "succeeded"},
		{"succeeded", "failed", "partial_failed"},
		{"succeeded", "cancelled", "partial_failed"},
		{"failed", "cancelled", "failed"},
		{"cancelled", "cancelled", "cancelled"},
		{"succeeded", "queued", "processing"},
		{"failed", "queued", "processing"},
		{"unknown", "queued", "unknown"},
		{"succeeded", "unknown", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.first+"/"+c.second, func(t *testing.T) {
			v := taskFixture(t, "text", "text")
			for i, status := range []string{c.first, c.second} {
				v.Task.Results[i].Status = status
				if status == "succeeded" {
					v.Task.Results[i].Result = &Result{Summary: "文字"}
				}
			}
			v.Aggregate()
			if v.Task.Status != c.want {
				t.Fatalf("got %s want %s", v.Task.Status, c.want)
			}
		})
	}
}

func TestAcceptedNameIsFixedAndLegacyCancellationStopsPendingSpeech(t *testing.T) {
	v := taskFixture(t, "text_audio")
	v.Schemes[0].Name = "受理时名称"
	// Legacy task rows contain the scheme snapshot, but no per-item name.
	v.Complete(Completion{Work: Work{Stage: "analysis"}, Result: &Result{Summary: "文字"}})
	v.Task.Status = "cancelled"
	got := v.PublicTask()
	if got.Status != "partial_failed" || got.Results[0].OverallStatus != "partial_failed" || got.Results[0].Speech.Status != "skipped" || got.Results[0].Name != "受理时名称" {
		t.Fatalf("legacy cancelled task: %+v", got)
	}
	if v.Task.Results[0].Speech.Status != "pending" {
		t.Fatal("projection mutated persisted speech")
	}
}
