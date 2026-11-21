// Package inotify is the trigger that watches local directories with
// inotify and makes a scan for each folder that changes.
package inotify

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	Paths     []PathConfig       `yaml:"paths"`
}

// PathConfig is one watched path. Its rules apply before the rules of the
// trigger.
type PathConfig struct {
	Path    string             `yaml:"path"`
	Rewrite []autoscan.Rewrite `yaml:"rewrite"`
	Include []string           `yaml:"include"`
	Exclude []string           `yaml:"exclude"`
}

// errEmptyPath means that a path entry has no path.
var errEmptyPath = errors.New("inotify path is empty")

const (
	// watchRetryMin and watchRetryMax limit the delay before autoscan tries
	// again to watch a path that failed. The delay doubles after each failure.
	watchRetryMin = 5 * time.Second
	watchRetryMax = 5 * time.Minute
)

type watchEntry struct {
	id    fileID
	hasID bool
}

// A Trigger watches the paths of one inotify configuration and sends the
// scans to the processor.
type Trigger struct {
	paths    []*path
	watcher  *fsnotify.Watcher
	queue    *queue
	log      zerolog.Logger
	callback func(context.Context, ...autoscan.Scan) error
	priority int

	watchedMu       sync.Mutex
	watched         map[string]watchEntry
	watchedByID     map[fileID]string
	watchedSubtrees map[string]int

	walkDir func(string, fs.WalkDirFunc) error
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
	return NewWithContext(c, func(_ context.Context, scans ...autoscan.Scan) error {
		return callback(scans...)
	})
}

