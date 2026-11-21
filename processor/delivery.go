package processor

import (
	"context"
	"errors"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/rs/zerolog/log"
)

const pollInterval = 15 * time.Second

// maxScanAttempts is how many times in a row a target can reject one scan
// before the worker sets the scan aside.
const maxScanAttempts = 5

type targetWorker struct {
	Target
	client autoscan.Target

	// rejected is the scan revision that the target rejected last, and
	// rejections counts how many times in a row it rejected it.
	rejected   scanRevision
	rejections int

	// setAside reports whether the worker set a scan aside since the last
	// successful delivery.
	setAside bool
}

type scanRevision struct {
	folder   string
	revision string
}

func (p *Processor) runTarget(ctx context.Context, target *targetWorker) {
	for ctx.Err() == nil {
		delay, err := p.deliver(ctx, target)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error().Err(err).Str("target", target.ID).Msg("Scan delivery paused")
			delay = pollInterval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Processor) deliver(ctx context.Context, target *targetWorker) (time.Duration, error) {
	retry, err := p.store.retry(target.ID)
	if err != nil {
		return 0, err
	}
	if wait := time.Until(retry.next); wait > 0 {
		return wait, nil
	}
	if target.client == nil {
		if err := target.connect(); err != nil {
			return p.retryTarget(target, retry.delay, err)
		}
	}
	return p.deliverScan(ctx, target, retry.delay)
}

func (target *targetWorker) connect() error {
	client, err := target.Open()
	if err != nil {
		return err
	}
	if err := client.Available(); err != nil {
		return err
	}
	target.client = client
	return nil
}

func (p *Processor) deliverScan(ctx context.Context, target *targetWorker, retryDelay time.Duration) (time.Duration, error) {
	scan, err := p.store.GetAvailableScan(target.ID, time.Now().Add(-p.minimumAge))
	if errors.Is(err, errNoScans) {
		if retryDelay > 0 {
			return pollInterval, p.store.clearIdleRetry(target.ID)
		}
		return pollInterval, nil
	}
	if err != nil {
		return 0, err
	}
	if err := p.waitForAnchors(ctx); err != nil {
		return 0, err
	}
	err = target.client.Scan(scan.Scan)
	if errors.Is(err, autoscan.ErrNoLibrary) {
		return p.skipScan(target, scan, err)
	}
	if err != nil {
		target.client = nil
		if target.rejects(scan, err) {
			return p.setScanAside(target, scan, err)
		}
		return p.retryTarget(target, retryDelay, err)
	}
	result, err := p.store.completeDelivery(target.ID, scan)
	if err != nil {
		return 0, err
	}
	target.rejected, target.rejections, target.setAside = scanRevision{}, 0, false
	p.processed.Add(result.scans)
	return p.scanDelay, nil
}

// skipScan removes the delivery of a scan whose folder is in no library of
// the target. The target answered, so its client stays connected, and the
// skip does not count as a processed scan or as a rejection.
func (p *Processor) skipScan(target *targetWorker, scan queuedScan, reason error) (time.Duration, error) {
	if _, err := p.store.completeDelivery(target.ID, scan); err != nil {
		return 0, err
	}
	p.skipped.Add(1)
	log.Warn().Err(reason).Str("target", target.ID).Msg("Scan skipped, folder is in no target library")
	return p.scanDelay, nil
}

// rejects counts a failed delivery of scan and reports whether the worker
// sets the scan aside. An error that means that the target is unavailable
// does not count. A failed scan resets the client, so each counted failure
// after the first follows a successful availability check. The worker sets
// aside at most one scan between two successful deliveries, because a target
// that rejects every scan is broken and must keep its scans.
func (target *targetWorker) rejects(scan queuedScan, err error) bool {
	if errors.Is(err, autoscan.ErrTargetUnavailable) {
		return false
	}
	current := scanRevision{folder: scan.Folder, revision: scan.revision}
	if target.rejected != current {
		target.rejected, target.rejections = current, 0
	}
	target.rejections++
	return target.rejections >= maxScanAttempts && !target.setAside
}

// setScanAside removes the delivery of scan to target without counting it
// as processed. Delivery continues with the next scan. If a newer event for
// the folder arrived during the delivery, the delivery stays for the new
// revision, and the worker can still set aside a scan.
func (p *Processor) setScanAside(target *targetWorker, scan queuedScan, failure error) (time.Duration, error) {
	result, err := p.store.setAsideDelivery(target.ID, scan)
	if err != nil {
		return 0, errors.Join(failure, err)
	}
	target.rejected, target.rejections = scanRevision{}, 0
	if !result.removed {
		return p.scanDelay, nil
	}
	target.setAside = true
	log.Error().
		Err(failure).
		Str("target", target.ID).
		Str("path", scan.Folder).
		Int("attempts", maxScanAttempts).
		Msg("Target keeps rejecting scan, scan set aside")
	return p.scanDelay, nil
}

func (p *Processor) retryTarget(target *targetWorker, previous time.Duration, failure error) (time.Duration, error) {
	delay := min(time.Hour, max(15*time.Second, 2*previous))
	if err := p.store.recordFailure(target.ID, delay, time.Now().Add(delay), failure.Error()); err != nil {
		return 0, errors.Join(failure, err)
	}
	log.Warn().Err(failure).Str("target", target.ID).Dur("retry_in", delay).Msg("Target delivery failed")
	return delay, nil
}
