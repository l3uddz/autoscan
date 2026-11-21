package processor

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/l3uddz/autoscan"
)

type fakeTarget struct {
	err    error
	onScan func(autoscan.Scan) error

	mu    sync.Mutex
	scans []autoscan.Scan
}

func (f *fakeTarget) Scan(_ context.Context, scan autoscan.Scan) error {
	f.mu.Lock()
	f.scans = append(f.scans, scan)
	f.mu.Unlock()
	if f.onScan != nil {
		return f.onScan(scan)
	}
	return f.err
}

func (f *fakeTarget) Available(context.Context) error {
	return f.err
}

func TestProcessKeepsScanEnqueuedDuringDispatch(t *testing.T) {
	const minimumAge = 10 * time.Minute
	for _, tc := range []struct {
		name        string
		refreshTime bool
	}{
		{name: "new timestamp restarts minimum age", refreshTime: true},
		{name: "unchanged stored values still need delivery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				clock := time.Now()
				target := &fakeTarget{}
				configured := connectedTarget("target", target)
				proc := testProcessorWithConfig(t, Config{Targets: []Target{configured}, MinimumAge: minimumAge})
				worker := &targetWorker{Target: configured}
				first := autoscan.Scan{Folder: "/mnt/media/show", Priority: 5, Time: clock.Add(-time.Hour)}
				if err := proc.Add(first); err != nil {
					t.Fatal(err)
				}

				followUp := autoscan.Scan{Folder: first.Folder, Priority: 3, Time: first.Time}
				if tc.refreshTime {
					followUp.Time = clock
				}
				target.onScan = func(autoscan.Scan) error {
					return proc.Add(followUp)
				}
				if _, _, err := proc.deliver(t.Context(), worker); err != nil {
					t.Fatalf("first delivery: %v", err)
				}
				if remaining, err := proc.ScansRemaining(); err != nil || remaining != 1 {
					t.Fatalf("ScansRemaining after first delivery = %d, %v, want 1, nil", remaining, err)
				}

				target.onScan = nil
				if tc.refreshTime {
					if delay, _, err := proc.deliver(t.Context(), worker); err != nil || delay == 0 {
						t.Fatalf("delivery before minimum age: delay %v, err %v", delay, err)
					}
					assertDelivered(t, target, first.Folder)
					time.Sleep(minimumAge + time.Second)
				}
				if _, _, err := proc.deliver(t.Context(), worker); err != nil {
					t.Fatalf("follow-up delivery: %v", err)
				}
				want := autoscan.Scan{Folder: first.Folder, Priority: first.Priority, Time: followUp.Time}
				if len(target.scans) != 2 {
					t.Fatalf("delivered scans = %+v, want [%+v %+v]", target.scans, first, want)
				}
				got := target.scans[1]
				if got.Folder != want.Folder || got.Priority != want.Priority || !got.Time.Equal(want.Time) {
					t.Errorf("follow-up scan = %+v, want %+v", got, want)
				}
				if remaining, err := proc.ScansRemaining(); err != nil || remaining != 0 {
					t.Errorf("ScansRemaining after follow-up delivery = %d, %v, want 0, nil", remaining, err)
				}
			})
		})
	}
}
