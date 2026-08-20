package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminJobJSONOnlyExposesResultAvailability(t *testing.T) {
	raw, err := json.Marshal(AdminJob{ResultFile: "results/private.csv", ResultAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "private.csv") || !strings.Contains(text, `"result_available":true`) {
		t.Fatalf("unexpected job JSON: %s", text)
	}
}

func TestValidateDeviceImportFileAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.csv")
	if err := os.WriteFile(path, []byte("\ufeffdevice_id,device_key\n设备-1,key-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateDeviceImportFile(file, 1024); err != nil {
		t.Fatalf("valid UTF-8 BOM CSV rejected: %v", err)
	}
}

func TestValidateDeviceImportFileRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.csv")
	if err := os.WriteFile(path, []byte{'d', 'e', 'v', 'i', 'c', 'e', '_', 'i', 'd', ',', 'd', 'e', 'v', 'i', 'c', 'e', '_', 'k', 'e', 'y', '\n', 0xff, ',', 'x', '\n'}, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateDeviceImportFile(file, 1024); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("expected UTF-8 error, got %v", err)
	}
}

func TestSafeCSVCellEscapesSpreadsheetFormulas(t *testing.T) {
	for _, value := range []string{"=1+1", "+cmd", "-1", "@SUM(A1)", "\tformula", "\rformula"} {
		if got := safeCSVCell(value); got != "'"+value {
			t.Fatalf("safeCSVCell(%q) = %q", value, got)
		}
	}
	if got := safeCSVCell("device-1"); got != "device-1" {
		t.Fatalf("ordinary value changed: %q", got)
	}
}
