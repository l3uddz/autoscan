package inotify

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
)

func TestRetryDoublesDelayUntilSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		failures := 3
		retry(zerolog.Nop(), "Failed", "/mnt/tv", func() error {
			if failures == 0 {
				return nil
			}
			failures--
			return errors.New("missing")
		})

		if elapsed, want := time.Since(start), 35*time.Second; elapsed != want {
			t.Errorf("retry took %v, want %v for delays of 5s, 10s, and 20s", elapsed, want)
		}
	})
}

func TestRetryCapsDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts []time.Time
		retry(zerolog.Nop(), "Failed", "/mnt/tv", func() error {
			attempts = append(attempts, time.Now())
			if len(attempts) < 9 {
				return errors.New("missing")
			}
			return nil
		})

		if gap := attempts[8].Sub(attempts[7]); gap != watchRetryMax {
			t.Errorf("Last retry delay = %v, want %v", gap, watchRetryMax)
		}
	})
}

func TestRootLostDuringSetupIsWatchedAgain(t *testing.T) {
	trigger := &Trigger{log: zerolog.Nop(), paths: []*path{{Path: "/mnt/tv"}}}
	p := trigger.paths[0]

	// The walk starts, then the root is removed before the walk marks the
	// path as watched.
	losses := p.losses.Load()
	trigger.rewatchLostRoot(fsnotify.Event{Name: "/mnt/tv", Op: fsnotify.Remove})

	p.watching.Store(true)
	watched, again := checkRootLoss(p, losses)
	if watched || !again || p.watching.Load() {
		t.Errorf("checkRootLoss = %v, %v with watching %v, want false, true with watching false", watched, again, p.watching.Load())
	}
}

func TestRootLostAfterSetupStartsOneRetry(t *testing.T) {
	trigger := &Trigger{log: zerolog.Nop(), paths: []*path{{Path: "/mnt/tv"}}}
	p := trigger.paths[0]

	// The walk marks the path, then the event loop handles the loss before
	// the walk checks the count. The event loop clears the mark and starts
	// the retry, so the walk must not start a second one.
	losses := p.losses.Load()
	p.watching.Store(true)
	p.losses.Add(1)
	if !p.watching.CompareAndSwap(true, false) {
		t.Fatal("the event loop could not clear the mark")
	}

	watched, again := checkRootLoss(p, losses)
	if watched || again {
		t.Errorf("checkRootLoss = %v, %v, want false, false: the event loop already started the retry", watched, again)
	}
}
