package media

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

const (
	// folderReloadInterval is how long a folder that matched no library
	// after a reload waits before it can cause another reload.
	folderReloadInterval = time.Hour

	// minReloadInterval is the shortest time between two loads of the list.
	minReloadInterval = time.Minute
)

// LibraryList holds the libraries of one media server. A user can add a
// library after the target connects, so the list reloads when a folder
// matches no library. A folder that matches no library is normal when
// autoscan sends scans to several servers, so the reloads are limited.
// Only one goroutine can use a LibraryList.
type LibraryList struct {
	log       zerolog.Logger
	load      func(context.Context) ([]Library, error)
	now       func() time.Time
	libraries []Library

	// attemptedAt is the time of the last load, and loadErr is its error.
	attemptedAt time.Time
	loadErr     error

	// unmatched holds the time of the reload that each folder caused,
	// for folders that matched no library after that reload.
	unmatched map[string]time.Time
}

// NewLibraryList loads the libraries with load and returns the list.
func NewLibraryList(ctx context.Context, log zerolog.Logger, load func(context.Context) ([]Library, error)) (*LibraryList, error) {
	l := &LibraryList{
		log:       log,
		load:      load,
		now:       time.Now,
		unmatched: make(map[string]time.Time),
	}

	if err := l.reload(ctx); err != nil {
		return nil, err
	}

	return l, nil
}

// Matching returns every library whose path contains folder. If no library
// matches, Matching reloads the list and matches again, unless the last load
// was less than minReloadInterval ago or folder caused a reload less than
// folderReloadInterval ago. It returns an error if the reload fails, and it
// returns the same error until the next reload can start.
func (l *LibraryList) Matching(ctx context.Context, folder string) ([]Library, error) {
	if libs := matching(l.libraries, folder); len(libs) > 0 {
		return libs, nil
	}

	now := l.now()
	if now.Sub(l.attemptedAt) < minReloadInterval {
		if l.loadErr != nil {
			return nil, fmt.Errorf("reload libraries: %w", l.loadErr)
		}
		return nil, nil
	}
	if reloadedAt, ok := l.unmatched[folder]; ok && now.Sub(reloadedAt) < folderReloadInterval {
		return nil, nil
	}

	if err := l.reload(ctx); err != nil {
		return nil, fmt.Errorf("reload libraries: %w", err)
	}

	libs := matching(l.libraries, folder)
	if len(libs) == 0 {
		l.unmatched[folder] = l.attemptedAt
	}

	return libs, nil
}

// reload loads the libraries and removes the expired unmatched folders.
func (l *LibraryList) reload(ctx context.Context) error {
	l.attemptedAt = l.now()
	libraries, err := l.load(ctx)
	l.loadErr = err
	if err != nil {
		return err
	}

	l.libraries = libraries
	for folder, reloadedAt := range l.unmatched {
		if l.attemptedAt.Sub(reloadedAt) >= folderReloadInterval {
			delete(l.unmatched, folder)
		}
	}

	l.log.Debug().
		Interface("libraries", libraries).
		Msg("Retrieved libraries")

	return nil
}
