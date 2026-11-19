package radarr

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New returns the HTTP handler for Radarr webhooks. It sends the scans to
// callback.
func New(c Config, callback autoscan.ProcessorFunc) (http.Handler, error) {
	return webhook.New(c, parse, now, callback)
}

type radarrEvent struct {
	Type string `json:"eventType"`

	File struct {
		RelativePath string
	} `json:"movieFile"`

	Movie struct {
		FolderPath string
	} `json:"movie"`

	RenamedFiles []struct {
		PreviousPath string
	} `json:"renamedMovieFiles"`
}

func parse(r *http.Request) (webhook.Event, error) {
	var e radarrEvent
	if err := webhook.DecodeJSON(r, &e); err != nil {
		return webhook.Event{}, err
	}

	event := webhook.Event{Type: e.Type}

	switch {
	case strings.EqualFold(e.Type, "Download"), strings.EqualFold(e.Type, "MovieFileDelete"):
		if e.File.RelativePath == "" || e.Movie.FolderPath == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{path.Dir(path.Join(e.Movie.FolderPath, e.File.RelativePath))}

	case strings.EqualFold(e.Type, "MovieDelete"):
		if e.Movie.FolderPath == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{e.Movie.FolderPath}

	// Scan the movie folder and the old folder of each renamed file.
	// Older Radarr releases send no renamed files. The handler removes
	// duplicate folders.
	case strings.EqualFold(e.Type, "Rename"):
		if e.Movie.FolderPath == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{e.Movie.FolderPath}
		for _, renamed := range e.RenamedFiles {
			if folder := parentFolder(renamed.PreviousPath); folder != "" {
				event.Folders = append(event.Folders, folder)
			}
		}
	}

	// Other events, such as Test or Grab, have no folders to scan.
	return event, nil
}

// parentFolder returns the folder of a file path that Radarr sends. Radarr
// on Windows separates folders with backslashes, which path.Dir ignores.
// The folder keeps the separators of the input, so that rewrite rules match
// it in the same form as movie.folderPath. parentFolder returns "" for a
// path without a folder.
func parentFolder(file string) string {
	i := strings.LastIndexAny(file, `/\`)
	switch {
	case i < 0:
		return ""
	case i == 0:
		return file[:1]
	default:
		return file[:i]
	}
}

var now = time.Now
