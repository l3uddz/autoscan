package migrate

import (
	"database/sql"
	"embed"
	"strings"
	"testing"

	// sqlite3 driver
	_ "modernc.org/sqlite"
)

//go:embed testdata
var testMigrations embed.FS

func TestMigrateReturnsCommitError(t *testing.T) {
	// The migration inserts a row that breaks a deferred foreign key.
	// SQLite checks a deferred foreign key at COMMIT, so only the commit fails.
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	mg, err := New(db, "testdata/deferred_fk")
	if err != nil {
		t.Fatal(err)
	}

	err = mg.Migrate(&testMigrations, "test")
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("Migrate returned %v, want the commit error", err)
	}
}

func TestMigrateReturnsVersionsReadError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// A view in place of the schema_migration table returns one version and
	// then fails, because abs of the smallest integer overflows. The failure
	// happens while the rows are read, so only rows.Err reports it.
	const view = `CREATE VIEW schema_migration (component, version) AS
		SELECT 'test', 1
		UNION ALL
		SELECT 'test', abs(-9223372036854775808)`
	if _, err := db.Exec(view); err != nil {
		t.Fatal(err)
	}

	mg, err := New(db, "testdata/deferred_fk")
	if err != nil {
		t.Fatal(err)
	}

	err = mg.Migrate(&testMigrations, "test")
	if err == nil || !strings.Contains(err.Error(), "versions") {
		t.Fatalf("Migrate returned %v, want the error from reading the versions", err)
	}
}
