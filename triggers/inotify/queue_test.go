package inotify

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func TestQueueSendsOneScanAfterDelay(t *testing.T) {
	scans := make(chan autoscan.Scan, 10)
	q := &queue{
		callback: func(s ...autoscan.Scan) error {
			for _, scan := range s {
				scans <- scan
			}
			return nil
		},
		log:      zerolog.Nop(),
		priority: 3,
		delay:    100 * time.Millisecond,
		interval: 10 * time.Millisecond,
		inputs:   make(chan string),
		scans:    make(map[string]time.Time),
	}
	go q.worker()
	defer close(q.inputs)

	// The second event for the same path restarts the delay.
	q.inputs <- "/mnt/media/show/"
	time.Sleep(q.delay / 2)
	last := time.Now()
	q.inputs <- "/mnt/media/show/"

	want := filepath.FromSlash("/mnt/media/show")
	select {
	case scan := <-scans:
		if elapsed := time.Since(last); elapsed < q.delay {
			t.Errorf("Scan sent %v after the last event, want at least %v", elapsed, q.delay)
		}
		if scan.Folder != want || scan.Priority != 3 {
			t.Errorf("Got scan %+v, want folder %s with priority 3", scan, want)
		}
	case <-time.After(time.Second):
		t.Fatal("No scan sent within 1s")
	}

	select {
	case scan := <-scans:
		t.Errorf("Got a second scan %+v, want one scan for a repeated path", scan)
	case <-time.After(3 * q.delay):
	}
}
