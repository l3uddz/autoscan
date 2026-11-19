package readarr

import (
	"net/http"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New returns the HTTP handler for Readarr webhooks. It sends the scans to
// callback.
func New(c Config, callback autoscan.ProcessorFunc) (http.Handler, error) {
	return webhook.New(c, parse, now, callback)
}

type readarrEvent struct {
	Type  string         `json:"eventType"`
	Files []webhook.File `json:"bookFiles"`
}

func parse(r *http.Request) (webhook.Event, error) {
	var event readarrEvent
	if err := webhook.DecodeJSON(r, &event); err != nil {
		return webhook.Event{}, err
	}

	return webhook.FileEvent(event.Type, event.Files)
}

var now = time.Now
