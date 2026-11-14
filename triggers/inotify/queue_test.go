package inotify

import (
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

	start := time.Now()
	q.inputs <- "/mnt/media/show/"
	q.inputs <- "/mnt/media/show/"

	select {
	case scan := <-scans:
		if elapsed := time.Since(start); elapsed < q.delay {
			t.Errorf("Scan sent after %v, want at least %v", elapsed, q.delay)
		}
		if scan.Folder != "/mnt/media/show" || scan.Priority != 3 {
			t.Errorf("Got scan %+v, want folder /mnt/media/show with priority 3", scan)
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
