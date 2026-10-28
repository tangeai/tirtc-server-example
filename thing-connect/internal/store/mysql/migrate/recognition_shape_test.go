package migrate

import (
	"reflect"
	"testing"
)

func TestRecognitionColumnAliasesMatchMySQLMetadata(t *testing.T) {
	for _, tc := range []struct{ sql, typ, def, extra string }{
		{"enabled BOOLEAN NOT NULL DEFAULT FALSE", "tinyint(1)", "0", ""},
		{"enabled BOOL NOT NULL DEFAULT TRUE", "tinyint(1)", "1", ""},
		{"updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)", "datetime(6)", "CURRENT_TIMESTAMP(6)", "DEFAULT_GENERATED on update CURRENT_TIMESTAMP(6)"},
		{"created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)", "datetime(6)", "current_timestamp(6)", "DEFAULT_GENERATED"},
		{"label VARCHAR(8) NOT NULL DEFAULT 'false'", "varchar(8)", "false", ""},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			_, want, _, _, err := parseColumnClause(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			got := InformationSchemaColumnShape(tc.typ, "NO", true, tc.def, tc.extra, "")
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("SQL=%+v MySQL=%+v", want, got)
			}
		})
	}
}

func TestCompactIndexName(t *testing.T) {
	for _, clause := range []string{"UNIQUE KEY uq_recognition_request(device_id,request_id)", "UNIQUE KEY uq_recognition_request (device_id,request_id)"} {
		name, shape, err := namedIndexShape(clause)
		if err != nil || name != "uq_recognition_request" || !shape.Unique || len(shape.Parts) != 2 {
			t.Fatalf("index parsed incorrectly: %s %+v %v", name, shape, err)
		}
	}
}
