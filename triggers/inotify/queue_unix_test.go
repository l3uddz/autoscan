//go:build unix

package inotify

import (
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func cpuTime(t *testing.T) time.Duration {
	t.Helper()

	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatal(err)
	}

	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func TestQueueIdlesWhileScanWaits(t *testing.T) {
	q := newQueue(func(...autoscan.Scan) error { return nil }, zerolog.Nop(), 0)
	defer close(q.inputs)

	// The scan waits for the queue delay, which is longer than the window.
	q.inputs <- "/mnt/media/show"

	const window = 300 * time.Millisecond
	start := cpuTime(t)
	time.Sleep(window)
	used := cpuTime(t) - start

	// A busy loop uses about one full core for the whole window.
	if used > window/3 {
		t.Errorf("Queue used %v of CPU time in %v while it waited", used, window)
	}
}
