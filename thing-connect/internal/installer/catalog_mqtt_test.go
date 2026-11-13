package installer

import "testing"

func TestMQTTDependenciesMatchCatalog(t *testing.T) {
	for _, s := range serviceCatalog {
		if got := ServiceUsesMQTT(s.Name); got != s.UsesMQTT {
			t.Fatalf("%s mqtt=%v", s.Name, got)
		}
	}
	if !ServiceUsesMQTT("device-server") || ServiceUsesMQTT("ai-server") || ServiceUsesMQTT("unknown") {
		t.Fatal("unexpected MQTT dependency")
	}
}
