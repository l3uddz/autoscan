package processor

import (
	"context"
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
		target := Target{ID: "plex", Open: func() (autoscan.Target, error) {
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
		hung := &anchorProbe{done: make(chan struct{})}
		proc.anchorProbe = hung

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		start := time.Now()
		status := proc.Status(ctx)
		if waited := time.Since(start); waited != 2*time.Second {
			t.Errorf("Status waited %v, want the 2s deadline", waited)
		}
		if !slices.Equal(status.Blockers, []string{"anchor_unavailable"}) || len(status.Targets) != 1 {
			t.Errorf("status = %+v, want only the anchor blocker and the target state", status)
		}
		if proc.anchorProbe != hung {
			t.Error("Status started a second anchor check while one was running")
		}
	})
}
