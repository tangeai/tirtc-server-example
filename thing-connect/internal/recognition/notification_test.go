package recognition

import "testing"

func TestNotificationTypesUsesSuccessfulInternalAnalysis(t *testing.T) {
	snapshot := Snapshot{Task: Task{Results: []Item{{OutputMode: "audio", Status: "succeeded"}}}, Schemes: []Scheme{{EventLabels: map[string][]string{"crying": {"哭声"}, "quarrel": {"争吵声"}}}}, Internal: []*Result{{Alarm: true, Labels: []string{"哭声", "哭声", "争吵声"}}}}
	got := snapshot.NotificationTypes(0, "analysis")
	if len(got) != 2 || got[0] != "crying" || got[1] != "quarrel" {
		t.Fatal(got)
	}
	if got := snapshot.NotificationTypes(0, "tts"); len(got) != 0 {
		t.Fatal(got)
	}
	snapshot.Task.Results[0].Status = "unknown"
	if got := snapshot.NotificationTypes(0, "analysis"); len(got) != 0 {
		t.Fatal(got)
	}
	snapshot.Task.Results[0].Status = "succeeded"
	snapshot.Schemes[0].EventLabels = nil
	if got := snapshot.NotificationTypes(0, "analysis"); len(got) != 0 {
		t.Fatal(got)
	}
}
