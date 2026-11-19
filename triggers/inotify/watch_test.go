package inotify

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

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
