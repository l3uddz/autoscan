package processor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
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
		if remaining, err := proc.ScansRemaining(t.Context()); err != nil || remaining != 0 {
			t.Fatalf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
		}
		if processed := proc.ScansProcessed(); processed != 1 {
			t.Errorf("ScansProcessed = %d, want 1: a set-aside scan counted as processed", processed)
		}
	})
}

func TestFolderInNoLibraryIsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var opens atomic.Int32
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			if scan.Folder == "movies" {
				return fmt.Errorf("%s: %w", scan.Folder, autoscan.ErrNoLibrary)
			}
			return nil
		}}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{{ID: "server", Open: func(context.Context) (autoscan.Target, error) {
			opens.Add(1)
			return target, nil
		}}}})
		addScans(t, proc, "movies", "tv")
		defer startProcessor(t, proc)()

		time.Sleep(time.Second)
		synctest.Wait()
		assertDelivered(t, target, "movies", "tv")
		assertTargetStatus(t, proc, "server", 0, "", time.Time{})
		if processed, skipped := proc.ScansProcessed(), proc.ScansSkipped(); processed != 2 || skipped != 1 {
			t.Errorf("ScansProcessed, ScansSkipped = %d, %d, want 2, 1", processed, skipped)
		}
		if n := opens.Load(); n != 1 {
			t.Errorf("Open calls = %d, want 1: a skipped folder reset the client", n)
		}
	})
}

func TestSkippedScanCountsAsProcessedInEitherOrder(t *testing.T) {
	for _, skipFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("skip first %v", skipFirst), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The second target waits until the first is done with the scan.
				release := make(chan struct{})
				receive := func(autoscan.Scan) error { return nil }
				skip := func(scan autoscan.Scan) error { return fmt.Errorf("%s: %w", scan.Folder, autoscan.ErrNoLibrary) }
				first, second := receive, skip
				if skipFirst {
					first, second = skip, receive
				}
				gated := func(scan autoscan.Scan) error {
					<-release
					return second(scan)
				}
				proc := testProcessorWithConfig(t, Config{Targets: []Target{
					connectedTarget("first", &fakeTarget{onScan: first}),
					connectedTarget("second", &fakeTarget{onScan: gated}),
				}})
				addScans(t, proc, "folder")
				defer startProcessor(t, proc)()

				time.Sleep(time.Second)
				synctest.Wait()
				close(release)
				time.Sleep(time.Second)
				synctest.Wait()
				if processed, skipped := proc.ScansProcessed(), proc.ScansSkipped(); processed != 1 || skipped != 1 {
					t.Errorf("ScansProcessed, ScansSkipped = %d, %d, want 1, 1", processed, skipped)
				}
			})
		})
	}
}

func TestSkippedScanKeepsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			if scan.Folder == "movies" {
				return fmt.Errorf("%s: %w", scan.Folder, autoscan.ErrNoLibrary)
			}
			return errors.New("400 Bad Request")
		}}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "bad", "movies")
		defer startProcessor(t, proc)()

		// The skip reaches no server, so /status keeps the error of the
		// scan that was set aside.
		time.Sleep(retriesUntilSetAside + time.Second)
		synctest.Wait()
		assertDelivered(t, target, "bad", "bad", "bad", "bad", "bad", "movies")
		assertTargetStatus(t, proc, "server", 0, "400 Bad Request", time.Time{})
	})
}

func TestNegativeMinimumAgeWaitsForScanTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{}
		proc := testProcessorWithConfig(t, Config{MinimumAge: -time.Minute, Targets: []Target{connectedTarget("server", target)}})
		// A scan time in the future, for example after the clock went back.
		if err := proc.Add(autoscan.Scan{Folder: "folder", Time: time.Now().Add(30 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		defer startProcessor(t, proc)()

		time.Sleep(time.Second)
		synctest.Wait()
		assertDelivered(t, target)
	})
}

func TestSkipOfOldRevisionCountsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var proc *Processor
		var attempts atomic.Int32
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			// A new event for the folder arrives during the first attempt.
			if attempts.Add(1) == 1 {
				if err := proc.Add(autoscan.Scan{Folder: scan.Folder, Time: time.Now().Add(-time.Minute)}); err != nil {
					t.Error(err)
				}
			}
			return fmt.Errorf("%s: %w", scan.Folder, autoscan.ErrNoLibrary)
		}}
		proc = testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "movies")
		defer startProcessor(t, proc)()

		time.Sleep(time.Minute)
		synctest.Wait()
		assertAttempts(t, target, 2)
		if processed, skipped := proc.ScansProcessed(), proc.ScansSkipped(); processed != 1 || skipped != 1 {
			t.Errorf("ScansProcessed, ScansSkipped = %d, %d, want 1, 1", processed, skipped)
		}
	})
}

func TestSetAsideScanKeepsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := &fakeTarget{onScan: func(autoscan.Scan) error { return errors.New("400 Bad Request") }}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "bad")
		defer startProcessor(t, proc)()

		// Wait past the set-aside and the next idle poll of the empty queue.
		time.Sleep(retriesUntilSetAside + pollInterval + time.Second)
		synctest.Wait()
		assertAttempts(t, target, maxScanAttempts)
		assertTargetStatus(t, proc, "server", 0, "400 Bad Request", time.Time{})
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

func TestUnavailableErrorsDoNotCountAsRejections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		unavailable := fmt.Errorf("503 Service Unavailable: %w", autoscan.ErrTargetUnavailable)
		var rejecting atomic.Bool
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			switch {
			case scan.Folder != "folder":
				return nil
			case rejecting.Load():
				return errors.New("400 Bad Request")
			default:
				return unavailable
			}
		}}
		proc := testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "folder", "next")
		defer startProcessor(t, proc)()

		// Seven unavailable attempts, at 0s, 15s, 45s, 105s, 225s, 465s,
		// and 945s, keep the scan.
		time.Sleep(945 * time.Second)
		synctest.Wait()
		assertAttempts(t, target, 7)
		assertTargetStatus(t, proc, "server", 2, unavailable.Error(), time.Now().Add(16*time.Minute))

		// Five rejections in a row then set it aside. The unavailable attempts
		// did not count toward them.
		rejecting.Store(true)
		time.Sleep(16*time.Minute + 32*time.Minute + 3*time.Hour)
		synctest.Wait()
		assertDelivered(t, target, slices.Concat(slices.Repeat([]string{"folder"}, 12), []string{"next"})...)
		if remaining, err := proc.ScansRemaining(t.Context()); err != nil || remaining != 0 {
			t.Fatalf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
		}
	})
}

func TestNewerEventKeepsSetAsideAllowance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var proc *Processor
		var attempts atomic.Int32
		target := &fakeTarget{onScan: func(scan autoscan.Scan) error {
			// A new event for the folder arrives during the fifth attempt.
			if attempts.Add(1) == maxScanAttempts {
				if err := proc.Add(autoscan.Scan{Folder: scan.Folder, Time: time.Now().Add(-time.Minute)}); err != nil {
					t.Error(err)
				}
			}
			return errors.New("400 Bad Request")
		}}
		proc = testProcessorWithConfig(t, Config{Targets: []Target{connectedTarget("server", target)}})
		addScans(t, proc, "folder")
		defer startProcessor(t, proc)()

		// The fifth attempt keeps the new revision. Five attempts of the new
		// revision then set it aside, because no scan was set aside before.
		time.Sleep(2 * retriesUntilSetAside)
		synctest.Wait()
		assertAttempts(t, target, 2*maxScanAttempts)
		if remaining, err := proc.ScansRemaining(t.Context()); err != nil || remaining != 0 {
			t.Fatalf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
		}
	})
}

func assertAttempts(t *testing.T, target *fakeTarget, want int) {
	t.Helper()
	target.mu.Lock()
	defer target.mu.Unlock()
	if len(target.scans) != want {
		t.Fatalf("Delivery attempts = %d, want %d", len(target.scans), want)
	}
}
