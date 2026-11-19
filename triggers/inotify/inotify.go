package inotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

type Config struct {
	Priority  int                `yaml:"priority"`
	Verbosity string             `yaml:"verbosity"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Include   []string           `yaml:"include"`
	Exclude   []string           `yaml:"exclude"`
	Paths     []struct {
		Path    string             `yaml:"path"`
		Rewrite []autoscan.Rewrite `yaml:"rewrite"`
		Include []string           `yaml:"include"`
		Exclude []string           `yaml:"exclude"`
	} `yaml:"paths"`
}

const (
	// watchRetryMin and watchRetryMax limit the delay before autoscan tries
	// again to watch a path that failed. The delay doubles after each failure.
	watchRetryMin = 5 * time.Second
	watchRetryMax = 5 * time.Minute
)

// A Trigger watches the paths of one inotify configuration and sends the
// scans to the processor.
type Trigger struct {
	paths    []*path
	watcher  *fsnotify.Watcher
	queue    *queue
	log      zerolog.Logger
	callback autoscan.ProcessorFunc
	priority int
}

type path struct {
	Path     string
	Rewriter autoscan.Rewriter
	Allowed  autoscan.Filterer

	// watching reports whether the trigger watches the tree at Path.
	watching atomic.Bool

	// losses counts the removals and renames of the root at Path.
	losses atomic.Int64
}

// Status reports how many paths of a trigger it watches.
type Status struct {
	Paths    int `json:"paths"`
	Watching int `json:"watching"`
}

// New returns a trigger that watches the paths of c and sends the scans to
// callback. The caller starts it with Run.
func New(c Config, callback autoscan.ProcessorFunc) (*Trigger, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("trigger", "inotify").
		Logger()

	var paths []*path
	for _, p := range c.Paths {
		rewriter, err := autoscan.NewRewriter(append(p.Rewrite, c.Rewrite...))
		if err != nil {
			return nil, err
		}

		filterer, err := autoscan.NewFilterer(append(p.Include, c.Include...), append(p.Exclude, c.Exclude...))
		if err != nil {
			return nil, err
		}

		paths = append(paths, &path{
			Path:     p.Path,
			Rewriter: rewriter,
			Allowed:  filterer,
		})
	}

	return &Trigger{
		paths:    paths,
		log:      l,
		callback: callback,
		priority: c.Priority,
	}, nil
}

// Status returns the number of paths and the number of watched paths.
func (t *Trigger) Status() Status {
	status := Status{Paths: len(t.paths)}
	for _, p := range t.paths {
		if p.watching.Load() {
			status.Watching++
		}
	}
	return status
}

// Run watches the paths and handles their events. It does not return.
// Each path starts on its own. If a path fails, Run tries it again later,
// so one failed path does not stop the others.
func (t *Trigger) Run() {
	retry(t.log, "Failed creating filesystem watcher", "", func() (err error) {
		t.watcher, err = fsnotify.NewWatcher()
		return err
	})
	t.queue = newQueue(t.callback, t.log, t.priority)

	for _, p := range t.paths {
		go t.watchPath(p)
	}

	t.worker()
}

// watchPath watches the tree at p and tries again until it succeeds.
func (t *Trigger) watchPath(p *path) {
	for {
		var losses int64
		retry(t.log, "Failed watching path", p.Path, func() error {
			losses = p.losses.Load()
			_, err := t.watchTree(p.Path)
			return err
		})

		p.watching.Store(true)
		watched, again := checkRootLoss(p, losses)
		if watched {
			t.log.Info().
				Str("path", p.Path).
				Msg("Watching path")
		}
		if !again {
			return
		}

		t.log.Warn().
			Str("path", p.Path).
			Msg("Watched path was removed while autoscan set up its watch, watching it again when it exists")
	}
}

// checkRootLoss runs after a walk marks p as watched. The walk started when
// p had the given number of root losses. checkRootLoss reports whether p is
// still watched, and whether the walk must run again because the root was
// lost. rewatchLostRoot ignores a loss while p is not watched, so a loss
// before the mark is found here. If the loss came after the mark,
// rewatchLostRoot and checkRootLoss both try to clear the mark, and only
// the one that clears it starts the next walk.
func checkRootLoss(p *path, losses int64) (watched bool, again bool) {
	if p.losses.Load() == losses {
		return true, false
	}
	return false, p.watching.CompareAndSwap(true, false)
}

// retry calls try until it returns nil. It logs each error with msg and path
// and waits a longer delay after each failure.
func retry(log zerolog.Logger, msg string, path string, try func() error) {
	delay := watchRetryMin
	for {
		err := try()
		if err == nil {
			return
		}

		log.Error().
			Err(err).
			Str("path", path).
			Dur("retry_in", delay).
			Msg(msg)
		time.Sleep(delay)
		delay = min(watchRetryMax, 2*delay)
	}
}

// watchTree watches every directory in the tree at root. It reports whether
// the walk saw a file, which is any entry that is not a directory.
//
// An error at root fails the walk. Below root, the walk skips an entry that
// it cannot read or watch, with a warning, because a retry does not fix a
// directory without permission or one that a user removed during the walk.
// The inotify watch limit fails the walk, because a retry can succeed after
// the user raises the limit.
func (t *Trigger) watchTree(root string) (bool, error) {
	hasFile := false
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			if path == root {
				return fmt.Errorf("%v: not a directory", path)
			}
			hasFile = true
			return nil
		}

		if err == nil {
			if err = t.watcher.Add(path); err == nil {
				t.log.Trace().
					Str("path", path).
					Msg("Watching directory")
				return nil
			}
			err = fmt.Errorf("watch directory: %w", err)
		}

		if errors.Is(err, syscall.ENOSPC) {
			return fmt.Errorf("%v: %w: the inotify watch limit is too low, raise fs.inotify.max_user_watches", path, err)
		}
		if path == root {
			return fmt.Errorf("%v: %w", path, err)
		}

		t.log.Warn().
			Err(err).
			Str("path", path).
			Msg("Skipped directory")
		if fi != nil && fi.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})

	return hasFile, err
}

// getPathObject returns the watched path that contains name. If more than
// one watched path contains name, the longest one wins.
func (t *Trigger) getPathObject(name string) (*path, error) {
	var match *path
	matchLen := -1

	for _, p := range t.paths {
		root := filepath.Clean(p.Path)
		if !isWithin(name, root) || len(root) <= matchLen {
			continue
		}

		match = p
		matchLen = len(root)
	}

	if match == nil {
		return nil, fmt.Errorf("path object not found: %v", name)
	}

	return match, nil
}

// isWithin reports whether name is root or is below root at a directory
// boundary. filepath.Rel also handles the roots "/" and ".". It returns an
// error if only one of the two paths is absolute.
func isWithin(name, root string) bool {
	rel, err := filepath.Rel(root, name)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (t *Trigger) worker() {
	// process events
	for {
		select {
		case event := <-t.watcher.Events:
			t.rewatchLostRoot(event)
			if name, ok := t.scanPath(event); ok {
				// move to queue
				t.queue.inputs <- name
			}

		case err := <-t.watcher.Errors:
			t.log.Error().
				Err(err).
				Msg("Failed receiving filesystem events")
		}
	}
}

// rewatchLostRoot handles the removal or rename of a watched root. The root
// then has no watch, so the trigger marks it as not watched and tries again
// to watch it, until the path exists again. A configured root below the lost
// root is lost too.
func (t *Trigger) rewatchLostRoot(event fsnotify.Event) {
	if !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return
	}

	lost := filepath.Clean(event.Name)
	if !slices.ContainsFunc(t.paths, func(p *path) bool { return filepath.Clean(p.Path) == lost }) {
		return
	}

	// After a rename, the subdirectories keep their own watches, on the
	// moved tree. Remove them, so that the next walk of the path does not
	// add a second watch for each directory. The kernel already removed the
	// watches of a deleted tree, so errors do not matter.
	for _, name := range t.watcher.WatchList() {
		if isWithin(name, lost) {
			_ = t.watcher.Remove(name)
		}
	}

	for _, p := range t.paths {
		if !isWithin(filepath.Clean(p.Path), lost) {
			continue
		}

		// Count the loss before the mark is cleared, so that a walk that
		// marks the path at the same time sees it. See checkRootLoss.
		p.losses.Add(1)
		if !p.watching.CompareAndSwap(true, false) {
			continue
		}

		t.log.Warn().
			Str("path", p.Path).
			Msg("Watched path was removed, watching it again when it exists")
		go t.watchPath(p)
	}
}

// scanPath handles a filesystem event. It returns the path to scan and
// reports whether the event needs a scan.
func (t *Trigger) scanPath(event fsnotify.Event) (string, bool) {
	// new filesystem event
	t.log.Trace().
		Interface("event", event).
		Msg("Filesystem event")

	isDir := false

	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		// create
		fi, err := os.Stat(event.Name)
		if err != nil {
			t.log.Error().
				Err(err).
				Str("path", event.Name).
				Msg("Failed retrieving filesystem info")
			return "", false
		}

		// watch new directories and scan them only if they contain a file
		if fi.IsDir() {
			hasFile, err := t.watchTree(event.Name)
			if err != nil {
				t.log.Error().
					Err(err).
					Str("path", event.Name).
					Msg("Failed watching new directory")
			}

			if !hasFile {
				return "", false
			}

			isDir = true
		}

	case event.Op&fsnotify.Rename == fsnotify.Rename, event.Op&fsnotify.Remove == fsnotify.Remove:
		// renamed / removed
	default:
		// ignore this event
		return "", false
	}

	// get path object
	p, err := t.getPathObject(event.Name)
	if err != nil {
		t.log.Error().
			Err(err).
			Str("path", event.Name).
			Msg("Failed determining path object")
		return "", false
	}

	// rewrite
	rewritten := p.Rewriter(event.Name)

	// filter
	if !p.Allowed(rewritten) {
		return "", false
	}

	// get directory where path has an extension
	if !isDir && filepath.Ext(rewritten) != "" {
		// there was most likely a file extension, use the directory
		rewritten = filepath.Dir(rewritten)
	}

	return rewritten, true
}

const (
	// queueDelay is how long a path waits after its last event.
	// Further events for the same path in that time restart the wait.
	queueDelay = 10 * time.Second

	// queueInterval is how often the queue looks for paths that are ready.
	queueInterval = time.Second

	queueRetryMin = time.Second
	queueRetryMax = time.Minute
)

type pendingScan struct {
	readyAt    time.Time
	retryDelay time.Duration
}

// A queue holds filesystem paths until the processor accepts them.
// Only the worker goroutine reads or writes scans.
type queue struct {
	callback autoscan.ProcessorFunc
	log      zerolog.Logger
	priority int
	inputs   chan string
	scans    map[string]pendingScan
}

func newQueue(cb autoscan.ProcessorFunc, log zerolog.Logger, priority int) *queue {
	q := &queue{
		callback: cb,
		log:      log,
		priority: priority,
		inputs:   make(chan string),
		scans:    make(map[string]pendingScan),
	}

	go q.worker()

	return q
}

func (q *queue) worker() {
	ticker := time.NewTicker(queueInterval)
	defer ticker.Stop()

	for {
		select {
		case path, ok := <-q.inputs:
			if !ok {
				// channel closed
				return
			}

			scan := q.scans[path]
			// A new event restarts the normal wait without shortening a retry.
			if readyAt := time.Now().Add(queueDelay); readyAt.After(scan.readyAt) {
				scan.readyAt = readyAt
				q.scans[path] = scan
			}

		case <-ticker.C:
			q.process()
		}
	}
}

func (q *queue) process() {
	for path, scan := range q.scans {
		now := time.Now()
		if now.Before(scan.readyAt) {
			continue
		}

		err := q.callback(autoscan.Scan{
			Folder:   filepath.Clean(path),
			Priority: q.priority,
			Time:     now,
		})

		if err != nil {
			scan.retryDelay = min(queueRetryMax, max(queueRetryMin, 2*scan.retryDelay))
			scan.readyAt = time.Now().Add(scan.retryDelay)
			q.scans[path] = scan
			q.log.Error().
				Err(err).
				Str("path", path).
				Dur("retry_delay", scan.retryDelay).
				Msg("Failed moving scan to processor, will retry")
			continue
		}

		delete(q.scans, path)
		q.log.Info().
			Str("path", path).
			Msg("Scan moved to processor")
	}
}
