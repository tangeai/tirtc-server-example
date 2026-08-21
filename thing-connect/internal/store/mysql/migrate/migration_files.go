package migrate

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// migrationSQL is packaged into every service binary, but only explicit
// installer/migration entry points execute it. Runtime services never run DDL.
//
//go:embed migrations/*/*.sql
var migrationSQL embed.FS

type TableShape struct {
	Columns map[string]ColumnShape
	Indexes map[string]IndexShape
}

type ColumnShape struct {
	ColumnType           string
	Nullable             bool
	Default              string
	AutoIncrement        bool
	OnUpdate             string
	GenerationExpression string
	GeneratedKind        string
}

type IndexPart struct {
	Column string
	Prefix int64
	Desc   bool
}

type IndexShape struct {
	Unique  bool
	Type    string
	Visible bool
	Parts   []IndexPart
}

const nullColumnDefault = "<NULL>"

// CurrentSchemaShape derives the complete current column/type and named-index
// contract from the same SQL files used by migrations.
func CurrentSchemaShape() (map[string]TableShape, error) {
	return SchemaShapeForVersions(CurrentMigrationVersions(), true)
}

// SchemaShapeForVersions derives the exact schema contract represented by a
// migration ledger. includeInstallationState accounts for the ownership row,
// which can exist before admin migration 4 when an interrupted fresh install
// has claimed an otherwise empty database.
func SchemaShapeForVersions(versions map[string]int, includeInstallationState bool) (map[string]TableShape, error) {
	current := CurrentMigrationVersions()
	for component, version := range versions {
		if version < 0 || version > current[component] {
			return nil, fmt.Errorf("unsupported %s schema version %d", component, version)
		}
	}
	paths := []string{"migrations/shared/schema_migrations.sql"}
	if versions["core"] >= 1 {
		paths = append(paths, coreV1MigrationFiles...)
	}
	if versions["core"] >= 2 {
		paths = append(paths, "migrations/core/002_user_device_sorting.sql")
	}
	if versions["admin"] >= 1 {
		paths = append(paths, "migrations/admin/001_schema.sql")
	}
	if versions["admin"] >= 2 {
		paths = append(paths, "migrations/admin/002_job_leases.sql")
	}
	if versions["admin"] >= 3 {
		paths = append(paths, "migrations/admin/003_plaintext_secrets.sql")
	}
	if versions["admin"] >= 4 || includeInstallationState {
		paths = append(paths, "migrations/admin/004_installation_state.sql")
	}
	statements, err := statementsFromFiles(paths...)
	if err != nil {
		return nil, err
	}
	result := map[string]TableShape{}
	for _, statement := range statements {
		name, shape, ok, parseErr := parseCreateTableShape(statement)
		if parseErr != nil {
			return nil, parseErr
		}
		if ok {
			result[name] = shape
			continue
		}
		if parseErr := applyAlterTableShape(result, statement); parseErr != nil {
			return nil, parseErr
		}
	}
	return result, nil
}

