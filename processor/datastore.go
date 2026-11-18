package processor

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"

	// sqlite3 driver
	_ "modernc.org/sqlite"
)

type datastore struct {
	db *sql.DB
}

type queuedScan struct {
	autoscan.Scan
	revision string
}

var (
	//go:embed migrations
	migrations embed.FS
)

func newDatastore(db *sql.DB, mg *migrate.Migrator) (*datastore, error) {
	// migrations
	if err := mg.Migrate(&migrations, "processor"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &datastore{db: db}, nil
}

const sqlUpsert = `
INSERT INTO scan (folder, priority, time, revision)
VALUES (?, ?, ?, hex(randomblob(16)))
ON CONFLICT (folder) DO UPDATE SET
	priority = MAX(excluded.priority, scan.priority),
	time = excluded.time,
	revision = excluded.revision
`

func (store *datastore) upsert(tx *sql.Tx, scan autoscan.Scan) error {
	if _, err := tx.Exec(sqlUpsert, scan.Folder, scan.Priority, scan.Time); err != nil {
		return err
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO scan_delivery (target, folder)
		SELECT id, ? FROM scan_target
	`, scan.Folder)
	return err
}

func (store *datastore) Upsert(scans []autoscan.Scan) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}

	for _, scan := range scans {
		if err = store.upsert(tx, scan); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return fmt.Errorf("%w (rollback: %w)", err, rollbackErr)
			}

			return err
		}
	}

	return tx.Commit()
}

const sqlGetScansRemaining = `SELECT COUNT(folder) FROM scan`

func (store *datastore) GetScansRemaining() (int, error) {
	row := store.db.QueryRow(sqlGetScansRemaining)

	// COUNT always returns one row.
	remaining := 0
	if err := row.Scan(&remaining); err != nil {
		return remaining, fmt.Errorf("get remaining scans: %w", err)
	}

	return remaining, nil
}

const sqlGetAvailableScan = `
SELECT scan.folder, priority, time, revision FROM scan_delivery
JOIN scan ON scan.folder = scan_delivery.folder
WHERE target = ? AND time < ?
ORDER BY priority DESC, time ASC
LIMIT 1
`

func (store *datastore) GetAvailableScan(target string, olderThan time.Time) (queuedScan, error) {
	row := store.db.QueryRow(sqlGetAvailableScan, target, olderThan)

	var scan queuedScan
	err := row.Scan(&scan.Folder, &scan.Priority, &scan.Time, &scan.revision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return scan, autoscan.ErrNoScans
	case err != nil:
		return scan, fmt.Errorf("get matching: %s: %w", err, autoscan.ErrFatal)
	}

	return scan, nil
}