// NewWithContext returns a trigger like New whose callback receives a context
// so datastore writes respect cancellation and the shutdown flush budget.
func NewWithContext(c Config, callback func(context.Context, ...autoscan.Scan) error) (*Trigger, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("trigger", "inotify").
		Logger()

	var paths []*path
	for _, p := range c.Paths {
		if p.Path == "" {
			return nil, errEmptyPath
		}
		rewriter, err := autoscan.NewRewriter(slices.Concat(p.Rewrite, c.Rewrite))
		if err != nil {
			return nil, err
		}

		filterer, err := autoscan.NewFilterer(slices.Concat(p.Include, c.Include), slices.Concat(p.Exclude, c.Exclude))
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

// Run watches the paths and handles their events until ctx ends.
// Each path starts on its own. If a path fails, Run tries it again later,
// so one failed path does not stop the others. When ctx ends, Run closes
// the watcher and flushes pending scans to the processor.
func (t *Trigger) Run(ctx context.Context) {
	if !retry(ctx, t.log, "Failed creating filesystem watcher", "", func() (err error) {
		t.watcher, err = fsnotify.NewWatcher()
		return err
	}) {
		return
	}
	t.queue = newQueue(ctx, t.callback, t.log, t.priority)

	// Drain watcher errors independently of event handling: fsnotify can
	// send on the unbuffered Errors channel while holding its internal mutex,
	// which worker calls like Add and Remove also acquire.
	var errWG sync.WaitGroup
	errWG.Go(t.drainErrors)

	var watchWG sync.WaitGroup
	startWatch := func(p *path) {
		watchWG.Go(func() {
			t.watchPath(ctx, p)
		})
	}
	for _, p := range t.paths {
		startWatch(p)
	}

	t.worker(ctx, startWatch)
	t.queue.stop()
	if err := t.watcher.Close(); err != nil {
		t.log.Warn().Err(err).Msg("Failed closing filesystem watcher")
	}
	errWG.Wait()
	watchWG.Wait()
}

func (t *Trigger) drainErrors() {
	for err := range t.watcher.Errors {
		t.log.Error().
			Err(err).
			Msg("Failed receiving filesystem events")
	}
}

// watchPath watches the tree at p and tries again until it succeeds or ctx ends.
func (t *Trigger) watchPath(ctx context.Context, p *path) {
	for {
		var losses int64
		if !retry(ctx, t.log, "Failed watching path", p.Path, func() error {
			losses = p.losses.Load()
			_, err := t.watchTree(ctx, p.Path)
			return err
		}) {
			return
		}

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

// retry calls try until it returns nil or ctx ends. It logs each error with
// msg and path and waits a longer delay after each failure. It returns true
// when try succeeded.
func retry(ctx context.Context, log zerolog.Logger, msg string, path string, try func() error) bool {
	delay := watchRetryMin
	for {
		err := try()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}

		log.Error().
			Err(err).
			Str("path", path).
			Dur("retry_in", delay).
			Msg(msg)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
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
func (t *Trigger) watchTree(ctx context.Context, root string) (bool, error) {
	walk := t.walkDir
	if walk == nil {
		walk = filepath.WalkDir
	}
	hasFile := false
	err := walk(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && !d.IsDir() {
			if path == root {
				return fmt.Errorf("%v: not a directory", path)
			}
			hasFile = true
			return nil
		}

		if err == nil {
			err = t.watchDir(path)
		}
		if err == nil {
			return nil
		}
		return t.walkError(root, path, d, err)
	})

	return hasFile, err
}

// watchDir adds a watch for the directory at path. It holds watchedMu across
// watcher.Add and updating the index so concurrent rename cleanup never misses
// a newly registered watch. If path was moved from another watched tree before
// cleanup removed the old watch, watchDir evicts the stale source watch first
// so fsnotify binds the watch descriptor to path instead of keeping the old
// path for the same inode.
func (t *Trigger) watchDir(path string) error {
	clean := filepath.Clean(path)
	t.watchedMu.Lock()
	err := t.watchDirLocked(clean)
	t.watchedMu.Unlock()
	if err != nil {
		return fmt.Errorf("watch directory: %w", err)
	}

	t.log.Trace().
		Str("path", clean).
		Msg("Watching directory")
	return nil
}

func (t *Trigger) watchDirLocked(clean string) error {
	id, hasID := statFileID(clean)
	if hasID {
		t.evictStaleWatchLocked(clean, id)
	}
	if err := t.watcher.Add(clean); err != nil {
		return err
	}
	t.addWatchedLocked(clean, id, hasID)
	return nil
}

func (t *Trigger) evictStaleWatchLocked(clean string, id fileID) {
	if existing, ok := t.watched[clean]; ok && existing.hasID && existing.id != id {
		t.deleteWatchedLocked(clean)
		_ = t.watcher.Remove(clean)
	}

	oldPath, ok := t.watchedByID[id]
	if !ok || oldPath == clean {
		return
	}
	if curID, curOK := statFileID(oldPath); curOK && curID == id {
		return
	}
	t.deleteWatchedLocked(oldPath)
	_ = t.watcher.Remove(oldPath)
}

func (t *Trigger) addWatchedLocked(clean string, id fileID, hasID bool) {
	if t.watched == nil {
		t.watched = make(map[string]watchEntry)
		t.watchedByID = make(map[fileID]string)
		t.watchedSubtrees = make(map[string]int)
	}
	prev, exists := t.watched[clean]
	if exists && prev.hasID && t.watchedByID[prev.id] == clean {
		delete(t.watchedByID, prev.id)
	}
	t.watched[clean] = watchEntry{id: id, hasID: hasID}
	if hasID {
		if _, owned := t.watchedByID[id]; !owned {
			t.watchedByID[id] = clean
		}
	}
	if !exists {
		t.adjustSubtreeCounts(clean, 1)
	}
}

func (t *Trigger) deleteWatchedLocked(clean string) {
	entry, exists := t.watched[clean]
	if !exists {
		return
	}
	delete(t.watched, clean)
	if entry.hasID && t.watchedByID[entry.id] == clean {
		delete(t.watchedByID, entry.id)
	}
	t.adjustSubtreeCounts(clean, -1)
}

func (t *Trigger) adjustSubtreeCounts(dir string, delta int) {
	for cur := dir; ; {
		if next := t.watchedSubtrees[cur] + delta; next <= 0 {
			delete(t.watchedSubtrees, cur)
		} else {
			t.watchedSubtrees[cur] = next
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return
		}
		cur = parent
	}
}

// removeWatchedBelow removes all watched directories at or below lost from the
// index and the watcher. An ordinary file path with no watches at or below it
// returns in O(1) without scanning the watch index or locking fsnotify.
func (t *Trigger) removeWatchedBelow(lost string) {
	t.watchedMu.Lock()
	defer t.watchedMu.Unlock()

	if t.watchedSubtrees[lost] == 0 {
		return
	}
	for name := range t.watched {
		if isWithin(name, lost) {
			t.deleteWatchedLocked(name)
			_ = t.watcher.Remove(name)
		}
	}
}

// walkError returns what the walk of watchTree does after err at path. It
// fails the walk at root and at the inotify watch limit. Below root, it logs
// a warning and skips the entry.
func (t *Trigger) walkError(root, path string, d fs.DirEntry, err error) error {
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
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
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

func (t *Trigger) worker(ctx context.Context, startWatch func(*path)) {
	// process events
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-t.watcher.Events:
			if !ok || !t.handleEvent(ctx, event, startWatch) {
				return
			}
		}
	}
}

func (t *Trigger) handleEvent(ctx context.Context, event fsnotify.Event, startWatch func(*path)) bool {
	t.rewatchLostRoot(event, startWatch)
	name, ok := t.scanPath(ctx, event)
	if !ok {
		return true
	}
	select {
	case t.queue.inputs <- name:
		return true
	case <-ctx.Done():
		return false
	}
}

// rewatchLostRoot releases watches below a removed or renamed directory, and
// restarts watching any configured root that was lost.
func (t *Trigger) rewatchLostRoot(event fsnotify.Event, startWatch func(*path)) {
	if !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return
	}

	// After a rename, Linux keeps the subdirectory watches on the moved
	// inodes under their old paths. Remove them for any renamed or removed
	// watched directory so moved trees get fresh watches and do not leak.
	lost := filepath.Clean(event.Name)
	t.removeWatchedBelow(lost)

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
		startWatch(p)
	}
}

// scanPath handles a filesystem event. It returns the path to scan and
// reports whether the event needs a scan.
func (t *Trigger) scanPath(ctx context.Context, event fsnotify.Event) (string, bool) {
	// new filesystem event
	t.log.Trace().
		Interface("event", event).
		Msg("Filesystem event")

	isDir, isFile := false, false
	switch {
	case event.Has(fsnotify.Create):
		var ok bool
		isDir, isFile, ok = t.handleCreate(ctx, event.Name)
		if !ok {
			return "", false
		}

	case event.Has(fsnotify.Rename), event.Has(fsnotify.Remove):
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

	// Use the parent directory for a known file or for a removed/renamed
	// path that looks like a file with an extension.
	if isFile || (!isDir && filepath.Ext(rewritten) != "") {
		rewritten = filepath.Dir(rewritten)
	}

	return rewritten, true
}

func (t *Trigger) handleCreate(ctx context.Context, name string) (isDir bool, isFile bool, ok bool) {
	fi, err := os.Stat(name)
	if err != nil {
		t.log.Error().
			Err(err).
			Str("path", name).
			Msg("Failed retrieving filesystem info")
		return false, false, false
	}

	if !fi.IsDir() {
		return false, true, true
	}

	// watch new directories and scan them only if they contain a file
	hasFile, err := t.watchTree(ctx, name)
	if err != nil && ctx.Err() == nil {
		t.log.Error().
			Err(err).
			Str("path", name).
			Msg("Failed watching new directory")
	}

	if !hasFile {
		return false, false, false
	}

	return true, false, true
}

const (
	// queueDelay is how long a path waits after its last event.
	// Further events for the same path in that time restart the wait.
	queueDelay = 10 * time.Second

	// queueInterval is how often the queue looks for paths that are ready.
	queueInterval = time.Second

	queueRetryMin = time.Second
	queueRetryMax = time.Minute

	// flushRetryTimeout and flushRetryDelay bound how long shutdown retries a
	// failed final flush (for example, a transient SQLite lock while active
	// deliveries finish) before giving up so shutdown stays within its budget.
	flushRetryTimeout = 2 * time.Second
	flushRetryDelay   = 100 * time.Millisecond
)

type pendingScan struct {
	readyAt    time.Time
	retryDelay time.Duration
}

// A queue holds filesystem paths until the processor accepts them.
// Only the worker goroutine reads or writes scans.
type queue struct {
	callback func(context.Context, ...autoscan.Scan) error
	log      zerolog.Logger
	priority int
	inputs   chan string
	done     chan struct{}
	scans    map[string]pendingScan
}

func newQueue(ctx context.Context, cb func(context.Context, ...autoscan.Scan) error, log zerolog.Logger, priority int) *queue {
	q := &queue{
		callback: cb,
		log:      log,
		priority: priority,
		inputs:   make(chan string),
		done:     make(chan struct{}),
		scans:    make(map[string]pendingScan),
	}

	go q.worker(ctx)

	return q
}

func (q *queue) stop() {
	close(q.inputs)
	<-q.done
}

// worker debounces incoming paths and flushes pending scans as soon as ctx
// ends or inputs closes, so a stalled filesystem walk in the event worker
// cannot delay saving scans from healthy roots during shutdown.
func (q *queue) worker(ctx context.Context) {
	defer close(q.done)
	ticker := time.NewTicker(queueInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.flush()
			return

		case path, ok := <-q.inputs:
			if !ok {
				q.flush()
				return
			}
			q.enqueue(path)

		case <-ticker.C:
			q.process(ctx)
		}
	}
}

func (q *queue) enqueue(path string) {
	scan := q.scans[path]
	// A new event restarts the normal wait without shortening a retry.
	if readyAt := time.Now().Add(queueDelay); readyAt.After(scan.readyAt) {
		scan.readyAt = readyAt
		q.scans[path] = scan
	}
}

func (q *queue) flush() {
	if len(q.scans) == 0 {
		return
	}

	now := time.Now()
	scans := make([]autoscan.Scan, 0, len(q.scans))
	for path := range q.scans {
		scans = append(scans, autoscan.Scan{
			Folder:   filepath.Clean(path),
			Priority: q.priority,
			Time:     now,
		})
	}

	flushCtx, cancel := context.WithTimeout(context.Background(), flushRetryTimeout)
	defer cancel()

	for {
		err := q.callback(flushCtx, scans...)
		if err == nil {
			clear(q.scans)
			for _, scan := range scans {
				q.log.Info().
					Str("path", scan.Folder).
					Msg("Scan flushed to processor")
			}
			return
		}
		if flushCtx.Err() != nil || !waitForRetry(flushCtx, flushRetryDelay) {
			q.log.Error().
				Err(err).
				Int("count", len(scans)).
				Msg("Failed flushing pending scans to processor during shutdown")
			return
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (q *queue) process(ctx context.Context) {
	for path, scan := range q.scans {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if now.Before(scan.readyAt) {
			continue
		}

		err := q.callback(ctx, autoscan.Scan{
			Folder:   filepath.Clean(path),
			Priority: q.priority,
			Time:     now,
		})

		if err != nil {
			if ctx.Err() != nil {
				return
			}
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
