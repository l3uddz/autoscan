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
	// Remove the targets that are no longer configured, their deliveries,
	// and the scans that no target needs. Without targets, the scans stay
	// queued for the next start.
	if _, err := tx.Exec(`
		DELETE FROM scan_target WHERE id NOT IN (SELECT value FROM json_each(?))
	`, string(configured)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM scan_delivery WHERE target NOT IN (SELECT id FROM scan_target)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM scan
		WHERE EXISTS (SELECT 1 FROM scan_target)
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE folder = scan.folder)
	`); err != nil {
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

// sqlResetRetry ends the retry delay of a target. It keeps the last error,
// so /status shows it until a scan reaches the target.
const sqlResetRetry = `UPDATE scan_target SET retry_delay = 0, next_retry = 0 WHERE id = ? AND retry_delay > 0`

// sqlClearRetry ends the retry delay and clears the last error of a target.
const sqlClearRetry = `UPDATE scan_target SET retry_delay = 0, next_retry = 0, last_error = '' WHERE id = ?`

// clearIdleRetry ends the retry delay of a target that has no deliveries.
// It keeps the last error.
func (store *datastore) clearIdleRetry(target string) error {
	_, err := store.db.Exec(`
		UPDATE scan_target SET retry_delay = 0, next_retry = 0
		WHERE id = ? AND retry_delay > 0
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE target = scan_target.id)
	`, target)
	return err
}

// deliveryResult reports what completeDelivery removed.
type deliveryResult struct {
	// removed reports whether the delivery to the target was removed. A newer
	// event for the folder keeps the delivery for its new revision.
	removed bool

	// scans is the number of scans that no target needs any more.
	scans int64
}

// completeDelivery removes the delivery of scan to target after the target
// received it, and clears the retry state of the target.
func (store *datastore) completeDelivery(target string, scan queuedScan) (deliveryResult, error) {
	return store.removeDelivery(target, scan, sqlClearRetry)
}

// dropDelivery removes the delivery of scan to target when the target did
// not receive it: the target kept rejecting it, or the folder is in no
// library of the target. It ends the retry delay and keeps the last error.
func (store *datastore) dropDelivery(target string, scan queuedScan) (deliveryResult, error) {
	return store.removeDelivery(target, scan, sqlResetRetry)
}

// removeDelivery removes the delivery of scan to target, removes the scan if
// no target needs it, and runs retrySQL for target.
func (store *datastore) removeDelivery(target string, scan queuedScan, retrySQL string) (deliveryResult, error) {
	tx, err := store.db.Begin()
	if err != nil {
		return deliveryResult{}, err
	}
	defer tx.Rollback()

	delivery, err := tx.Exec(`
		DELETE FROM scan_delivery WHERE target = ? AND folder = ?
		AND EXISTS (SELECT 1 FROM scan WHERE folder = scan_delivery.folder AND revision = ?)
	`, target, scan.Folder, scan.revision)
	if err != nil {
		return deliveryResult{}, err
	}
	deleted, err := tx.Exec(`
		DELETE FROM scan WHERE folder = ? AND revision = ?
		AND NOT EXISTS (SELECT 1 FROM scan_delivery WHERE folder = scan.folder)
	`, scan.Folder, scan.revision)
	if err != nil {
		return deliveryResult{}, err
	}
	if _, err := tx.Exec(retrySQL, target); err != nil {
		return deliveryResult{}, err
	}
	removed, err := delivery.RowsAffected()
	if err != nil {
		return deliveryResult{}, err
	}
	scans, err := deleted.RowsAffected()
	if err != nil {
		return deliveryResult{}, err
	}
	return deliveryResult{removed: removed > 0, scans: scans}, tx.Commit()
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
