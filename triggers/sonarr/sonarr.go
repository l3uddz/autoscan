package sonarr

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New creates an autoscan-compatible HTTP Trigger for Sonarr webhooks.
func New(c Config) (autoscan.HTTPTrigger, error) {
	return webhook.New(c, parse, now)
}

type sonarrEvent struct {
	Type string `json:"eventType"`

	File struct {
		RelativePath string
	} `json:"episodeFile"`

	Series struct {
		Path string
	} `json:"series"`

	RenamedFiles []struct {
		// use PreviousPath as the Series.Path might have changed.
		PreviousPath string
		RelativePath string
	} `json:"renamedEpisodeFiles"`
}

func parse(r *http.Request) (webhook.Event, error) {
	var e sonarrEvent
	if err := webhook.DecodeJSON(r, &e); err != nil {
		return webhook.Event{}, err
	}

	event := webhook.Event{Type: e.Type}

	switch {
	// a Download event is either an upgrade or a new file.
	// the EpisodeFileDelete event shares the same request format as Download.
	case strings.EqualFold(e.Type, "Download"), strings.EqualFold(e.Type, "EpisodeFileDelete"):
		if e.File.RelativePath == "" || e.Series.Path == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{path.Dir(path.Join(e.Series.Path, e.File.RelativePath))}

	// An entire show has been deleted
	case strings.EqualFold(e.Type, "SeriesDelete"):
		if e.Series.Path == "" {
			return event, webhook.ErrMissingFields
		}

		event.Folders = []string{e.Series.Path}

	// Scan the old and the new folder of each renamed file.
	// The handler removes duplicate folders.
	case strings.EqualFold(e.Type, "Rename"):
		if e.Series.Path == "" {
			return event, webhook.ErrMissingFields
		}

		for _, renamed := range e.RenamedFiles {
			event.Folders = append(event.Folders,
				path.Dir(renamed.PreviousPath),
				path.Dir(path.Join(e.Series.Path, renamed.RelativePath)),
			)
		}
	}

	// Other events, such as Test or Grab, have no folders to scan.
	return event, nil
}

var now = time.Now
