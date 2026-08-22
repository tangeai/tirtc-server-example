package migrate

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestSplitSQLStatementsRespectsQuotesAndComments(t *testing.T) {
	statements, err := splitSQLStatements("-- header\nINSERT INTO demo(value) VALUES ('a;b'); # tail\nUPDATE demo SET value=\"c;d\"; /* done; */")
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 {
		t.Fatalf("statements = %#v", statements)
	}
	if statements[0] != "INSERT INTO demo(value) VALUES ('a;b')" {
		t.Fatalf("first statement = %q", statements[0])
	}
	if statements[1] != "UPDATE demo SET value=\"c;d\"" {
		t.Fatalf("second statement = %q", statements[1])
	}
}

func TestEmbeddedMigrationFilesAreNonEmpty(t *testing.T) {
	paths := []string{
		"migrations/shared/schema_migrations.sql",
		"migrations/core/001_user.sql", "migrations/core/001_device.sql",
		"migrations/core/001_voip.sql", "migrations/core/001_ai.sql",
		"migrations/core/001_call.sql", "migrations/core/002_user_device_sorting.sql",
		"migrations/core/003_schema_comments.sql",
		"migrations/admin/001_schema.sql", "migrations/admin/002_job_leases.sql",
		"migrations/admin/003_plaintext_secrets.sql", "migrations/admin/004_installation_state.sql",
		"migrations/admin/005_schema_comments.sql",
	}
	for _, path := range paths {
		statements, err := statementsFromFiles(path)
		if err != nil || len(statements) == 0 {
			t.Fatalf("%s: statements=%d err=%v", path, len(statements), err)
		}
	}
}

