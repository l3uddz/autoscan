package readarr

import (
	"net/http"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New creates an autoscan-compatible HTTP Trigger for Readarr webhooks.
func New(c Config) (autoscan.HTTPTrigger, error) {
	return webhook.New(c, parse, now)
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
