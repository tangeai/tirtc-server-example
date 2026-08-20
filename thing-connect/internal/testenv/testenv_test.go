package testenv

import "testing"

func TestValidateTestDatabaseDSN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{name: "dedicated test schema", dsn: "user:pass@tcp(127.0.0.1:3306)/thing_connect_test?parseTime=true"},
		{name: "case insensitive suffix", dsn: "user:pass@tcp(127.0.0.1:3306)/THING_CONNECT_TEST"},
		{name: "development schema", dsn: "user:pass@tcp(127.0.0.1:3306)/thing_connect_dev", wantErr: true},
		{name: "production schema", dsn: "user:pass@tcp(127.0.0.1:3306)/thing_connect", wantErr: true},
		{name: "missing schema", dsn: "user:pass@tcp(127.0.0.1:3306)/", wantErr: true},
		{name: "invalid dsn", dsn: "not-a-dsn", wantErr: true},
	}
	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateTestDatabaseDSN(tc.dsn)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateTestDatabaseDSN() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
