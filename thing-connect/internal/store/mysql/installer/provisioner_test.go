package installer

import (
	"slices"
	"testing"
)

func TestRuntimeDMLTablesCoverSharedAccountConsumers(t *testing.T) {
	minimal, err := runtimeDMLTables(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, optionalTable := range []string{"voip_device_auth", "ai_user_resource", "call_contact"} {
		if !slices.Contains(minimal, optionalTable) {
			t.Fatalf("shared runtime account omitted %s privileges", optionalTable)
		}
	}
	if !slices.Contains(runtimeReadOnlyTables, "schema_migrations") {
		t.Fatal("runtime preflight omitted schema_migrations SELECT")
	}
	selected, err := runtimeDMLTables([]string{"ai-server"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(minimal, selected) {
		t.Fatalf("shared runtime grant changed with process selection: minimal=%v selected=%v", minimal, selected)
	}
	if _, err := runtimeDMLTables([]string{"ai-server", "ai-server"}); err == nil {
		t.Fatal("duplicate optional services were accepted")
	}
}