func parseCreateTableShape(statement string) (string, TableShape, bool, error) {
	const prefix = "CREATE TABLE IF NOT EXISTS "
	trimmed := strings.TrimSpace(statement)
	if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
		return "", TableShape{}, false, nil
	}
	remainder := strings.TrimSpace(trimmed[len(prefix):])
	open := strings.IndexByte(remainder, '(')
	engine := strings.LastIndex(strings.ToUpper(remainder), ") ENGINE")
	if open <= 0 || engine <= open {
		return "", TableShape{}, false, fmt.Errorf("unsupported CREATE TABLE statement")
	}
	name := strings.Trim(strings.TrimSpace(remainder[:open]), "`")
	shape := TableShape{Columns: map[string]ColumnShape{}, Indexes: map[string]IndexShape{}}
	for _, rawClause := range splitTopLevel(remainder[open+1 : engine]) {
		clause := strings.TrimSpace(rawClause)
		upper := strings.ToUpper(clause)
		switch {
		case strings.HasPrefix(upper, "PRIMARY KEY"):
			index, err := parseIndexClause(clause)
			if err != nil {
				return "", TableShape{}, false, fmt.Errorf("%s: %w", name, err)
			}
			shape.Indexes["PRIMARY"] = index
		case strings.HasPrefix(upper, "UNIQUE KEY "), strings.HasPrefix(upper, "KEY "), strings.HasPrefix(upper, "INDEX "):
			indexName, index, err := namedIndexShape(clause)
			if err != nil {
				return "", TableShape{}, false, fmt.Errorf("%s: %w", name, err)
			}
			shape.Indexes[indexName] = index
		default:
			column, columnShape, primary, unique, err := parseColumnClause(clause)
			if err != nil {
				return "", TableShape{}, false, fmt.Errorf("%s: %w", name, err)
			}
			shape.Columns[column] = columnShape
			if primary {
				shape.Indexes["PRIMARY"] = IndexShape{Unique: true, Type: "BTREE", Visible: true, Parts: []IndexPart{{Column: column}}}
			}
			if unique {
				shape.Indexes[column] = IndexShape{Unique: true, Type: "BTREE", Visible: true, Parts: []IndexPart{{Column: column}}}
			}
		}
	}
	if primary, ok := shape.Indexes["PRIMARY"]; ok {
		for _, part := range primary.Parts {
			column := shape.Columns[part.Column]
			column.Nullable = false
			shape.Columns[part.Column] = column
		}
	}
	return name, shape, true, nil
}

func applyAlterTableShape(tables map[string]TableShape, statement string) error {
	const prefix = "ALTER TABLE "
	trimmed := strings.TrimSpace(statement)
	if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
		return nil
	}
	remainder := strings.TrimSpace(trimmed[len(prefix):])
	space := strings.IndexAny(remainder, " \t\r\n")
	if space <= 0 {
		return fmt.Errorf("unsupported ALTER TABLE statement")
	}
	name := strings.Trim(remainder[:space], "`")
	shape, ok := tables[name]
	if !ok {
		return fmt.Errorf("ALTER TABLE references unknown table %s", name)
	}
	for _, rawClause := range splitTopLevel(strings.TrimSpace(remainder[space:])) {
		clause := strings.TrimSpace(rawClause)
		upper := strings.ToUpper(clause)
		switch {
		case strings.HasPrefix(upper, "ADD COLUMN "):
			column, columnShape, _, _, err := parseColumnClause(strings.TrimSpace(clause[len("ADD COLUMN "):]))
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			shape.Columns[column] = columnShape
		case strings.HasPrefix(upper, "ADD KEY "), strings.HasPrefix(upper, "ADD INDEX "), strings.HasPrefix(upper, "ADD UNIQUE KEY "):
			indexName, index, err := namedIndexShape(strings.TrimSpace(clause[len("ADD "):]))
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			shape.Indexes[indexName] = index
		}
	}
	tables[name] = shape
	return nil
}

func parseColumnClause(clause string) (string, ColumnShape, bool, bool, error) {
	fields := definitionFields(clause)
	if len(fields) < 2 {
		return "", ColumnShape{}, false, false, fmt.Errorf("invalid column clause")
	}
	name := strings.Trim(fields[0], "`")
	shape := ColumnShape{ColumnType: strings.ToLower(fields[1]), Nullable: true, Default: nullColumnDefault}
	primary := false
	unique := false
	for index := 2; index < len(fields); index++ {
		switch strings.ToUpper(fields[index]) {
		case "NOT":
			if index+1 < len(fields) && strings.EqualFold(fields[index+1], "NULL") {
				shape.Nullable = false
				index++
			}
		case "DEFAULT":
			if index+1 >= len(fields) {
				return "", ColumnShape{}, false, false, fmt.Errorf("column %s has invalid DEFAULT", name)
			}
			shape.Default = normalizeDefault(fields[index+1])
			index++
		case "AUTO_INCREMENT":
			shape.AutoIncrement = true
		case "ON":
			if index+2 < len(fields) && strings.EqualFold(fields[index+1], "UPDATE") {
				shape.OnUpdate = normalizeExpression(fields[index+2])
				index += 2
			}
		case "AS":
			if index+1 >= len(fields) {
				return "", ColumnShape{}, false, false, fmt.Errorf("column %s has invalid generation expression", name)
			}
			shape.GenerationExpression = normalizeExpression(fields[index+1])
			if index+2 < len(fields) {
				kind := strings.ToUpper(fields[index+2])
				if kind == "STORED" || kind == "VIRTUAL" {
					shape.GeneratedKind = kind
				}
			}
		case "PRIMARY":
			primary = index+1 < len(fields) && strings.EqualFold(fields[index+1], "KEY")
			if primary {
				shape.Nullable = false
			}
		case "UNIQUE":
			unique = true
		}
	}
	return name, shape, primary, unique, nil
}

