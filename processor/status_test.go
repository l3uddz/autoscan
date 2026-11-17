package processor

import (
	"errors"
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
