package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePoolYAML(t *testing.T) {
	cases := []struct {
		name, fields string
		open, idle   int
		invalid      bool
	}{
		{"legacy defaults", "", 50, 10, false},
		{"custom budget", "  max_open_conns: 10\n  max_idle_conns: 2\n", 10, 2, false},
		{"no idle connections", "  max_idle_conns: 0\n", 50, 0, false},
		{"small pool", "  max_open_conns: 3\n", 3, 3, false},
		{"explicit zero open", "  max_open_conns: 0\n", 50, 10, false},
		{"negative open", "  max_open_conns: -1\n", 0, 0, true},
		{"negative idle", "  max_idle_conns: -1\n", 0, 0, true},
		{"idle exceeds open", "  max_open_conns: 2\n  max_idle_conns: 3\n", 0, 0, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("jwt_secret: test-secret\ndatabase:\n  dsn: unused\n"+tt.fields), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFile(path)
			if tt.invalid {
				if err == nil {
					t.Fatal("invalid pool configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			open, idle, err := cfg.Database.ConnectionLimits()
			if err != nil || open != tt.open || idle != tt.idle {
				t.Fatalf("limits = %d/%d, %v; want %d/%d", open, idle, err, tt.open, tt.idle)
			}
		})
	}
}
