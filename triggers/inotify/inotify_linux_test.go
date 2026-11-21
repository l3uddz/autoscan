//go:build linux

package inotify

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

// newWatchingDaemon returns a trigger that watches root with no rewrite and
// no filter.
func newWatchingDaemon(t *testing.T, root string) *Trigger {
	t.Helper()

	rewriter, err := autoscan.NewRewriter(nil)
	if err != nil {
		t.Fatal(err)
	}

	filterer, err := autoscan.NewFilterer(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })

	d := &Trigger{
		paths:   []*path{{Path: root, Rewriter: rewriter, Allowed: filterer}},
		watcher: watcher,
		log:     zerolog.Nop(),
	}

	if _, err := d.watchTree(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	return d
}

// receiveScans handles the events of d until no event arrives for a short
// time. It returns the paths that the events would queue.
func receiveScans(t *testing.T, d *Trigger) []string {
	t.Helper()

	var scans []string
	for {
		select {
		case event := <-d.watcher.Events:
			d.rewatchLostRoot(event, func(p *path) { go d.watchPath(t.Context(), p) })
			if name, ok := d.scanPath(t.Context(), event); ok {
				scans = append(scans, name)
			}
		case err := <-d.watcher.Errors:
			t.Fatal(err)
		case <-time.After(500 * time.Millisecond):
			return scans
		}
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rename(t *testing.T, from, to string) {
	t.Helper()

	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func assertWatched(t *testing.T, d *Trigger, paths ...string) {
	t.Helper()

	watched := d.watcher.WatchList()
	for _, p := range paths {
		if !slices.Contains(watched, p) {
			t.Errorf("Directory %q is not watched, watched: %q", p, watched)
		}
	}
}

func TestScanPathMovedDirectoryWithFiles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	d := newWatchingDaemon(t, root)

	// The name has a dot, so the file extension heuristic would pick the parent.
	src := filepath.Join(outside, "Show.S01")
	mkdirAll(t, filepath.Join(src, "Season 1"))
	writeFile(t, filepath.Join(src, "Season 1", "episode.mkv"))

	dst := filepath.Join(root, "Show.S01")
	rename(t, src, dst)

	scans := receiveScans(t, d)
	if want := []string{dst}; !slices.Equal(scans, want) {
		t.Errorf("Got scans %q, want %q", scans, want)
	}

	assertWatched(t, d, dst, filepath.Join(dst, "Season 1"))
}

func TestScanPathEmptyNewDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	d := newWatchingDaemon(t, root)

	src := filepath.Join(outside, "Show")
	mkdirAll(t, filepath.Join(src, "Season 1"))

	dst := filepath.Join(root, "Show")
	rename(t, src, dst)

	if scans := receiveScans(t, d); len(scans) != 0 {
		t.Errorf("Got scans %q, want none", scans)
	}

	season := filepath.Join(dst, "Season 1")
	assertWatched(t, d, dst, season)

	// A file added later gets a scan of its directory from its own event.
	writeFile(t, filepath.Join(season, "episode.mkv"))

	scans := receiveScans(t, d)
	if want := []string{season}; !slices.Equal(scans, want) {
		t.Errorf("Got scans %q, want %q", scans, want)
	}
}

func newTestTrigger(t *testing.T, roots ...string) *Trigger {
	t.Helper()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })

	trigger := &Trigger{watcher: watcher, log: zerolog.Nop()}
	for _, root := range roots {
		trigger.paths = append(trigger.paths, &path{Path: root})
	}
	return trigger
}

func TestWatchTreeSkipsUnreadableSubdirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a directory without permissions")
	}

	root := t.TempDir()
	locked, open := filepath.Join(root, "locked"), filepath.Join(root, "open")
	mkdirAll(t, filepath.Join(locked, "inner"))
	mkdirAll(t, open)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	trigger := newTestTrigger(t, root)
	if _, err := trigger.watchTree(t.Context(), root); err != nil {
		t.Fatalf("watchTree returned %v, want the unreadable directory skipped", err)
	}

	watched := trigger.watcher.WatchList()
	assertWatched(t, trigger, root, open)
	if slices.Contains(watched, filepath.Join(locked, "inner")) {
		t.Errorf("Watched a directory below the unreadable directory: %q", watched)
	}
}

func TestWatchTreeFailsForMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	trigger := newTestTrigger(t, root)

	if _, err := trigger.watchTree(t.Context(), root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("watchTree returned %v, want a not-exist error", err)
	}
}

func TestStatusCountsWatchedPaths(t *testing.T) {
	watched := t.TempDir()
	trigger := newTestTrigger(t, watched, filepath.Join(t.TempDir(), "missing"))

	trigger.watchPath(t.Context(), trigger.paths[0])

	if got, want := trigger.Status(), (Status{Paths: 2, Watching: 1}); got != want {
		t.Errorf("Status = %+v, want %+v", got, want)
	}
}

func TestWatchTreeFailsForFileRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	writeFile(t, root)
	trigger := newTestTrigger(t, root)

	if _, err := trigger.watchTree(t.Context(), root); err == nil {
		t.Error("watchTree returned no error for a root that is a file")
	}
}

