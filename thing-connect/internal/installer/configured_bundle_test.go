package installer

import (
	"context"
	"testing"
)

func TestDatabaseTargetIdentityIgnoresCredentialsButNotTarget(t *testing.T) {
	first, err := databaseTargetIdentity("migration:secret@tcp(db.internal:3306)/thing_connect?parseTime=true")
	if err != nil {
		t.Fatal(err)
	}
	second, err := databaseTargetIdentity("runtime:other@tcp(db.internal:3306)/thing_connect?charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same target identities differ: %q != %q", first, second)
	}
	different, err := databaseTargetIdentity("runtime:other@tcp(db.internal:3306)/other_database")
	if err != nil {
		t.Fatal(err)
	}
	if different == first {
		t.Fatal("different database names produced the same identity")
	}
}

func TestValidateConfiguredServiceBundleAcceptsRequiredOnlyRevision(t *testing.T) {
	options := testOptions(t)
	if _, err := NewFileBundleStore(options).Publish(context.Background(), testDraft(), "operation-1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfiguredServiceBundle(options.DeployRoot); err != nil {
		t.Fatalf("required-only bundle: %v", err)
	}
}
