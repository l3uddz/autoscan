package main

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
)

// openDatabase opens the SQLite datastore at path and checks that it works.
// The file URL escapes path, so a "?" or a "#" stays part of the file name.
// The settings are for a second process, such as the sqlite3 shell or a
// backup tool, and for an image without a writable /tmp:
//   - busy_timeout waits up to 5 seconds for a lock that another process
//     holds, instead of failing at once.
//   - _txlock=immediate takes the write lock when a write transaction
//     starts, so the busy timeout applies to it. A read-only transaction
//     starts without it.
//   - temp_store keeps temporary tables and indexes in memory.
func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	query := url.Values{
		"_pragma": {"busy_timeout(5000)", "temp_store(MEMORY)"},
		"_txlock": {"immediate"},
	}
	dsn := url.URL{Scheme: "file", Path: abs, RawQuery: query.Encode()}

	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	// One connection serializes the writes of this process.
	db.SetMaxOpenConns(1)

	// sql.Open does not open the file, so check it here.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
