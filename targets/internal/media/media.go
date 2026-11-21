// Package media holds code that the Plex, Emby, and Jellyfin targets share.
package media

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

// A Library is one folder of a media server library.
// ID is the Plex section ID. Emby and Jellyfin do not use it.
type Library struct {
	ID   int
	Name string
	Path string
}

// NewLibrary returns a Library whose path ends with a separator, so that
// /mnt/tv does not match the folder /mnt/tv2/Show. A Windows path that
// separates its folders with backslashes, such as C:\TV, gets a backslash.
func NewLibrary(id int, name string, path string) Library {
	separator := "/"
	if strings.Contains(path, `\`) && !strings.Contains(path, "/") {
		separator = `\`
	}
	if path != "" && !strings.HasSuffix(path, separator) {
		path += separator
	}

	return Library{ID: id, Name: name, Path: path}
}

// matching returns every library whose path contains folder, or whose
// path is folder.
func matching(libraries []Library, folder string) []Library {
	matches := make([]Library, 0)
	for _, lib := range libraries {
		root := lib.Path[:max(0, len(lib.Path)-1)]
		if strings.HasPrefix(folder, lib.Path) || folder == root {
			matches = append(matches, lib)
		}
	}

	return matches
}

// Names returns the library names, for log fields.
func Names(libraries []Library) []string {
	names := make([]string, 0, len(libraries))
	for _, lib := range libraries {
		names = append(names, lib.Name)
	}

	return names
}

// SendUpdated reports a changed path to Emby or Jellyfin with
// POST Library/Media/Updated below prefix, the path elements of the API
// root. The request names no library.
func SendUpdated(ctx context.Context, c *httpapi.Client, prefix []string, path string, updateType string) error {
	type update struct {
		Path       string `json:"path"`
		UpdateType string `json:"updateType"`
	}

	payload := struct {
		Updates []update `json:"Updates"`
	}{
		Updates: []update{{Path: path, UpdateType: updateType}},
	}

	return c.PostJSON(ctx, payload, slices.Concat(prefix, []string{"Library", "Media", "Updated"})...)
}

// A PathAPI is a media server client whose scan request takes only a path.
type PathAPI interface {
	Available(ctx context.Context) error
	Scan(ctx context.Context, path string) error
}

// PathTarget is the autoscan.Target for Emby and Jellyfin. Their scan request
// takes a path and no library, and the server finds the items for that path.
// So PathTarget sends one request for each scan folder, even if the folder is
// in more than one library.
type PathTarget struct {
	log       zerolog.Logger
	rewrite   autoscan.Rewriter
	libraries *LibraryList
	api       PathAPI
}

// NewPathTarget returns a PathTarget.
func NewPathTarget(log zerolog.Logger, rewrite autoscan.Rewriter, libraries *LibraryList, api PathAPI) *PathTarget {
	return &PathTarget{log: log, rewrite: rewrite, libraries: libraries, api: api}
}

func (t *PathTarget) Available(ctx context.Context) error {
	return t.api.Available(ctx)
}

// Scan sends the rewritten scan folder to the media server. If the folder
// is in no library, Scan sends nothing and returns autoscan.ErrNoLibrary.
func (t *PathTarget) Scan(ctx context.Context, scan autoscan.Scan) error {
	scanFolder := t.rewrite(scan.Folder)

	libs, err := t.libraries.Matching(ctx, scanFolder)
	if err != nil {
		return err
	}
	if len(libs) == 0 {
		return fmt.Errorf("%s: %w", scanFolder, autoscan.ErrNoLibrary)
	}

	l := t.log.With().
		Str("path", scanFolder).
		Strs("libraries", Names(libs)).
		Logger()

	l.Trace().Msg("Sending scan request")

	if err := t.api.Scan(ctx, scanFolder); err != nil {
		return err
	}

	l.Info().Msg("Scan moved to target")
	return nil
}
