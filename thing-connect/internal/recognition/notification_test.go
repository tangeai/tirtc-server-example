package recognition

import "testing"

func TestAlarmNotification(t *testing.T) {
	for _, tc := range []struct {
		status, stage string
		alarm         bool
		labels        []string
		want          bool
		text          string
	}{
		{"succeeded", "analysis", true, nil, true, ""},
		{"succeeded", "analysis", true, []string{" 哭泣 ", "哭泣", "陌生词"}, true, "哭泣、陌生词"},
		{"succeeded", "analysis", false, []string{"哭声"}, false, ""},
		{"unknown", "analysis", true, nil, false, ""},
		{"succeeded", "tts", true, nil, false, ""},
	} {
		v := Snapshot{Task: Task{Results: []Item{{OutputMode: "audio", Status: tc.status}}}, Internal: []*Result{{Alarm: tc.alarm, Labels: tc.labels}}}
		if text, ok := v.AlarmNotification(0, tc.stage); ok != tc.want || text != tc.text {
			t.Fatal(tc, text, ok)
		}
	}
}

func TestAlarmResultWithUnusableLabels(t *testing.T) {
	for _, raw := range []string{
		`{"summary":"异常","alarm":true,"details":{}}`,
		`{"summary":"异常","alarm":true,"labels":null,"details":{}}`,
		`{"summary":"异常","alarm":true,"labels":"哭","details":{}}`,
	} {
		result, err := ValidateResult([]byte(raw))
		if err != nil || !result.Alarm || len(result.Labels) != 0 {
			t.Fatal(raw, result, err)
		}
	}
}
