package processor

import (
	"context"
	"errors"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/rs/zerolog/log"
)

const pollInterval = 15 * time.Second

type targetWorker struct {
	Target
	client autoscan.Target
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
	if err := target.client.Scan(scan.Scan); err != nil {
		target.client = nil
		return p.retryTarget(target, retryDelay, err)
	}
	count, err := p.store.completeDelivery(target.ID, scan)
	if err != nil {
		return 0, err
	}
	p.processed.Add(count)
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
