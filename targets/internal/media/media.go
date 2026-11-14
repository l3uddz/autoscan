// Package media holds code that the Plex, Emby, and Jellyfin targets share.
package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

// A Library is one folder of a media server library.
// ID is the Plex section ID. Emby and Jellyfin do not use it.
type Library struct {
	ID   int
	Name string
	Path string
}

// NewLibrary returns a Library whose path ends with a slash, so that
// /mnt/tv does not match the folder /mnt/tv2/Show.
func NewLibrary(id int, name string, path string) Library {
	if path != "" && !strings.HasSuffix(path, "/") {
		path += "/"
	}

	return Library{ID: id, Name: name, Path: path}
}

// Matching returns every library whose path contains folder.
func Matching(libraries []Library, folder string) []Library {
	matches := make([]Library, 0)
	for _, lib := range libraries {
		if strings.HasPrefix(folder, lib.Path) {
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

// NewUpdatedRequest creates the Emby and Jellyfin request that reports a
// changed path: POST /Library/Media/Updated. The request names no library.
func NewUpdatedRequest(baseURL string, path string, updateType string) (*http.Request, error) {
	type update struct {
		Path       string `json:"path"`
		UpdateType string `json:"updateType"`
	}

	payload := struct {
		Updates []update `json:"Updates"`
	}{
		Updates: []update{{Path: path, UpdateType: updateType}},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed encoding scan request payload: %v: %w", err, autoscan.ErrFatal)
	}

	req, err := http.NewRequest("POST", autoscan.JoinURL(baseURL, "Library", "Media", "Updated"), bytes.NewBuffer(b))
	if err != nil {
		return nil, fmt.Errorf("failed creating scan request: %v: %w", err, autoscan.ErrFatal)
	}

	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// A PathAPI is a media server client whose scan request takes only a path.
type PathAPI interface {
	Available() error
	Scan(path string) error
}

// PathTarget is the autoscan.Target for Emby and Jellyfin. Their scan request
// takes a path and no library, and the server finds the items for that path.
// So PathTarget sends one request for each scan folder, even if the folder is
// in more than one library.
type PathTarget struct {
	log       zerolog.Logger
	rewrite   autoscan.Rewriter
	libraries []Library
	api       PathAPI
}

// NewPathTarget returns a PathTarget.
func NewPathTarget(log zerolog.Logger, rewrite autoscan.Rewriter, libraries []Library, api PathAPI) *PathTarget {
	return &PathTarget{log: log, rewrite: rewrite, libraries: libraries, api: api}
}

func (t *PathTarget) Available() error {
	return t.api.Available()
}

// Scan sends the rewritten scan folder to the media server. If the folder
// is in no library, Scan logs a warning and sends nothing.
func (t *PathTarget) Scan(scan autoscan.Scan) error {
	scanFolder := t.rewrite(scan.Folder)

	libs := Matching(t.libraries, scanFolder)
	if len(libs) == 0 {
		t.log.Warn().
			Str("path", scanFolder).
			Msg("No target libraries found")

		return nil
	}

	l := t.log.With().
		Str("path", scanFolder).
		Strs("libraries", Names(libs)).
		Logger()

	l.Trace().Msg("Sending scan request")

	if err := t.api.Scan(scanFolder); err != nil {
		return err
	}

	l.Info().Msg("Scan moved to target")
	return nil
}
