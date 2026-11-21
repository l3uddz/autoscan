package inotify

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func TestQueueRetriesFailedScan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := filepath.FromSlash("/mnt/media/show")
		attempts := make(chan autoscan.Scan, 4)
		firstAttempt := true
		q := newQueue(t.Context(), func(_ context.Context, scans ...autoscan.Scan) error {
			for _, scan := range scans {
				select {
				case attempts <- scan:
				default:
					t.Error("Too many scan attempts")
				}
			}
			if firstAttempt {
				firstAttempt = false
				return errors.New("queue write failed")
			}
			return nil
		}, zerolog.Nop(), 3)
		defer q.stop()

		q.inputs <- folder
		time.Sleep(queueDelay)
		synctest.Wait()
		if got := len(attempts); got != 1 {
			t.Fatalf("Got %d attempts after the initial delay, want 1", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if got := len(attempts); got != 2 {
			t.Fatalf("Got %d attempts after the retry delay, want 2", got)
		}
		for range 2 {
			scan := <-attempts
			if scan.Folder != folder || scan.Priority != 3 {
				t.Errorf("Got scan %+v, want folder %s with priority 3", scan, folder)
			}
		}

		time.Sleep(time.Minute)
		synctest.Wait()
		if got := len(attempts); got != 0 {
			t.Errorf("Got %d extra attempts after success, want 0", got)
		}
	})
}

func TestQueueCapsRetryDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		q := newQueue(t.Context(), func(context.Context, ...autoscan.Scan) error {
			attempts.Add(1)
			return errors.New("queue write failed")
		}, zerolog.Nop(), 0)
		defer q.stop()

		folder := filepath.FromSlash("/mnt/media/show")
		q.inputs <- folder
		delays := []int{10, 1, 2, 4, 8, 16, 32, 60, 60}
		for i, seconds := range delays {
			time.Sleep(time.Duration(seconds)*time.Second - time.Nanosecond)
			synctest.Wait()
			if got := int(attempts.Load()); got != i {
				t.Fatalf("Got %d attempts before the %ds delay elapsed, want %d", got, seconds, i)
			}

			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if got := int(attempts.Load()); got != i+1 {
				t.Fatalf("Got %d attempts after the %ds delay elapsed, want %d", got, seconds, i+1)
			}

			if seconds == 32 {
				q.inputs <- folder // This event must not shorten the next retry delay.
			}
		}
	})
}

func TestQueueSendsOneScanAfterDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scans := make(chan autoscan.Scan, 4)
		q := newQueue(t.Context(), func(_ context.Context, accepted ...autoscan.Scan) error {
			for _, scan := range accepted {
				select {
				case scans <- scan:
				default:
					t.Error("Too many scans")
				}
			}
			return nil
		}, zerolog.Nop(), 3)
		defer q.stop()

		q.inputs <- "/mnt/media/show/"
		time.Sleep(queueDelay / 2)
		q.inputs <- "/mnt/media/show/"

		time.Sleep(queueDelay - time.Nanosecond)
		synctest.Wait()
		if got := len(scans); got != 0 {
			t.Fatalf("Got %d scans before the delay from the last event elapsed, want 0", got)
		}

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := len(scans); got != 1 {
			t.Fatalf("Got %d scans after the delay from the last event elapsed, want 1", got)
		}
		want := filepath.FromSlash("/mnt/media/show")
		scan := <-scans
		if scan.Folder != want || scan.Priority != 3 {
			t.Errorf("Got scan %+v, want folder %s with priority 3", scan, want)
		}

		time.Sleep(queueDelay)
		synctest.Wait()
		if got := len(scans); got != 0 {
			t.Errorf("Got %d extra scans for a repeated path, want 0", got)
		}
	})
}

func TestQueueFlushesPendingScansOnStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var flushed []autoscan.Scan
		q := newQueue(t.Context(), func(_ context.Context, accepted ...autoscan.Scan) error {
			flushed = append(flushed, accepted...)
			return nil
		}, zerolog.Nop(), 3)

		q.inputs <- "/mnt/media/show/"
		synctest.Wait()
		q.stop()

		want := filepath.FromSlash("/mnt/media/show")
		if len(flushed) != 1 || flushed[0].Folder != want || flushed[0].Priority != 3 {
			t.Errorf("Flushed scans = %+v, want one scan for %s with priority 3", flushed, want)
		}
	})
}

func TestQueueFlushesPendingScansOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var flushed []autoscan.Scan
		q := newQueue(ctx, func(_ context.Context, accepted ...autoscan.Scan) error {
			flushed = append(flushed, accepted...)
			return nil
		}, zerolog.Nop(), 3)

		q.inputs <- "/mnt/media/show/"
		synctest.Wait()
		cancel()
		<-q.done

		want := filepath.FromSlash("/mnt/media/show")
		if len(flushed) != 1 || flushed[0].Folder != want || flushed[0].Priority != 3 {
			t.Errorf("Flushed scans = %+v, want one scan for %s with priority 3", flushed, want)
		}
	})
}

func TestQueueFlushRetriesTransientErrorOnStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts int
		var flushed []autoscan.Scan
		q := newQueue(t.Context(), func(_ context.Context, accepted ...autoscan.Scan) error {
			attempts++
			if attempts < 3 {
				return errors.New("datastore busy")
			}
			flushed = append(flushed, accepted...)
			return nil
		}, zerolog.Nop(), 3)

		q.inputs <- "/mnt/media/show/"
		synctest.Wait()

		start := time.Now()
		q.stop()
		elapsed := time.Since(start)

		if attempts != 3 {
			t.Errorf("flush attempts = %d, want 3", attempts)
		}
		if want := 2 * flushRetryDelay; elapsed != want {
			t.Errorf("flush retry duration = %v, want %v", elapsed, want)
		}
		want := filepath.FromSlash("/mnt/media/show")
		if len(flushed) != 1 || flushed[0].Folder != want || flushed[0].Priority != 3 {
			t.Errorf("Flushed scans = %+v, want one scan for %s with priority 3", flushed, want)
		}
	})
}

func TestQueueFlushStopsRetryingAfterBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts int
		q := newQueue(t.Context(), func(context.Context, ...autoscan.Scan) error {
			attempts++
			return errors.New("datastore unavailable")
		}, zerolog.Nop(), 3)

		q.inputs <- "/mnt/media/show/"
		synctest.Wait()

		start := time.Now()
		q.stop()
		elapsed := time.Since(start)

		if attempts < 2 {
			t.Errorf("flush attempts = %d, want retries within budget", attempts)
		}
		if elapsed != flushRetryTimeout {
			t.Errorf("flush duration = %v, want %v", elapsed, flushRetryTimeout)
		}
	})
}

func TestQueueFlushCancelsSlowCallbackAtBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts int
		q := newQueue(t.Context(), func(ctx context.Context, _ ...autoscan.Scan) error {
			attempts++
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		}, zerolog.Nop(), 3)

		q.inputs <- "/mnt/media/show/"
		synctest.Wait()

		start := time.Now()
		q.stop()
		elapsed := time.Since(start)

		if attempts != 1 {
			t.Errorf("flush attempts = %d, want 1", attempts)
		}
		if elapsed != flushRetryTimeout {
			t.Errorf("flush duration = %v, want %v", elapsed, flushRetryTimeout)
		}
	})
}

func TestQueueProcessStopsOnCancelAndFlushesRemaining(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var normalCalls int
		var saved []string
		q := newQueue(ctx, func(cbCtx context.Context, accepted ...autoscan.Scan) error {
			if cbCtx == ctx {
				normalCalls++
				cancel()
			}
			for _, s := range accepted {
				saved = append(saved, s.Folder)
			}
			return nil
		}, zerolog.Nop(), 3)

		first := filepath.FromSlash("/mnt/media/show1")
		second := filepath.FromSlash("/mnt/media/show2")
		q.inputs <- first
		q.inputs <- second

		time.Sleep(queueDelay)
		<-q.done

		if normalCalls != 1 {
			t.Errorf("normal process callbacks = %d, want 1 before shutdown flush", normalCalls)
		}
		slices.Sort(saved)
		if want := []string{first, second}; !slices.Equal(saved, want) {
			t.Errorf("saved scans = %q, want %q", saved, want)
		}
	})
}
