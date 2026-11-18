//go:build linux

package inotify

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

// newWatchingDaemon returns a daemon that watches root with no rewrite and
// no filter.
func newWatchingDaemon(t *testing.T, root string) *daemon {
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

	d := &daemon{
		paths:   []path{{Path: root, Rewriter: rewriter, Allowed: filterer}},
		watcher: watcher,
		log:     zerolog.Nop(),
	}

	if _, err := d.watchTree(root); err != nil {
		t.Fatal(err)
	}

	return d
}

// receiveScans handles the events of d until no event arrives for a short
// time. It returns the paths that the events would queue.
func receiveScans(t *testing.T, d *daemon) []string {
	t.Helper()

	var scans []string
	for {
		select {
		case event := <-d.watcher.Events:
			if name, ok := d.scanPath(event); ok {
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

func assertWatched(t *testing.T, d *daemon, paths ...string) {
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
