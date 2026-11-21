package sonarr

import (
	"net/http"
	"strings"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New returns the HTTP handler for Sonarr webhooks. It sends the scans to
// callback.
func New(c Config, callback autoscan.ProcessorFunc) (http.Handler, error) {
	return webhook.New(c, parse, callback)
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

		event.Folders = []string{webhook.FileFolder(e.Series.Path, e.File.RelativePath)}

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
			if folder := webhook.ParentFolder(renamed.PreviousPath); folder != "" {
				event.Folders = append(event.Folders, folder)
			}
			event.Folders = append(event.Folders, webhook.FileFolder(e.Series.Path, renamed.RelativePath))
		}
	}

	// Other events, such as Test or Grab, have no folders to scan.
	return event, nil
}
