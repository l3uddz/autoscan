package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenDatabaseKeepsPathCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto?scan #1 %41.db")
	db, err := openDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file %q: %v", path, err)
	}
}

func TestOpenDatabaseSetsPragmas(t *testing.T) {
	db, err := openDatabase(t.Context(), filepath.Join(t.TempDir(), "autoscan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var busyTimeout, tempStore int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA temp_store`).Scan(&tempStore); err != nil {
		t.Fatal(err)
	}
	// temp_store 2 is MEMORY.
	if busyTimeout != 5000 || tempStore != 2 {
		t.Errorf("busy_timeout, temp_store = %d, %d, want 5000, 2", busyTimeout, tempStore)
	}
}

func TestOpenDatabaseWaitsForAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autoscan.db")
	db, err := openDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (v INTEGER)`); err != nil {
		t.Fatal(err)
	}

	// Another process, such as the sqlite3 shell, holds the write lock.
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	lock, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		lock.Commit()
	}()

	// The transaction reads before it writes, as storeScanTimesInUTC does.
	// A deferred transaction would then fail at once instead of waiting.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin returned %v, want it to wait for the lock", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO t VALUES (2)`); err != nil {
		t.Fatalf("Exec returned %v, want it to wait for the lock", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDatabaseFailsForMissingDirectory(t *testing.T) {
	if _, err := openDatabase(t.Context(), filepath.Join(t.TempDir(), "missing", "autoscan.db")); err == nil {
		t.Error("openDatabase returned nil error for a missing directory")
	}
}
