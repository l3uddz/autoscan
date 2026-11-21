// Package media holds code that the Plex, Emby, and Jellyfin targets share.
package media

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

// Config is the configuration of a Plex, Emby, or Jellyfin target.
type Config struct {
	URL       string             `yaml:"url"`
	Token     string             `yaml:"token"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

// BaseURL returns the configured server URL.
func (c Config) BaseURL() string {
	return c.URL
}

// Validate returns an error for each value of c that cannot work. An empty
// token is allowed, because Plex can allow a network without one.
func (c Config) Validate() error {
	_, rewriteErr := autoscan.NewRewriter(c.Rewrite)
	return errors.Join(httpapi.CheckBaseURL(c.URL), httpapi.CheckToken(c.Token), rewriteErr)
}

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
// path is folder. Empty paths and empty folders match nothing.
func matching(libraries []Library, folder string) []Library {
	if folder == "" {
		return nil
	}

	matches := make([]Library, 0)
	for _, lib := range libraries {
		if lib.Path == "" {
			continue
		}
		root := lib.Path[:len(lib.Path)-1]
		if strings.HasPrefix(folder, lib.Path) || (root != "" && folder == root) {
			matches = append(matches, lib)
		}
	}

	return matches
}

// Names returns the unique library names in order, for log fields.
func Names(libraries []Library) []string {
	seen := make(map[string]bool, len(libraries))
	names := make([]string, 0, len(libraries))
	for _, lib := range libraries {
		if seen[lib.Name] {
			continue
		}
		seen[lib.Name] = true
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

// An API is the client of one media server.
type API interface {
	Available(ctx context.Context) error
	Libraries(ctx context.Context) ([]Library, error)

	// Scan asks the server to scan folder, which is in libs. Plex scans each
	// library. Emby and Jellyfin find the libraries from the path, so they
	// send one request even for a folder in several libraries.
	Scan(ctx context.Context, folder string, libs []Library) error
}

// Logger returns the logger of a target of kind for c. The url field omits
// the password and the secretParams query parameters.
func Logger(kind string, c Config, secretParams ...string) zerolog.Logger {
	return autoscan.GetLogger(c.Verbosity).With().
		Str("target", kind).
		Str("url", httpapi.RedactURL(c.URL, secretParams...)).
		Logger()
}

// Target is the autoscan.Target for Plex, Emby, and Jellyfin.
type Target struct {
	log       zerolog.Logger
	rewrite   autoscan.Rewriter
	libraries *LibraryList
	api       API
}

// NewTarget reads the libraries through api and returns the target for c.
func NewTarget(ctx context.Context, log zerolog.Logger, c Config, api API) (*Target, error) {
	rewrite, err := autoscan.NewRewriter(c.Rewrite)
	if err != nil {
		return nil, err
	}

	libraries, err := NewLibraryList(ctx, log, api.Libraries)
	if err != nil {
		return nil, err
	}

	return &Target{log: log, rewrite: rewrite, libraries: libraries, api: api}, nil
}

func (t *Target) Available(ctx context.Context) error {
	return t.api.Available(ctx)
}

// Scan sends the rewritten scan folder to the media server. If the folder
// is in no library, Scan sends nothing and returns autoscan.ErrNoLibrary.
func (t *Target) Scan(ctx context.Context, scan autoscan.Scan) error {
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

	if err := t.api.Scan(ctx, scanFolder, libs); err != nil {
		return err
	}

	l.Info().Msg("Scan moved to target")
	return nil
}
