package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/migrate"
	"github.com/l3uddz/autoscan/processor"
	"github.com/l3uddz/autoscan/triggers/inotify"
)

func TestShutdownFinishesOpenRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	requestErr := make(chan error, 1)
	go func() {
		res, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				err = fmt.Errorf("status %d", res.StatusCode)
			}
		}
		requestErr <- err
	}()
	<-started

	processorDone := make(chan struct{})
	close(processorDone)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan struct{})
	go func() {
		shutdown(ctx, []*http.Server{srv}, processorDone)
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the open request finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-requestErr; err != nil {
		t.Errorf("Open request failed: %v", err)
	}
	<-shutdownDone
}

func TestShutdownStopsWaitingForProcessorAtDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		shutdown(ctx, nil, make(chan struct{}))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited past its deadline for the processor")
	}
}

func TestShutdownStopsEveryServerWhileOneWaits(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	slow := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})}
	slowListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go slow.Serve(slowListener)

	other := &http.Server{Handler: http.NotFoundHandler()}
	otherListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go other.Serve(otherListener)

	go func() {
		if res, err := http.Get("http://" + slowListener.Addr().String()); err == nil {
			res.Body.Close()
		}
	}()
	<-started

	processorDone := make(chan struct{})
	close(processorDone)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan struct{})
	go func() {
		shutdown(ctx, []*http.Server{slow, other}, processorDone)
		close(shutdownDone)
	}()
	defer func() {
		close(release)
		<-shutdownDone
	}()

	// The second server stops accepting connections while the first still
	// waits for its open request.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("tcp", otherListener.Addr().String())
		if err != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the second server still accepts connections while the first waits")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScanStatsStopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		proc, _ := newRouterTestProcessor(t, processor.Config{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			scanStats(ctx, proc, time.Hour)
		}()

		synctest.Wait()
		start := time.Now()
		cancel()
		<-done
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("scanStats waited %v after cancellation, want immediate exit", elapsed)
		}
	})
}

func TestScansRemainingCancelsWhileDBConnectionHeld(t *testing.T) {
	proc, db := newRouterTestProcessor(t, processor.Config{})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = proc.ScansRemaining(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ScansRemaining returned %v, want context.DeadlineExceeded while sole DB connection is held", err)
	}
}

func TestNewWatchersCollectsAllErrors(t *testing.T) {
	configs := []inotify.Config{
		{Paths: []inotify.PathConfig{{Path: ""}}},
		{Paths: []inotify.PathConfig{{Path: "/mnt/tv", Rewrite: []autoscan.Rewrite{{From: "("}}}}},
	}

	watchers, err := newWatchers(configs, func(context.Context, ...autoscan.Scan) error { return nil })
	if watchers != nil {
		t.Errorf("newWatchers returned %v with an error", watchers)
	}
	if err == nil || !strings.Contains(err.Error(), "inotify-1") || !strings.Contains(err.Error(), "inotify-2") {
		t.Errorf("newWatchers error = %v, want errors naming inotify-1 and inotify-2", err)
	}
}

func TestAddContextStopsAtDeadlineWhenDatabaseLocked(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "autoscan.db")

	db, err := openDatabase(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mg, err := migrate.New(db, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	proc, err := processor.New(processor.Config{DB: db, Migrator: mg})
	if err != nil {
		t.Fatal(err)
	}

	// Hold an exclusive write lock on a separate connection to the same SQLite
	// file. openDatabase configures busy_timeout(5000); AddContext must stop at
	// its shorter context deadline rather than waiting the full 5 seconds.
	locker, err := openDatabase(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })

	lockTx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = proc.AddContext(ctx, autoscan.Scan{Folder: "/mnt/tv/Show", Time: time.Now()})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("AddContext succeeded while another connection held a write lock")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("AddContext waited %v under a 150ms deadline (busy_timeout overrun)", elapsed)
	}
}
