// Package radarr is the webhook trigger for Radarr.
package radarr

import (
	"net/http"
	"strings"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New returns the HTTP handler for Radarr webhooks. It sends the scans to
// callback.
func New(c Config, callback autoscan.ProcessorFunc) (http.Handler, error) {
	return webhook.New(c, parse, callback)
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

		event.Folders = []string{webhook.FileFolder(e.Movie.FolderPath, e.File.RelativePath)}

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
			if folder := webhook.ParentFolder(renamed.PreviousPath); folder != "" {
				event.Folders = append(event.Folders, folder)
			}
		}
	}

	// Other events, such as Test or Grab, have no folders to scan.
	return event, nil
}
