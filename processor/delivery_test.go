package processor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
)

func TestTargetDeliveryRecoversAfterRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "autoscan.db")
		healthy, offline := &fakeTarget{}, &fakeTarget{}
		startup := make(chan struct{})
		finishStartup := sync.OnceFunc(func() { close(startup) })
		defer finishStartup()
		unavailable := errors.New("target is offline")
		startupErr := unavailable
		targets := []Target{
			connectedTarget("healthy", healthy),
			{ID: "offline", Open: func(context.Context) (autoscan.Target, error) {
				<-startup
				return offline, startupErr
			}},
		}
		open := func() (*Processor, *sql.DB) {
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			return processorWithDB(t, db, Config{Targets: targets, ScanDelay: 5 * time.Second}), db
		}
		proc, db := open()
		first := autoscan.Scan{Folder: "first", Priority: 2, Time: time.Now().Add(-time.Hour)}
		second := autoscan.Scan{Folder: "second", Priority: 1, Time: first.Time}
		if err := proc.Add(first, second); err != nil {
			t.Fatal(err)
		}

		stop := startProcessor(t, proc)
		defer func() {
			finishStartup()
			stop()
		}()
		assertDelivered(t, healthy, "first")
		time.Sleep(5 * time.Second)
		synctest.Wait()
		assertDelivered(t, healthy, "first", "second")
		finishStartup()
		synctest.Wait()
		retryAt := time.Now().Add(15 * time.Second)
		assertTargetStatus(t, proc, "offline", 2, unavailable.Error(), retryAt)
		assertTargetStatus(t, proc, "healthy", 0, "", time.Time{})

		stop()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		startupErr = nil
		proc, db = open()
		defer db.Close()
		defer startProcessor(t, proc)()
		assertTargetStatus(t, proc, "offline", 2, unavailable.Error(), retryAt)
		time.Sleep(14 * time.Second)
		synctest.Wait()
		assertDelivered(t, offline)

		time.Sleep(time.Second)
		synctest.Wait()
		assertDelivered(t, offline, "first")
		assertTargetStatus(t, proc, "offline", 1, "", time.Time{})
		time.Sleep(5 * time.Second)
		synctest.Wait()
		assertDelivered(t, offline, "first", "second")
		assertDelivered(t, healthy, "first", "second")
		if remaining, err := proc.ScansRemaining(); err != nil || remaining != 0 {
			t.Fatalf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
		}
		if processed := proc.ScansProcessed(); processed != 2 {
			t.Fatalf("ScansProcessed = %d, want 2", processed)
		}
	})
}

func TestScanFailureRetriesOnlyItsTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		healthy, flaky := &fakeTarget{}, &fakeTarget{}
		failure := errors.New("scan request failed")
		var failing atomic.Bool
		failing.Store(true)
		flaky.onScan = func(autoscan.Scan) error {
			if failing.Load() {
				return failure
			}
			return nil
		}
		proc := testProcessorWithConfig(t, Config{
			Targets: []Target{connectedTarget("healthy", healthy), connectedTarget("flaky", flaky)},
		})
		scan := autoscan.Scan{Folder: "first", Time: time.Now().Add(-time.Minute)}
		if err := proc.Add(scan); err != nil {
			t.Fatal(err)
		}
		defer startProcessor(t, proc)()
		assertDelivered(t, healthy, "first")
		assertTargetStatus(t, proc, "flaky", 1, failure.Error(), time.Now().Add(15*time.Second))

		time.Sleep(15 * time.Second)
		synctest.Wait()
		assertTargetStatus(t, proc, "flaky", 1, failure.Error(), time.Now().Add(30*time.Second))

		failing.Store(false)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		assertTargetStatus(t, proc, "flaky", 0, "", time.Time{})
		assertDelivered(t, healthy, "first")
		assertDelivered(t, flaky, "first", "first", "first")

		failing.Store(true)
		scan.Folder = "second"
		if err := proc.Add(scan); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		assertTargetStatus(t, proc, "flaky", 1, failure.Error(), time.Now().Add(15*time.Second))
		assertDelivered(t, healthy, "first", "second")
	})
}

func TestMissingAnchorPausesAllTargets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := &fakeTarget{}, &fakeTarget{}
		anchor := filepath.Join(t.TempDir(), "mount.anchor")
		proc := testProcessorWithConfig(t, Config{
			Anchors: []string{anchor},
			Targets: []Target{connectedTarget("first", first), connectedTarget("second", second)},
		})
		if err := proc.Add(autoscan.Scan{Folder: "queued", Time: time.Now().Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
		defer startProcessor(t, proc)()
		assertDelivered(t, first)
		assertDelivered(t, second)
		assertTargetStatus(t, proc, "first", 1, "", time.Time{})
		assertTargetStatus(t, proc, "second", 1, "", time.Time{})

		if err := os.WriteFile(anchor, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Second)
		synctest.Wait()
		assertDelivered(t, first, "queued")
		assertDelivered(t, second, "queued")
	})
}

func TestHungAnchorCheckPausesDeliveryUntilItFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := &fakeTarget{}, &fakeTarget{}
		anchor := filepath.Join(t.TempDir(), "mount.anchor")
		if err := os.WriteFile(anchor, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		proc := testProcessorWithConfig(t, Config{
			Anchors: []string{anchor},
			Targets: []Target{connectedTarget("first", first), connectedTarget("second", second)},
		})
		if err := proc.Add(autoscan.Scan{Folder: "queued", Time: time.Now().Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
		hung := hangAnchorCheck(proc)
		defer startProcessor(t, proc)()

		time.Sleep(anchorTimeout + 2*pollInterval)
		synctest.Wait()
		assertDelivered(t, first)
		assertDelivered(t, second)
		proc.anchorMu.Lock()
		shared := proc.anchorProbe == hung
		proc.anchorMu.Unlock()
		if !shared {
			t.Fatal("a second anchor check started while one was running")
		}

		finishAnchorCheck(proc, hung)
		time.Sleep(pollInterval)
		synctest.Wait()
		assertDelivered(t, first, "queued")
		assertDelivered(t, second, "queued")
	})
}

func TestHungAnchorCheckTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proc := testProcessorWithConfig(t, Config{Anchors: []string{filepath.Join(t.TempDir(), "mount.anchor")}})
		hangAnchorCheck(proc)

		start := time.Now()
		err := proc.waitForAnchors(t.Context())
		if waited := time.Since(start); waited != anchorTimeout {
			t.Errorf("waitForAnchors waited %v, want %v", waited, anchorTimeout)
		}
		if !errors.Is(err, errAnchorUnavailable) {
			t.Fatalf("waitForAnchors error = %v, want %v", err, errAnchorUnavailable)
		}
	})
}

func TestHungAnchorCheckDoesNotBlockShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{}
		proc := testProcessorWithConfig(t, Config{
			Anchors: []string{filepath.Join(t.TempDir(), "mount.anchor")},
			Targets: []Target{connectedTarget("plex", target)},
		})
		if err := proc.Add(autoscan.Scan{Folder: "queued", Time: time.Now().Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
		hangAnchorCheck(proc)
		stop := startProcessor(t, proc)

		start := time.Now()
		stop()
		if waited := time.Since(start); waited != 0 {
			t.Errorf("Run returned after %v, want no wait for the anchor check", waited)
		}
		assertDelivered(t, target)
	})
}

func TestTargetRetriesStayAtOneHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{err: errors.New("offline")}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("offline", target)}})
		defer startProcessor(t, proc)()

		delays := []time.Duration{
			15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
			4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute,
			time.Hour, time.Hour, time.Hour,
		}
		for _, delay := range delays {
			assertTargetStatus(t, proc, "offline", 0, "offline", time.Now().Add(delay))
			time.Sleep(delay)
			synctest.Wait()
		}
	})
}

func startProcessor(t *testing.T, proc *Processor) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		proc.Run(ctx)
	}()
	synctest.Wait()
	return func() {
		cancel()
		<-done
	}
}

// hangAnchorCheck makes the shared anchor check look like an os.Stat call
// that blocks on a hung mount.
func hangAnchorCheck(proc *Processor) *anchorProbe {
	hung := &anchorProbe{done: make(chan struct{})}
	proc.anchorMu.Lock()
	proc.anchorProbe = hung
	proc.anchorMu.Unlock()
	return hung
}

// finishAnchorCheck ends a hung check the same way as probeAnchors.
func finishAnchorCheck(proc *Processor, hung *anchorProbe) {
	proc.anchorMu.Lock()
	proc.anchorProbe = nil
	proc.anchorMu.Unlock()
	close(hung.done)
}

func connectedTarget(id string, target autoscan.Target) Target {
	return Target{ID: id, Open: func(context.Context) (autoscan.Target, error) { return target, nil }}
}

func assertDelivered(t *testing.T, target *fakeTarget, folders ...string) {
	t.Helper()
	target.mu.Lock()
	defer target.mu.Unlock()
	if len(target.scans) != len(folders) {
		t.Fatalf("delivered scans = %+v, want folders %v", target.scans, folders)
	}
	for i, folder := range folders {
		if target.scans[i].Folder != folder {
			t.Fatalf("delivered scan %d = %+v, want folder %q", i, target.scans[i], folder)
		}
	}
}

func assertTargetStatus(t *testing.T, proc *Processor, id string, pending int, lastError string, nextRetry time.Time) {
	t.Helper()
	statuses, _, err := proc.store.queueStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.ID == id {
			if status.Pending != pending || status.LastError != lastError || !status.NextRetry.Equal(nextRetry) {
				t.Fatalf("status for %q = %+v, want %d pending, error %q, retry at %v", id, status, pending, lastError, nextRetry)
			}
			return
		}
	}
	t.Fatalf("missing status for %q in %+v", id, statuses)
}

func testProcessorWithConfig(t *testing.T, cfg Config) *Processor {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return processorWithDB(t, db, cfg)
}

func processorWithDB(t *testing.T, db *sql.DB, cfg Config) *Processor {
	t.Helper()
	db.SetMaxOpenConns(1)
	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DB, cfg.Migrator = db, mg
	proc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return proc
}

func TestShutdownStopsScanWithoutRecordingFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &blockingTarget{}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "folder")
		stop := startProcessor(t, proc)

		// The worker waits in the scan request until the shutdown ends it.
		stop()
		assertTargetStatus(t, proc, "server", 1, "", time.Time{})
		if remaining, err := proc.ScansRemaining(); err != nil || remaining != 1 {
			t.Fatalf("ScansRemaining = %d, %v, want 1, nil", remaining, err)
		}
	})
}

// blockingTarget answers a scan request only when its context ends, like an
// HTTP request to a server that does not answer.
type blockingTarget struct{}

func (blockingTarget) Available(context.Context) error { return nil }

func (blockingTarget) Scan(ctx context.Context, _ autoscan.Scan) error {
	<-ctx.Done()
	return fmt.Errorf("%w: %w", ctx.Err(), autoscan.ErrTargetUnavailable)
}
