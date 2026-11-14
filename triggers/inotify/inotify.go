package inotify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

type daemon struct {
	callback autoscan.ProcessorFunc
	paths    []path
	watcher  *fsnotify.Watcher
	queue    *queue
	log      zerolog.Logger
}

type path struct {
	Path     string
	Rewriter autoscan.Rewriter
	Allowed  autoscan.Filterer
}

func New(c Config) (autoscan.Trigger, error) {
	l := autoscan.GetLogger(c.Verbosity).With().
		Str("trigger", "inotify").
		Logger()

	var paths []path
	for _, p := range c.Paths {
		p := p

		rewriter, err := autoscan.NewRewriter(append(p.Rewrite, c.Rewrite...))
		if err != nil {
			return nil, err
		}

		filterer, err := autoscan.NewFilterer(append(p.Include, c.Include...), append(p.Exclude, c.Exclude...))
		if err != nil {
			return nil, err
		}

		paths = append(paths, path{
			Path:     p.Path,
			Rewriter: rewriter,
			Allowed:  filterer,
		})
	}

	trigger := func(callback autoscan.ProcessorFunc) {
		d := daemon{
			log:      l,
			callback: callback,
			paths:    paths,
			queue:    newQueue(callback, l, c.Priority),
		}

		// start job(s)
		if err := d.startMonitoring(); err != nil {
			l.Error().
				Err(err).
				Msg("Failed initialising jobs")
			return
		}
	}

	return trigger, nil
}

func (d *daemon) startMonitoring() error {
	// create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	d.watcher = watcher

	// setup watcher
	for _, p := range d.paths {
		if err := filepath.Walk(p.Path, d.walkFunc); err != nil {
			_ = d.watcher.Close()
			return err
		}
	}

	// start worker
	go d.worker()

	return nil
}

func (d *daemon) walkFunc(path string, fi os.FileInfo, err error) error {
	// handle error
	if err != nil {
		return fmt.Errorf("walk func: %v: %w", path, err)
	}

	// ignore non-directory
	if !fi.Mode().IsDir() {
		return nil
	}

	if err := d.watcher.Add(path); err != nil {
		return fmt.Errorf("watch directory: %v: %w", path, err)
	}

	d.log.Trace().
		Str("path", path).
		Msg("Watching directory")

	return nil
}

// getPathObject returns the watched path that contains name. If more than
// one watched path contains name, the longest one wins.
func (d *daemon) getPathObject(name string) (*path, error) {
	var match *path
	matchLen := -1

	for i := range d.paths {
		root := filepath.Clean(d.paths[i].Path)
		if !isWithin(name, root) || len(root) <= matchLen {
			continue
		}

		match = &d.paths[i]
		matchLen = len(root)
	}

	if match == nil {
		return nil, fmt.Errorf("path object not found: %v", name)
	}

	return match, nil
}

// isWithin reports whether name is root or is below root at a directory
// boundary. root must be clean.
func isWithin(name, root string) bool {
	name = filepath.Clean(name)
	if name == root {
		return true
	}

	// The root path "/" already ends with the separator.
	prefix := strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(name, prefix)
}

func (d *daemon) worker() {
	// close watcher
	defer d.watcher.Close()

	// process events
	for {
		select {
		case event := <-d.watcher.Events:
			// new filesystem event
			d.log.Trace().
				Interface("event", event).
				Msg("Filesystem event")

			switch {
			case event.Op&fsnotify.Create == fsnotify.Create:
				// create
				fi, err := os.Stat(event.Name)
				if err != nil {
					d.log.Error().
						Err(err).
						Str("path", event.Name).
						Msg("Failed retrieving filesystem info")
					continue
				}

				// watch new directories
				if fi.IsDir() {
					if err := filepath.Walk(event.Name, d.walkFunc); err != nil {
						d.log.Error().
							Err(err).
							Str("path", event.Name).
							Msg("Failed watching new directory")
					}

					continue
				}

			case event.Op&fsnotify.Rename == fsnotify.Rename, event.Op&fsnotify.Remove == fsnotify.Remove:
				// renamed / removed
			default:
				// ignore this event
				continue
			}

			// get path object
			p, err := d.getPathObject(event.Name)
			if err != nil {
				d.log.Error().
					Err(err).
					Str("path", event.Name).
					Msg("Failed determining path object")
				continue
			}

			// rewrite
			rewritten := p.Rewriter(event.Name)

			// filter
			if !p.Allowed(rewritten) {
				continue
			}

			// get directory where path has an extension
			if filepath.Ext(rewritten) != "" {
				// there was most likely a file extension, use the directory
				rewritten = filepath.Dir(rewritten)
			}

			// move to queue
			d.queue.inputs <- rewritten

		case err := <-d.watcher.Errors:
			d.log.Error().
				Err(err).
				Msg("Failed receiving filesystem events")
		}
	}
}

const (
	// queueDelay is how long a path waits after its last event.
	// Further events for the same path in that time restart the wait.
	queueDelay = 10 * time.Second

	// queueInterval is how often the queue looks for paths that are ready.
	queueInterval = time.Second
)

// A queue collects paths from filesystem events and sends each path
// to the processor once its delay has passed.
// Only the worker goroutine reads or writes scans.
type queue struct {
	callback autoscan.ProcessorFunc
	log      zerolog.Logger
	priority int
	delay    time.Duration
	interval time.Duration
	inputs   chan string
	scans    map[string]time.Time
}

func newQueue(cb autoscan.ProcessorFunc, log zerolog.Logger, priority int) *queue {
	q := &queue{
		callback: cb,
		log:      log,
		priority: priority,
		delay:    queueDelay,
		interval: queueInterval,
		inputs:   make(chan string),
		scans:    make(map[string]time.Time),
	}

	go q.worker()

	return q
}

func (q *queue) worker() {
	ticker := time.NewTicker(q.interval)
	defer ticker.Stop()

	for {
		select {
		case path, ok := <-q.inputs:
			if !ok {
				// channel closed
				return
			}

			q.scans[path] = time.Now().Add(q.delay)

		case <-ticker.C:
			q.process()
		}
	}
}

func (q *queue) process() {
	// move scans to processor
	for p, t := range q.scans {
		// time has not elapsed
		if time.Now().Before(t) {
			continue
		}

		// move to processor
		err := q.callback(autoscan.Scan{
			Folder:   filepath.Clean(p),
			Priority: q.priority,
			Time:     time.Now(),
		})

		if err != nil {
			q.log.Error().
				Err(err).
				Str("path", p).
				Msg("Failed moving scan to processor")
		} else {
			q.log.Info().
				Str("path", p).
				Msg("Scan moved to processor")
		}

		// remove queued scan
		delete(q.scans, p)
	}
}
