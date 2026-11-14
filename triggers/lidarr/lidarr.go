package lidarr

import (
	"net/http"
	"time"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config = webhook.Config

// New creates an autoscan-compatible HTTP Trigger for Lidarr webhooks.
func New(c Config) (autoscan.HTTPTrigger, error) {
	return webhook.New(c, parse, now)
}

type lidarrEvent struct {
	Type  string         `json:"eventType"`
	Files []webhook.File `json:"trackFiles"`
}

func parse(r *http.Request) (webhook.Event, error) {
	var event lidarrEvent
	if err := webhook.DecodeJSON(r, &event); err != nil {
		return webhook.Event{}, err
	}

	return webhook.FileEvent(event.Type, event.Files)
}

var now = time.Now
