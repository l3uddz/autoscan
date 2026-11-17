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
	_, err := tx.Exec(sqlUpsert, scan.Folder, scan.Priority, scan.Time)
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
		return remaining, fmt.Errorf("get remaining scans: %v: %w", err, autoscan.ErrFatal)
	}

	return remaining, nil
}

const sqlGetAvailableScan = `
SELECT folder, priority, time, revision FROM scan
WHERE time < ?
ORDER BY priority DESC, time ASC
LIMIT 1
`

func (store *datastore) GetAvailableScan(minAge time.Duration) (queuedScan, error) {
	row := store.db.QueryRow(sqlGetAvailableScan, now().Add(-1*minAge))

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

const sqlGetAll = `
SELECT folder, priority, time FROM scan
`

func (store *datastore) GetAll() (scans []autoscan.Scan, err error) {
	rows, err := store.db.Query(sqlGetAll)
	if err != nil {
		return scans, err
	}

	defer rows.Close()
	for rows.Next() {
		scan := autoscan.Scan{}
		err = rows.Scan(&scan.Folder, &scan.Priority, &scan.Time)
		if err != nil {
			return scans, err
		}

		scans = append(scans, scan)
	}

	return scans, rows.Err()
}

const sqlDelete = `
DELETE FROM scan WHERE folder = ? AND revision = ?
`

func (store *datastore) Delete(scan queuedScan) error {
	_, err := store.db.Exec(sqlDelete, scan.Folder, scan.revision)
	if err != nil {
		return fmt.Errorf("delete: %s: %w", err, autoscan.ErrFatal)
	}

	return nil
}

var now = time.Now
