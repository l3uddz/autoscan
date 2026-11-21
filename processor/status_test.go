package processor

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/l3uddz/autoscan"
)

func TestStatusReadyAfterReconnectWithYoungScans(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var failed atomic.Bool
		target := Target{ID: "plex", Open: func(context.Context) (autoscan.Target, error) {
			if failed.CompareAndSwap(false, true) {
				return nil, errors.New("offline")
			}
			return &fakeTarget{}, nil
		}}
		proc := testProcessorWithConfig(t, Config{MinimumAge: time.Hour, Targets: []Target{target}})
		if err := proc.Add(autoscan.Scan{Folder: "young", Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
		defer startProcessor(t, proc)()

		time.Sleep(15 * time.Second)
		synctest.Wait()
		status := proc.Status(t.Context())
		if !status.CanDeliver || len(status.Targets) != 1 || status.Targets[0].Pending != 1 || status.Targets[0].NextRetry != nil {
			t.Fatalf("status after reconnect = %+v, want a ready target with one pending scan", status)
		}
	})
}

func TestStatusStopsWaitingForHungAnchorCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		anchor := filepath.Join(t.TempDir(), "mount.anchor")
		if err := os.WriteFile(anchor, nil, 0600); err != nil {
			t.Fatal(err)
		}
		proc := testProcessorWithConfig(t, Config{Anchors: []string{anchor}, Targets: []Target{connectedTarget("plex", &fakeTarget{})}})
		retryAt := time.Now().Add(time.Second)
		if err := proc.store.recordFailure(t.Context(), "plex", 15*time.Second, retryAt, "offline"); err != nil {
			t.Fatal(err)
		}
		hung := hangAnchorCheck(proc)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		start := time.Now()
		status := proc.Status(ctx)
		if waited := time.Since(start); waited != 2*time.Second {
			t.Errorf("Status waited %v, want the 2s deadline", waited)
		}
		if !slices.Equal(status.Blockers, []string{"anchor_unavailable"}) || len(status.Targets) != 1 {
			t.Fatalf("status = %+v, want only the anchor blocker and the target state", status)
		}
		// The retry time passes during the wait, but the target state is from before it.
		if next := status.Targets[0].NextRetry; next == nil || !next.Equal(retryAt) {
			t.Errorf("next retry = %v, want %v from the time of the queue read", next, retryAt)
		}
		if proc.anchorProbe != hung {
			t.Error("Status started a second anchor check while one was running")
		}
	})
}

func TestStatusReadsWhileAnotherProcessWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autoscan.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	proc := processorWithDB(t, db, Config{})

	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	lock, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec(`INSERT INTO scan_target (id) VALUES ('other')`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := proc.store.queueStatus(t.Context()); err != nil {
		t.Errorf("queueStatus returned %v while another process holds the write lock", err)
	}
}
