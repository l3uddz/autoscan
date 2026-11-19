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

// errNoScans means that a target has no scan ready for delivery.
var errNoScans = errors.New("no scans currently available")

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

	store := &datastore{db: db}
	if err := store.storeScanTimesInUTC(); err != nil {
		return nil, fmt.Errorf("store scan times in UTC: %w", err)
	}
	return store, nil
}

// The driver stores a time as text, and SQL compares and sorts that text.
// The text follows time order only when every time uses the same offset, so
// the datastore stores and compares scan times in UTC.
const storedUTCSuffix = " +0000 UTC"

// storeScanTimesInUTC rewrites the scan times that earlier releases stored
// with the UTC offset of the local zone.
func (store *datastore) storeScanTimesInUTC() error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT folder, time FROM scan WHERE time NOT LIKE ?`, "%"+storedUTCSuffix)
	if err != nil {
		return err
	}
	var scans []autoscan.Scan
	for rows.Next() {
		var scan autoscan.Scan
		if err := rows.Scan(&scan.Folder, &scan.Time); err != nil {
			rows.Close()
			return err
		}
		scans = append(scans, scan)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	// Close the rows before the updates, because the connection pool can
	// have only one connection.
	if err := rows.Close(); err != nil {
		return err
	}

	for _, scan := range scans {
		if _, err := tx.Exec(`UPDATE scan SET time = ? WHERE folder = ?`, scan.Time.UTC(), scan.Folder); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	if _, err := tx.Exec(sqlUpsert, scan.Folder, scan.Priority, scan.Time.UTC()); err != nil {
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

	defer tx.Rollback()

	for _, scan := range scans {
		if err = store.upsert(tx, scan); err != nil {
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
	row := store.db.QueryRow(sqlGetAvailableScan, target, olderThan.UTC())

	var scan queuedScan
	err := row.Scan(&scan.Folder, &scan.Priority, &scan.Time, &scan.revision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return scan, errNoScans
	case err != nil:
		return scan, fmt.Errorf("get matching: %w", err)
	}

	return scan, nil
}
