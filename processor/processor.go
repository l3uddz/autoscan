// Package processor stores the scans in the SQLite datastore and sends
// each folder to the targets when it is due.
package processor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
)

type Config struct {
	Anchors    []string
	MinimumAge time.Duration
	ScanDelay  time.Duration
	Targets    []Target

	DB       *sql.DB
	Migrator *migrate.Migrator
}

// Target identifies a configured destination across restarts. Open can contact
// the server to initialize a client, so the target worker calls it.
type Target struct {
	ID          string
	StatusLabel string
	Open        func(ctx context.Context) (autoscan.Target, error)
}

// TargetStatus reports persisted delivery and retry state for a target.
// A zero NextRetry means no retry is scheduled.
type TargetStatus struct {
	ID        string
	Pending   int
	LastError string
	NextRetry time.Time
}

func New(c Config) (*Processor, error) {
	store, err := newDatastore(c.DB, c.Migrator)
	if err != nil {
		return nil, err
	}
	if err := store.configureTargets(c.Targets); err != nil {
		return nil, fmt.Errorf("configure target deliveries: %w", err)
	}

	// A negative minimum age would deliver a scan before its time.
	return &Processor{
		anchors:    c.Anchors,
		minimumAge: max(0, c.MinimumAge),
		scanDelay:  c.ScanDelay,
		targets:    c.Targets,
		store:      store,
	}, nil
}

type Processor struct {
	anchors    []string
	minimumAge time.Duration
	scanDelay  time.Duration
	targets    []Target
	store      *datastore
	processed  atomic.Int64
	skipped    atomic.Int64

	anchorMu    sync.Mutex
	anchorProbe *anchorProbe
}

func (p *Processor) Add(scans ...autoscan.Scan) error {
	return p.store.Upsert(scans)
}

func (p *Processor) ScansRemaining() (int, error) {
	return p.store.GetScansRemaining()
}

func (p *Processor) ScansProcessed() int64 {
	return p.processed.Load()
}

// ScansSkipped returns how many deliveries the processor removed because
// the scan folder was in no library of the target. A skipped scan that no
// target needs any more also counts as processed.
func (p *Processor) ScansSkipped() int64 {
	return p.skipped.Load()
}

// Run starts one worker for each target and waits for them to exit.
func (p *Processor) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, target := range p.targets {
		workers.Go(func() {
			p.runTarget(ctx, &targetWorker{Target: target})
		})
	}
	workers.Wait()
}

// errAnchorUnavailable means that an anchor file is missing. Delivery waits
// until every anchor is available.
var errAnchorUnavailable = errors.New("anchor file is unavailable")

// anchorTimeout limits how long a delivery waits for the anchor check.
const anchorTimeout = 10 * time.Second

// waitForAnchors waits for the shared anchor check. A check that does not
// finish within anchorTimeout counts as an unavailable anchor.
func (p *Processor) waitForAnchors(ctx context.Context) error {
	if len(p.anchors) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, anchorTimeout)
	defer cancel()
	err := p.probeAnchors().wait(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("anchor check did not finish within %v: %w", anchorTimeout, errAnchorUnavailable)
	}
	return err
}

// anchorProbe is one anchor check that delivery workers and status requests
// share. os.Stat on a hung network mount can block indefinitely and ignores
// contexts, so at most one check runs at a time.
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

func (p *Processor) checkAnchors() error {
	for _, anchor := range p.anchors {
		info, err := os.Stat(anchor)
		if err != nil || info.IsDir() {
			return fmt.Errorf("%s: %w", anchor, errAnchorUnavailable)
		}
	}
	return nil
}
