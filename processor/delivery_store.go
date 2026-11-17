package processor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type retryState struct {
	delay time.Duration
	next  time.Time
}

func (store *datastore) configureTargets(targets []Target) error {
	ids := make([]string, len(targets))
	seen := make(map[string]bool, len(targets))
	for i, target := range targets {
		if target.ID == "" || seen[target.ID] {
			return fmt.Errorf("target %d needs a unique, nonempty ID", i+1)
		}
		seen[target.ID] = true
		ids[i] = target.ID
	}
	configured, err := json.Marshal(ids)
	if err != nil {
		return err
	}

	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Only new targets get a delivery for every queued scan. Existing targets
	// keep their pending rows, so a restart cannot replay completed deliveries.
	if _, err := tx.Exec(`
		INSERT INTO scan_delivery (target, folder)
		SELECT value, folder FROM json_each(?) CROSS JOIN scan
		WHERE value NOT IN (SELECT id FROM scan_target)
	`, string(configured)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO scan_target (id) SELECT value FROM json_each(?)
	`, string(configured)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM scan_target WHERE id NOT IN (SELECT value FROM json_each(?));
		DELETE FROM scan_delivery WHERE target NOT IN (SELECT id FROM scan_target);
		DELETE FROM scan
		WHERE EXISTS (SELECT 1 FROM scan_target)
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE folder = scan.folder)
	`, string(configured)); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *datastore) retry(target string) (retryState, error) {
	var retry retryState
	var next int64
	err := store.db.QueryRow(`
		SELECT retry_delay, next_retry FROM scan_target WHERE id = ?
	`, target).Scan(&retry.delay, &next)
	retry.next = retryTime(next)
	return retry, err
}

func (store *datastore) recordFailure(target string, delay time.Duration, next time.Time, message string) error {
	_, err := store.db.Exec(`
		UPDATE scan_target SET retry_delay = ?, next_retry = ?, last_error = ? WHERE id = ?
	`, delay, next.UnixNano(), message, target)
	return err
}

const sqlClearRetry = `UPDATE scan_target SET retry_delay = 0, next_retry = 0, last_error = '' WHERE id = ? AND retry_delay > 0`

func (store *datastore) clearIdleRetry(target string) error {
	_, err := store.db.Exec(sqlClearRetry+`
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE target = scan_target.id)
	`, target)
	return err
}

func (store *datastore) completeDelivery(target string, scan queuedScan) (int64, error) {
	tx, err := store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM scan_delivery WHERE target = ? AND folder = ?
		AND EXISTS (SELECT 1 FROM scan WHERE folder = scan_delivery.folder AND revision = ?)
	`, target, scan.Folder, scan.revision); err != nil {
		return 0, err
	}
	deleted, err := tx.Exec(`
		DELETE FROM scan WHERE folder = ? AND revision = ?
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE folder = scan.folder)
	`, scan.Folder, scan.revision)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(sqlClearRetry, target); err != nil {
		return 0, err
	}
	count, err := deleted.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func targetStatuses(ctx context.Context, tx *sql.Tx) ([]TargetStatus, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, last_error, next_retry,
			(SELECT COUNT(*) FROM scan_delivery WHERE target = scan_target.id)
		FROM scan_target ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []TargetStatus
	for rows.Next() {
		var status TargetStatus
		var next int64
		if err := rows.Scan(&status.ID, &status.LastError, &next, &status.Pending); err != nil {
			return nil, err
		}
		status.NextRetry = retryTime(next)
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}

func retryTime(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}