func TestRemovedRootIsWatchedAgain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	mkdirAll(t, root)
	trigger := newTestTrigger(t, root)
	trigger.watchPath(t.Context(), trigger.paths[0])

	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "the removed root to be not watched", func() bool {
		select {
		case event := <-trigger.watcher.Events:
			trigger.rewatchLostRoot(event, func(p *path) { go trigger.watchPath(t.Context(), p) })
		case <-time.After(10 * time.Millisecond):
		}
		return trigger.Status().Watching == 0
	})

	mkdirAll(t, root)
	waitFor(t, 2*watchRetryMin, "the recreated root to be watched", func() bool {
		return trigger.Status().Watching == 1
	})
	assertWatched(t, trigger, root)
}

func waitFor(t *testing.T, timeout time.Duration, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("Timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRenamedRootReleasesSubdirectoryWatches(t *testing.T) {
	parent := t.TempDir()
	root, nested := filepath.Join(parent, "media"), filepath.Join(parent, "media", "tv")
	mkdirAll(t, filepath.Join(nested, "Show"))
	trigger := newTestTrigger(t, root, nested)
	trigger.watchPath(t.Context(), trigger.paths[0])
	trigger.watchPath(t.Context(), trigger.paths[1])

	rename(t, root, filepath.Join(parent, "media.old"))
	waitFor(t, 2*time.Second, "the renamed root to be not watched", func() bool {
		select {
		case event := <-trigger.watcher.Events:
			trigger.rewatchLostRoot(event, func(p *path) { go trigger.watchPath(t.Context(), p) })
		case <-time.After(10 * time.Millisecond):
		}
		return trigger.Status().Watching == 0
	})

	for _, name := range trigger.watcher.WatchList() {
		if isWithin(name, root) {
			t.Errorf("Watch %q below the renamed root remains", name)
		}
	}
}

func TestRenamedAncestorLosesNestedRoot(t *testing.T) {
	parent := t.TempDir()
	media := filepath.Join(parent, "media")
	show := filepath.Join(media, "tv", "Show")
	mkdirAll(t, show)
	trigger := newTestTrigger(t, media, show)
	trigger.watchPath(t.Context(), trigger.paths[0])
	trigger.watchPath(t.Context(), trigger.paths[1])

	// Moving media/tv out of media loses the root media/tv/Show, but not media.
	rename(t, filepath.Join(media, "tv"), filepath.Join(parent, "tv"))
	waitFor(t, 2*time.Second, "the nested root to be not watched", func() bool {
		select {
		case event := <-trigger.watcher.Events:
			trigger.rewatchLostRoot(event, func(p *path) { go trigger.watchPath(t.Context(), p) })
		case <-time.After(10 * time.Millisecond):
		}
		return !trigger.paths[1].watching.Load()
	})

	if !trigger.paths[0].watching.Load() {
		t.Error("The root above the moved directory is marked as not watched")
	}
	for _, name := range trigger.watcher.WatchList() {
		if isWithin(name, filepath.Join(media, "tv")) {
			t.Errorf("Watch %q below the moved directory remains", name)
		}
	}
}

func TestRenamedSubdirectoryKeepsWatchingNewPath(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	mkdirAll(t, filepath.Join(show, "Season 1"))
	d := newWatchingDaemon(t, root)

	renamed := filepath.Join(root, "Show (2024)")
	rename(t, show, renamed)
	_ = receiveScans(t, d)

	season := filepath.Join(renamed, "Season 1")
	writeFile(t, filepath.Join(season, "episode.mkv"))

	scans := receiveScans(t, d)
	if !slices.Contains(scans, season) {
		t.Errorf("Got scans %q, want %q after renaming parent directory", scans, season)
	}
}

func TestSubdirectoryMovedOutsideRootReleasesWatches(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	show := filepath.Join(root, "Show")
	mkdirAll(t, filepath.Join(show, "Season 1"))
	d := newWatchingDaemon(t, root)

	rename(t, show, filepath.Join(outside, "Show"))
	_ = receiveScans(t, d)

	for _, name := range d.watcher.WatchList() {
		if isWithin(name, show) {
			t.Errorf("Watch %q below the moved directory remains", name)
		}
	}
}

func TestScanPathCreatedFileWithoutExtensionScansParent(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	mkdirAll(t, show)
	d := newWatchingDaemon(t, root)

	writeFile(t, filepath.Join(show, "README"))

	scans := receiveScans(t, d)
	if want := []string{show}; !slices.Equal(scans, want) {
		t.Errorf("Got scans %q, want %q", scans, want)
	}
}

func TestTriggerRunStopsOnCancel(t *testing.T) {
	watched := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	trigger, err := New(Config{
		Paths: []PathConfig{{Path: watched}, {Path: missing}},
	}, func(...autoscan.Scan) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		trigger.Run(ctx)
	}()

	waitFor(t, 2*time.Second, "watched path to start", func() bool {
		return trigger.Status().Watching == 1
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Trigger.Run did not exit after context cancellation")
	}
}
