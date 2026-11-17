package processor

import (
	"context"
	"database/sql"
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

	Db *sql.DB
	Mg *migrate.Migrator
}

// Target identifies a configured destination across restarts. Open can contact
// the server to initialize a client, so the target worker calls it.
type Target struct {
	ID   string
	Open func() (autoscan.Target, error)
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
	store, err := newDatastore(c.Db, c.Mg)
	if err != nil {
		return nil, err
	}
	if err := store.configureTargets(c.Targets); err != nil {
		return nil, fmt.Errorf("configure target deliveries: %w", err)
	}

	return &Processor{
		anchors:    c.Anchors,
		minimumAge: c.MinimumAge,
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

func (p *Processor) TargetStatuses() ([]TargetStatus, error) {
	return p.store.targetStatuses()
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

func (p *Processor) checkAnchors() error {
	for _, anchor := range p.anchors {
		info, err := os.Stat(anchor)
		if err != nil || info.IsDir() {
			return fmt.Errorf("%s: %w", anchor, autoscan.ErrAnchorUnavailable)
		}
	}
	return nil
}
