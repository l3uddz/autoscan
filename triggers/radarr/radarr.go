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

// New creates an autoscan-compatible HTTP Trigger for Radarr webhooks.
func New(c Config) (autoscan.HTTPTrigger, error) {
	return webhook.New(c, parse, now)
}

type radarrEvent struct {
	Type string `json:"eventType"`

	File struct {
		RelativePath string
	} `json:"movieFile"`

	Movie struct {
		FolderPath string
	} `json:"movie"`
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

	case strings.EqualFold(e.Type, "MovieDelete"), strings.EqualFold(e.Type, "Rename"):
		if e.Movie.FolderPath == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{e.Movie.FolderPath}
	}

	// Other events, such as Test or Grab, have no folders to scan.
	return event, nil
}

var now = time.Now