func namedIndexShape(clause string) (string, IndexShape, error) {
	fields := definitionFields(clause)
	if len(fields) < 2 {
		return "", IndexShape{}, fmt.Errorf("invalid index clause")
	}
	nameIndex := 1
	if strings.EqualFold(fields[0], "UNIQUE") {
		nameIndex = 2
	}
	if len(fields) <= nameIndex {
		return "", IndexShape{}, fmt.Errorf("invalid index clause")
	}
	index, err := parseIndexClause(clause)
	return strings.Trim(fields[nameIndex], "`"), index, err
}

func parseIndexClause(clause string) (IndexShape, error) {
	upper := strings.ToUpper(strings.TrimSpace(clause))
	open := strings.IndexByte(clause, '(')
	close := strings.LastIndexByte(clause, ')')
	if open < 0 || close <= open {
		return IndexShape{}, fmt.Errorf("invalid index column list")
	}
	shape := IndexShape{
		Unique: strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "UNIQUE "),
		Type:   "BTREE", Visible: true,
	}
	for _, rawPart := range splitTopLevel(clause[open+1 : close]) {
		part := strings.TrimSpace(rawPart)
		prefix := int64(0)
		if prefixOpen := strings.LastIndexByte(part, '('); prefixOpen > 0 && strings.HasSuffix(part, ")") {
			if _, err := fmt.Sscan(part[prefixOpen+1:len(part)-1], &prefix); err != nil {
				return IndexShape{}, fmt.Errorf("invalid index prefix %q", part)
			}
			part = part[:prefixOpen]
		}
		part = strings.Trim(strings.TrimSpace(part), "`")
		if part == "" {
			return IndexShape{}, fmt.Errorf("empty index column")
		}
		shape.Parts = append(shape.Parts, IndexPart{Column: part, Prefix: prefix})
	}
	return shape, nil
}

