package migrate

import (
	"context"
	"testing"

	"thing-connect/internal/testenv"
)

func TestRefinedRoleDefaultsPreserveLocalEditsAndNameCollisions(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	type row struct {
		ID, Name, VoiceID, Prompt string
		Revision                  int64
		UpdatedBy                 int64
	}
	var saved []row
	if err := db.Select(&saved, "SELECT id,name,voice_id AS voiceid,prompt,revision,updated_by AS updatedby FROM ai_role_types WHERE id IN ('preset-001','preset-002','preset-003','preset-004')"); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 4 {
		t.Fatalf("missing fixtures: %d", len(saved))
	}
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Rollback restores the original catalog after every assertion, including failures.
	if _, err = tx.Exec("DELETE FROM ai_role_types WHERE id IN ('preset-001','preset-002','preset-003','preset-004')"); err != nil {
		t.Fatal(err)
	}
	original, err := statementsFromFiles("migrations/admin/002_ai_role_types.sql")
	if err != nil {
		t.Fatal(err)
	}
	refined, err := statementsFromFiles("migrations/admin/003_xiaotai_role_defaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range original[1:5] {
		if _, err = tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec("UPDATE ai_role_types SET prompt='管理员手写设定' WHERE id='preset-002'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE ai_role_types SET voice_id='administrator-voice' WHERE id='preset-003'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO ai_role_types (id,name,prompt) VALUES ('collision-test','晨光青年','已有自建类型')"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range refined[:4] {
		if _, err = tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var got row
	if err = tx.Get(&got, "SELECT id,name,voice_id AS voiceid,prompt,revision,updated_by AS updatedby FROM ai_role_types WHERE id='preset-001'"); err != nil {
		t.Fatal(err)
	}
	if got.Name != "海风知己" || got.Revision != 2 {
		t.Fatalf("untouched default not refined: %+v", got)
	}
	var text string
	if err = tx.Get(&text, "SELECT prompt FROM ai_role_types WHERE id='preset-002'"); err != nil || text != "管理员手写设定" {
		t.Fatalf("prompt overwritten: %q %v", text, err)
	}
	if err = tx.Get(&text, "SELECT voice_id FROM ai_role_types WHERE id='preset-003'"); err != nil || text != "administrator-voice" {
		t.Fatalf("voice overwritten: %q %v", text, err)
	}
	if err = tx.Get(&text, "SELECT name FROM ai_role_types WHERE id='preset-004'"); err != nil || text != "阳光青年" {
		t.Fatalf("name collision not preserved: %q %v", text, err)
	}
	for _, statement := range refined[:4] {
		if _, err = tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	if err = tx.Get(&revision, "SELECT revision FROM ai_role_types WHERE id='preset-001'"); err != nil || revision != 2 {
		t.Fatalf("replay changed revision: %d %v", revision, err)
	}
}

func TestCompleteRoleCatalogPublicationAndReplay(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		where string
		want  int
	}{
		{"id LIKE 'preset-%' OR id LIKE 'catalog-%'", 58},
		{"status='published' AND id LIKE 'preset-%'", 43},
		{"status='published' AND voice_model='seed-tts-2.0'", 30},
		{"status='published' AND voice_model='cosyvoice-v3-flash'", 20},
		{"id LIKE 'catalog-%' AND status='published'", 7},
		{"language<>'zh'", 0},
		{"status='offline'", 8},
		{"status='published' AND match_kind='substitute'", 21},
		{"prompt=''", 2},
		{"status='published' AND (voice_id='' OR prompt='' OR voice_model NOT IN ('cosyvoice-v3-flash','seed-tts-2.0'))", 0},
	} {
		var n int
		if err := db.Get(&n, "SELECT COUNT(*) FROM ai_role_types WHERE "+c.where); err != nil || n != c.want {
			t.Fatalf("%s count=%d want=%d err=%v", c.where, n, c.want, err)
		}
	}
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE ai_role_types SET prompt='本地定制',revision=revision+1,updated_by=1 WHERE id='preset-002'"); err != nil {
		t.Fatal(err)
	}
	statements, err := statementsFromFiles("migrations/admin/005_role_catalog_models.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range statements[3:] {
		if _, err = tx.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := statementsFromFiles("migrations/admin/006_remove_beta_role_types.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range removed {
		if _, err = tx.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var prompt string
	if err = tx.Get(&prompt, "SELECT prompt FROM ai_role_types WHERE id='preset-002'"); err != nil || prompt != "本地定制" {
		t.Fatalf("local edit lost: %q %v", prompt, err)
	}
	var n int
	if err = tx.Get(&n, "SELECT COUNT(*) FROM ai_role_types"); err != nil || n != 58 {
		t.Fatalf("replay duplicate count=%d %v", n, err)
	}
}

func TestSubstituteVoiceDefaultsPreservePromptAndAdministratorChanges(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var originalPrompt string
	if err = tx.Get(&originalPrompt, "SELECT prompt FROM ai_role_types WHERE id='preset-001'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE ai_role_types SET status='draft',voice_id='',voice_model='cosyvoice-v3-flash',match_kind='missing',revision=4,updated_by=0 WHERE id IN ('preset-001','preset-005','preset-006')"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE ai_role_types SET prompt='管理员自定义内容' WHERE id='preset-005'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE ai_role_types SET updated_by=1 WHERE id='preset-006'"); err != nil {
		t.Fatal(err)
	}
	statements, err := statementsFromFiles("migrations/admin/007_enable_substitute_role_voices.sql")
	if err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		for _, stmt := range statements {
			if _, err = tx.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	var got struct {
		Voice, Prompt, Status, Kind string
		Revision                    int
	}
	if err = tx.Get(&got, "SELECT voice_id AS voice,prompt,status,match_kind AS kind,revision FROM ai_role_types WHERE id='preset-001'"); err != nil {
		t.Fatal(err)
	}
	if got.Voice != "longantai_v3" || got.Status != "published" || got.Kind != "substitute" || got.Revision != 5 || got.Prompt != originalPrompt {
		t.Fatalf("substitution or replay invalid: %+v", got)
	}
	for _, id := range []string{"preset-005", "preset-006"} {
		var status string
		if err = tx.Get(&status, "SELECT status FROM ai_role_types WHERE id=?", id); err != nil || status != "draft" {
			t.Fatalf("administrator changes overwritten for %s: %s %v", id, status, err)
		}
	}
}
