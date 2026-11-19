package processor

import (
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/l3uddz/autoscan"
)

// retriesUntilSetAside is the time from the first failed delivery of a scan
// to its last attempt: the retry delays 15s, 30s, 60s, and 120s.
const retriesUntilSetAside = 225 * time.Second

func addScans(t *testing.T, proc *Processor, folders ...string) {
	t.Helper()
	for i, folder := range folders {
		scan := autoscan.Scan{Folder: folder, Priority: len(folders) - i, Time: time.Now().Add(-time.Minute)}
		if err := proc.Add(scan); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRejectedScanIsSetAside(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			if scan.Folder == "bad" {
				return errors.New("400 Bad Request")
			}
			return nil
		}}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "bad", "good")
		defer startProcessor(t, proc)()

		time.Sleep(retriesUntilSetAside - time.Second)
		synctest.Wait()
		assertDelivered(t, target, "bad", "bad", "bad", "bad")

		time.Sleep(time.Second)
		synctest.Wait()
		assertDelivered(t, target, "bad", "bad", "bad", "bad", "bad", "good")
		assertTargetStatus(t, proc, "server", 0, "", time.Time{})
		if remaining, err := proc.ScansRemaining(); err != nil || remaining != 0 {
			t.Fatalf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
		}
		if processed := proc.ScansProcessed(); processed != 1 {
			t.Errorf("ScansProcessed = %d, want 1: a set-aside scan counted as processed", processed)
		}
	})
}

func TestTargetThatRejectsEveryScanSetsAsideOnlyOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("403 Forbidden")
		target := &fakeTarget{onScan: func(autoscan.Scan) error { return failure }}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "first", "second")
		defer startProcessor(t, proc)()

		// The second scan starts after the first is set aside. Wait past its
		// fifth and sixth attempts, 225s and 465s after its first attempt.
		time.Sleep(2*retriesUntilSetAside + 240*time.Second)
		synctest.Wait()
		target.mu.Lock()
		attempts := len(target.scans)
		target.mu.Unlock()
		if attempts != 11 {
			t.Fatalf("Delivery attempts = %d, want 11", attempts)
		}
		assertTargetStatus(t, proc, "server", 1, failure.Error(), time.Now().Add(8*time.Minute))
	})
}

func TestUnavailableTargetNeverSetsScanAside(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := fmt.Errorf("503 Service Unavailable: %w", autoscan.ErrTargetUnavailable)
		target := &fakeTarget{onScan: func(autoscan.Scan) error { return failure }}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "folder")
		defer startProcessor(t, proc)()

		// Wait past seven attempts.
		time.Sleep(retriesUntilSetAside + 240*time.Second + 480*time.Second)
		synctest.Wait()
		target.mu.Lock()
		attempts := len(target.scans)
		target.mu.Unlock()
		if attempts != 7 {
			t.Fatalf("Delivery attempts = %d, want 7", attempts)
		}
		assertTargetStatus(t, proc, "server", 1, failure.Error(), time.Now().Add(16*time.Minute))
	})
}
