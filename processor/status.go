package processor

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/l3uddz/autoscan"
)

type Status struct {
	CanDeliver       bool             `json:"can_deliver"`
	Blockers         []string         `json:"blockers"`
	OldestPendingAge *int64           `json:"oldest_pending_scan_age_seconds"`
	Targets          []DeliveryStatus `json:"targets"`
}

type DeliveryStatus struct {
	ID        string     `json:"id"`
	Pending   int        `json:"pending"`
	LastError string     `json:"last_error"`
	NextRetry *time.Time `json:"next_retry"`
}

func (p *Processor) Status(ctx context.Context) Status {
	status := Status{Blockers: []string{}, Targets: []DeliveryStatus{}}
	// Start the anchor check before the database read, so that a slow check
	// cannot use up the time that the read needs.
	var anchors *anchorProbe
	if len(p.anchors) > 0 {
		anchors = p.probeAnchors()
	}
	// Compare the queue state with the time of its read, not with the time
	// after the anchor wait.
	now := time.Now()
	targets, oldest, err := p.store.queueStatus(ctx)
	if anchors != nil && anchors.wait(ctx) != nil {
		status.Blockers = append(status.Blockers, "anchor_unavailable")
	}
	if len(p.targets) == 0 {
		status.Blockers = append(status.Blockers, "no_targets")
	}
	if err != nil {
		status.Blockers = append(status.Blockers, "database_unavailable")
		return status
	}
	if oldest != nil {
		age := int64(max(0, now.Sub(*oldest)) / time.Second)
		status.OldestPendingAge = &age
	}
	var hasReadyTarget bool
	status.Targets, hasReadyTarget = p.deliveryStatuses(targets, now)
	status.CanDeliver = len(status.Blockers) == 0 && hasReadyTarget
	return status
}

// anchorProbe is one anchor check that concurrent status requests share.
// os.Stat on a hung network mount can block indefinitely and ignores the
// request context, so at most one check runs at a time.
type anchorProbe struct {
	done chan struct{}
	err  error
}

func (p *Processor) probeAnchors() *anchorProbe {
	p.anchorMu.Lock()
	defer p.anchorMu.Unlock()
	if p.anchorProbe == nil {
		probe := &anchorProbe{done: make(chan struct{})}
		p.anchorProbe = probe
		go func() {
			probe.err = p.checkAnchors()
			p.anchorMu.Lock()
			p.anchorProbe = nil
			p.anchorMu.Unlock()
			close(probe.done)
		}()
	}
	return p.anchorProbe
}

func (probe *anchorProbe) wait(ctx context.Context) error {
	// Use a finished result even if the context also ended.
	select {
	case <-probe.done:
		return probe.err
	default:
	}
	select {
	case <-probe.done:
		return probe.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Processor) deliveryStatuses(targets []TargetStatus, now time.Time) ([]DeliveryStatus, bool) {
	byID := make(map[string]TargetStatus, len(targets))
	for _, target := range targets {
		byID[target.ID] = target
	}
	deliveries := make([]DeliveryStatus, 0, len(p.targets))
	ready := false
	for i, configured := range p.targets {
		target, ok := byID[configured.ID]
		if !ok {
			continue
		}
		label := configured.StatusLabel
		if label == "" {
			label = strconv.Itoa(i + 1)
		}
		delivery := DeliveryStatus{ID: label, Pending: target.Pending, LastError: publicTargetError(target.LastError)}
		// The retry record stays until a delivery succeeds, so only a future
		// retry time means that the worker waits. This matches deliver.
		if target.NextRetry.After(now) {
			delivery.NextRetry = &target.NextRetry
		} else {
			ready = true
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, ready
}

func publicTargetError(message string) string {
	// Target errors can include URLs, headers, or response bodies with credentials.
	if message == "" {
		return ""
	}
	unavailable := autoscan.ErrTargetUnavailable.Error()
	if message == unavailable || strings.HasSuffix(message, ": "+unavailable) {
		return unavailable
	}
	return "target request failed"
}

func (store *datastore) queueStatus(ctx context.Context) ([]TargetStatus, *time.Time, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	targets, err := targetStatuses(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	oldest, err := oldestPendingScan(ctx, tx)
	return targets, oldest, err
}

func oldestPendingScan(ctx context.Context, tx *sql.Tx) (*time.Time, error) {
	rows, err := tx.QueryContext(ctx, `SELECT time FROM scan`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// SQLite stores the timestamp as text. Compare the decoded times because
	// timestamps with different UTC offsets can sort in a different order.
	var oldest *time.Time
	for rows.Next() {
		var stamp time.Time
		if err := rows.Scan(&stamp); err != nil {
			return nil, err
		}
		if oldest == nil || stamp.Before(*oldest) {
			oldest = &stamp
		}
	}
	return oldest, rows.Err()
}