func TestMigrationCatalogDiscoversOrderedContiguousVersions(t *testing.T) {
	catalog, err := buildMigrationCatalog([]string{
		"migrations/core/002_change.sql",
		"migrations/admin/001_schema.sql",
		"migrations/core/001_user.sql",
		"migrations/core/001_device.sql",
		"migrations/admin/002_change.sql",
		"migrations/shared/schema_migrations.sql",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog["core"]; !reflect.DeepEqual(got, []migrationVersion{
		{Version: 1, Paths: []string{"migrations/core/001_device.sql", "migrations/core/001_user.sql"}},
		{Version: 2, Paths: []string{"migrations/core/002_change.sql"}},
	}) {
		t.Fatalf("core migration catalog = %#v", got)
	}
	if _, err := buildMigrationCatalog([]string{
		"migrations/core/001_schema.sql",
		"migrations/core/003_gap.sql",
		"migrations/admin/001_schema.sql",
	}); err == nil || !strings.Contains(err.Error(), "gap at version 2") {
		t.Fatalf("migration gap was accepted: %v", err)
	}
}

func TestNewSchemaMigrationsDocumentTablesAndColumns(t *testing.T) {
	commentBaseline := map[string]int{"core": 2, "admin": 4}
	for component, migrations := range migrationCatalog {
		for _, migration := range migrations {
			if migration.Version <= commentBaseline[component] {
				continue
			}
			statements, err := statementsFromFiles(migration.Paths...)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range statements {
				upper := strings.ToUpper(strings.TrimSpace(statement))
				switch {
				case strings.HasPrefix(upper, "CREATE TABLE "):
					if !strings.Contains(upper, " COMMENT=") && !strings.Contains(upper, " COMMENT =") {
						t.Errorf("%s migration %d CREATE TABLE lacks a table COMMENT", component, migration.Version)
					}
					open := strings.IndexByte(statement, '(')
					close := strings.LastIndex(strings.ToUpper(statement), ") ENGINE")
					if open < 0 || close <= open {
						t.Fatalf("unsupported CREATE TABLE in %s migration %d", component, migration.Version)
					}
					for _, rawClause := range splitTopLevel(statement[open+1 : close]) {
						clause := strings.TrimSpace(rawClause)
						clauseUpper := strings.ToUpper(clause)
						if strings.HasPrefix(clauseUpper, "PRIMARY ") || strings.HasPrefix(clauseUpper, "KEY ") ||
							strings.HasPrefix(clauseUpper, "INDEX ") || strings.HasPrefix(clauseUpper, "UNIQUE ") {
							continue
						}
						if !strings.Contains(clauseUpper, " COMMENT ") {
							t.Errorf("%s migration %d column lacks COMMENT: %s", component, migration.Version, clause)
						}
					}
				case strings.HasPrefix(upper, "ALTER TABLE "):
					for _, rawClause := range splitTopLevel(statement) {
						clauseUpper := strings.ToUpper(strings.TrimSpace(rawClause))
						columnChange := strings.HasPrefix(clauseUpper, "ADD COLUMN ") ||
							strings.HasPrefix(clauseUpper, "MODIFY COLUMN ") ||
							strings.Contains(clauseUpper, " ADD COLUMN ") || strings.Contains(clauseUpper, " MODIFY COLUMN ")
						if columnChange &&
							!strings.Contains(clauseUpper, " COMMENT ") {
							t.Errorf("%s migration %d column change lacks COMMENT: %s", component, migration.Version, rawClause)
						}
					}
				}
			}
		}
	}
}

func TestCurrentSchemaShapeIncludesEveryDomainAndAlteredColumns(t *testing.T) {
	shape, err := CurrentSchemaShape()
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]map[string]string{
		"admin_users":      {"password": "varchar(255)"},
		"ai_user_resource": {"name": "varchar(128)"},
		"users":            {"disabled_at": "datetime"},
		"device_bind":      {"mac_user_key": "varchar(64)"},
	}
	for table, columns := range checks {
		for column, dataType := range columns {
			if got := shape[table].Columns[column].ColumnType; got != dataType {
				t.Errorf("%s.%s type = %q, want %q", table, column, got, dataType)
			}
		}
	}
	generated := shape["device_bind"].Columns["mac_user_key"]
	if generated.Nullable != true || generated.GeneratedKind != "STORED" || generated.GenerationExpression == "" {
		t.Fatalf("device_bind.mac_user_key shape = %+v", generated)
	}
	unique := shape["device_bind"].Indexes["uq_mac_user"]
	if !unique.Unique || !reflect.DeepEqual(unique.Parts, []IndexPart{{Column: "mac_user_key"}}) {
		t.Fatalf("device_bind.uq_mac_user shape = %+v", unique)
	}
	sorting := shape["device_bind"].Indexes["idx_device_active_time"]
	if sorting.Unique || !reflect.DeepEqual(sorting.Parts, []IndexPart{{Column: "active_time"}, {Column: "id"}}) {
		t.Fatalf("device_bind.idx_device_active_time shape = %+v", sorting)
	}
	updated := shape["users"].Columns["updated_at"]
	if updated.Nullable || updated.Default != "current_timestamp" || updated.OnUpdate != "current_timestamp" {
		t.Fatalf("users.updated_at shape = %+v", updated)
	}
}

func TestSchemaShapeForVersionsExcludesFutureChangesAndTracksEarlyClaim(t *testing.T) {
	shape, err := SchemaShapeForVersions(map[string]int{"core": 1, "admin": 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := shape["users"].Indexes["idx_users_created"]; ok {
		t.Fatal("core version 1 unexpectedly includes the version 2 sort index")
	}
	if _, ok := shape["admin_jobs"].Columns["lease_until"]; ok {
		t.Fatal("admin version 1 unexpectedly includes the version 2 lease column")
	}
	if _, ok := shape["config_entries"].Columns["secret_value"]; ok {
		t.Fatal("admin version 1 unexpectedly includes the version 3 secret column")
	}
	if _, ok := shape["thingconnect_installation_state"]; ok {
		t.Fatal("an unclaimed admin version 1 database unexpectedly includes the ownership table")
	}

	claimed, err := SchemaShapeForVersions(map[string]int{"core": 1, "admin": 1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := claimed["thingconnect_installation_state"]; !ok {
		t.Fatal("an interrupted claimed database must include the ownership table")
	}
}

func TestInformationSchemaColumnShapeNormalizesMySQLGeneratedMetadata(t *testing.T) {
	got := InformationSchemaColumnShape(
		"varchar(64)", "YES", false, "", "STORED GENERATED",
		"if((`mac` = _utf8mb4'') or (`user_id` = 0),NULL,concat(`mac`,_utf8mb4':',`user_id`))",
	)
	want := ColumnShape{
		ColumnType: "varchar(64)", Nullable: true, Default: nullColumnDefault,
		GeneratedKind: "STORED", GenerationExpression: "ifmac=''oruser_id=0,null,concatmac,':',user_id",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("column shape = %+v, want %+v", got, want)
	}
}

func TestInformationSchemaColumnShapeKeepsEmptyStringDefaultDistinctFromNull(t *testing.T) {
	empty := InformationSchemaColumnShape("varchar(64)", "NO", true, "", "", "")
	if empty.Default != "" {
		t.Fatalf("empty string default = %q, want an empty string", empty.Default)
	}
	null := InformationSchemaColumnShape("varchar(64)", "YES", false, "", "", "")
	if null.Default != nullColumnDefault {
		t.Fatalf("NULL default = %q, want %q", null.Default, nullColumnDefault)
	}
}

func TestInformationSchemaColumnShapeNormalizesEscapedGeneratedLiterals(t *testing.T) {
	got := InformationSchemaColumnShape(
		"varchar(64)", "YES", false, "", "STORED GENERATED",
		`if((mac = \'\') or (user_id = 0),NULL,concat(mac,\':\',user_id))`,
	)
	if got.GenerationExpression != "ifmac=''oruser_id=0,null,concatmac,':',user_id" {
		t.Fatalf("generation expression = %q", got.GenerationExpression)
	}
}

type unknownTargetConn struct{}

func (unknownTargetConn) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	panic("unexpected write")
}

type ledgerGuardConn struct {
	tables        []string
	markerProduct string
	markerStatus  string
}

func (ledgerGuardConn) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	panic("unexpected write")
}
func (connection ledgerGuardConn) GetContext(_ context.Context, destination any, query string, _ ...any) error {
	if !strings.Contains(query, "thingconnect_installation_state") {
		panic("unexpected scalar query")
	}
	value := reflect.ValueOf(destination).Elem()
	value.FieldByName("Product").SetString(connection.markerProduct)
	value.FieldByName("Status").SetString(connection.markerStatus)
	return nil
}
func (connection ledgerGuardConn) SelectContext(_ context.Context, destination any, query string, _ ...any) error {
	if strings.Contains(query, "information_schema.tables") {
		*(destination.(*[]string)) = append([]string(nil), connection.tables...)
	}
	// The ledger query intentionally leaves its typed slice empty.
	return nil
}
func (unknownTargetConn) GetContext(context.Context, any, string, ...any) error {
	panic("unexpected scalar query")
}
func (unknownTargetConn) SelectContext(_ context.Context, destination any, _ string, _ ...any) error {
	tables := destination.(*[]string)
	*tables = []string{"foreign_records"}
	return nil
}

func TestMigrationPreflightRefusesUnknownNonEmptyTargetBeforeWrite(t *testing.T) {
	err := requireOwnedMigrationTarget(context.Background(), unknownTargetConn{})
	if err == nil || !strings.Contains(err.Error(), "unknown table") {
		t.Fatalf("requireOwnedMigrationTarget = %v", err)
	}
}

func TestMigrationPreflightRequiresMarkerToRecoverEmptyLedger(t *testing.T) {
	withoutMarker := ledgerGuardConn{tables: []string{"schema_migrations"}}
	if err := requireOwnedMigrationTarget(context.Background(), withoutMarker); err == nil {
		t.Fatal("generic empty schema_migrations table was trusted")
	}
	recoverable := ledgerGuardConn{
		tables:        []string{"device_pool", "schema_migrations", "thingconnect_installation_state"},
		markerProduct: "thingconnect", markerStatus: "installing",
	}
	if err := requireOwnedMigrationTarget(context.Background(), recoverable); err != nil {
		t.Fatalf("recoverable partial migration = %v", err)
	}
	installedWithoutLedger := recoverable
	installedWithoutLedger.markerStatus = "installed"
	if err := requireOwnedMigrationTarget(context.Background(), installedWithoutLedger); err == nil {
		t.Fatal("installed database with an empty ledger was trusted")
	}
}

func TestEmbeddedMigrationsAndBootstrapSchemaHaveSameObjects(t *testing.T) {
	var embedded strings.Builder
	if err := fs.WalkDir(migrationSQL, "migrations", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".sql") {
			raw, readErr := migrationSQL.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			embedded.Write(raw)
			embedded.WriteByte('\n')
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "scripts", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`CREATE TABLE IF NOT EXISTS [a-z_]+`),
		regexp.MustCompile(`(?:UNIQUE KEY|KEY|INDEX) [A-Za-z0-9_]+`),
	}
	for _, pattern := range patterns {
		want := matchSet(pattern, embedded.String())
		got := matchSet(pattern, string(bootstrap))
		for object := range want {
			if !got[object] {
				t.Errorf("scripts/schema.sql is missing %q", object)
			}
		}
		for object := range got {
			if !want[object] {
				t.Errorf("embedded migrations are missing %q", object)
			}
		}
	}
	bootstrapStatements, err := splitSQLStatements(string(bootstrap))
	if err != nil {
		t.Fatal(err)
	}
	bootstrapShape, err := schemaShapeFromStatements(bootstrapStatements)
	if err != nil {
		t.Fatal(err)
	}
	embeddedShape, err := CurrentSchemaShape()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bootstrapShape, embeddedShape) {
		t.Fatal("scripts/schema.sql shape differs from the current embedded migration shape")
	}
}

func matchSet(pattern *regexp.Regexp, value string) map[string]bool {
	result := map[string]bool{}
	for _, match := range pattern.FindAllString(value, -1) {
		if strings.HasPrefix(match, "INDEX ") {
			match = "KEY " + strings.TrimPrefix(match, "INDEX ")
		}
		result[match] = true
	}
	return result
}