func definitionFields(value string) []string {
	var result []string
	start := -1
	depth := 0
	var quote byte
	for index := 0; index <= len(value); index++ {
		var ch byte
		if index < len(value) {
			ch = value[index]
		}
		if quote != 0 {
			if ch == '\\' && quote != '`' && index+1 < len(value) {
				index++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if index < len(value) {
			switch ch {
			case '\'', '"', '`':
				quote = ch
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if index == len(value) || ((ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n') && depth == 0) {
			if start >= 0 {
				result = append(result, value[start:index])
				start = -1
			}
		} else if start < 0 {
			start = index
		}
	}
	return result
}

func normalizeDefault(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "NULL") {
		return nullColumnDefault
	}
	if len(trimmed) >= 2 && ((trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') || (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"')) {
		return trimmed[1 : len(trimmed)-1]
	}
	if strings.EqualFold(trimmed, "CURRENT_TIMESTAMP") || strings.EqualFold(trimmed, "CURRENT_TIMESTAMP()") {
		return "current_timestamp"
	}
	return trimmed
}

func normalizeExpression(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "_utf8mb4", "")
	value = strings.ReplaceAll(value, `\'`, `'`)
	value = strings.ReplaceAll(value, "(", "")
	value = strings.ReplaceAll(value, ")", "")
	return strings.Join(strings.Fields(value), "")
}

// InformationSchemaColumnShape normalizes MySQL 8 metadata into the same
// semantic representation as the embedded SQL parser.
func InformationSchemaColumnShape(columnType, nullable string, defaultValid bool, defaultValue, extra, generationExpression string) ColumnShape {
	shape := ColumnShape{
		ColumnType: strings.ToLower(columnType), Nullable: strings.EqualFold(nullable, "YES"),
		Default: nullColumnDefault, GenerationExpression: normalizeExpression(generationExpression),
	}
	if defaultValid {
		shape.Default = normalizeDefault(defaultValue)
	}
	lowerExtra := strings.ToLower(extra)
	shape.AutoIncrement = strings.Contains(lowerExtra, "auto_increment")
	if strings.Contains(lowerExtra, "stored generated") {
		shape.GeneratedKind = "STORED"
	} else if strings.Contains(lowerExtra, "virtual generated") {
		shape.GeneratedKind = "VIRTUAL"
	}
	if at := strings.Index(lowerExtra, "on update "); at >= 0 {
		shape.OnUpdate = normalizeExpression(lowerExtra[at+len("on update "):])
	}
	return shape
}

func splitTopLevel(value string) []string {
	var result []string
	start := 0
	depth := 0
	var quote byte
	for index := 0; index < len(value); index++ {
		ch := value[index]
		if quote != 0 {
			if ch == '\\' && quote != '`' && index+1 < len(value) {
				index++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, value[start:index])
				start = index + 1
			}
		}
	}
	result = append(result, value[start:])
	return result
}

// EnsureInstallationState creates only the durable installer ownership table.
// The installer calls it after its zero-write assessment and MySQL named lock;
// it is not a general schema migration entry point.
func EnsureInstallationState(ctx context.Context, database *sqlx.DB) error {
	statements, err := statementsFromFiles("migrations/admin/004_installation_state.sql")
	if err != nil {
		return err
	}
	for index, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil && !IsIgnorableDDLError(err) {
			return fmt.Errorf("create installation state statement %d: %w", index+1, err)
		}
	}
	return nil
}

func statementsFromFiles(paths ...string) ([]string, error) {
	var statements []string
	for _, path := range paths {
		raw, err := migrationSQL.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %s: %w", path, err)
		}
		parsed, err := splitSQLStatements(string(raw))
		if err != nil {
			return nil, fmt.Errorf("parse embedded migration %s: %w", path, err)
		}
		statements = append(statements, parsed...)
	}
	return statements, nil
}

// splitSQLStatements accepts the controlled MySQL migration subset used by
// this repository. It splits only on semicolons outside quoted strings and
// comments, so credentials or comments can never turn one version into an
// accidental multi-statement driver call.
func splitSQLStatements(source string) ([]string, error) {
	var statements []string
	var current strings.Builder
	var quote byte
	lineComment := false
	blockComment := false
	for index := 0; index < len(source); index++ {
		ch := source[index]
		next := byte(0)
		if index+1 < len(source) {
			next = source[index+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
				current.WriteByte(ch)
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			current.WriteByte(ch)
			if ch == '\\' && quote != '`' && next != 0 {
				index++
				current.WriteByte(source[index])
				continue
			}
			if ch == quote {
				if next == quote && quote != '`' {
					index++
					current.WriteByte(source[index])
					continue
				}
				quote = 0
			}
			continue
		}
		switch {
		case ch == '\'', ch == '"', ch == '`':
			quote = ch
			current.WriteByte(ch)
		case ch == '#':
			lineComment = true
		case ch == '-' && next == '-' && (index+2 == len(source) || source[index+2] == ' ' || source[index+2] == '\t' || source[index+2] == '\r' || source[index+2] == '\n'):
			lineComment = true
			index++
		case ch == '/' && next == '*':
			blockComment = true
			index++
		case ch == ';':
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	if quote != 0 || blockComment {
		return nil, fmt.Errorf("unterminated quote or block comment")
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}
	if len(statements) == 0 {
		return nil, fmt.Errorf("migration file contains no statements")
	}
	return statements, nil
}
