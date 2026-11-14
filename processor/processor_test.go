package processor

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
)

type fakeTarget struct {
	err error

	mu    sync.Mutex
	scans []autoscan.Scan
}

func (f *fakeTarget) Scan(scan autoscan.Scan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans = append(f.scans, scan)
	return f.err
}

func (f *fakeTarget) Available() error {
	return f.err
}

func newTestProcessor(t *testing.T) *Processor {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	proc, err := New(Config{Db: db, Mg: mg})
	if err != nil {
		t.Fatal(err)
	}

	return proc
}

func TestCheckAvailability(t *testing.T) {
	proc := newTestProcessor(t)

	if err := proc.CheckAvailability([]autoscan.Target{&fakeTarget{}, &fakeTarget{}}); err != nil {
		t.Errorf("Got error %v, want nil when all targets are available", err)
	}

	down := &fakeTarget{err: autoscan.ErrTargetUnavailable}
	if err := proc.CheckAvailability([]autoscan.Target{&fakeTarget{}, down}); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v when one target is unavailable", err, autoscan.ErrTargetUnavailable)
	}
}

func TestProcessSendsScanToAllTargets(t *testing.T) {
	proc := newTestProcessor(t)
	scan := autoscan.Scan{Folder: "/mnt/media/show", Priority: 1, Time: time.Now().Add(-time.Hour)}
	if err := proc.Add(scan); err != nil {
		t.Fatal(err)
	}

	targets := []*fakeTarget{{}, {}}
	if err := proc.Process([]autoscan.Target{targets[0], targets[1]}); err != nil {
		t.Fatalf("Process returned %v", err)
	}

	for i, target := range targets {
		if len(target.scans) != 1 || target.scans[0].Folder != scan.Folder {
			t.Errorf("Target %d got scans %+v, want one scan for %s", i, target.scans, scan.Folder)
		}
	}

	if remaining, err := proc.ScansRemaining(); err != nil || remaining != 0 {
		t.Errorf("ScansRemaining = %d, %v, want 0, nil", remaining, err)
	}
	if processed := proc.ScansProcessed(); processed != 1 {
		t.Errorf("ScansProcessed = %d, want 1", processed)
	}
}

func TestProcessKeepsScanWhenTargetFails(t *testing.T) {
	proc := newTestProcessor(t)
	scan := autoscan.Scan{Folder: "/mnt/media/show", Priority: 1, Time: time.Now().Add(-time.Hour)}
	if err := proc.Add(scan); err != nil {
		t.Fatal(err)
	}

	down := &fakeTarget{err: autoscan.ErrTargetUnavailable}
	if err := proc.Process([]autoscan.Target{&fakeTarget{}, down}); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Fatalf("Process returned %v, want %v", err, autoscan.ErrTargetUnavailable)
	}

	if remaining, err := proc.ScansRemaining(); err != nil || remaining != 1 {
		t.Errorf("ScansRemaining = %d, %v, want 1, nil", remaining, err)
	}
}
