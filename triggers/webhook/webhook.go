// Package webhook holds the HTTP handler that the webhook triggers share.
// Each trigger supplies a Parser that turns its request into an Event.
// The handler rewrites the folders, removes duplicates, and sends the scans
// to the processor.
package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/l3uddz/autoscan"
)

// Config is the configuration of a Lidarr, Radarr, Readarr, or Sonarr trigger.
type Config struct {
	Name      string             `yaml:"name"`
	Priority  int                `yaml:"priority"`
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Verbosity string             `yaml:"verbosity"`
}

// ErrMissingFields means that an event lacks a field that the trigger needs.
var ErrMissingFields = errors.New("required fields are missing")

// An Event is what a trigger reads from one webhook request.
type Event struct {
	// Type is the event type, for logs. The -arrs send the type "Test"
	// when you test the webhook.
	Type string

	// Folders are the folders to scan, before the rewrite.
	// A Test event or an event that the trigger does not handle has none.
	Folders []string
}

// A Parser reads one webhook request. If it returns an error, the handler
// responds with 400 Bad Request.
type Parser func(r *http.Request) (Event, error)

// New returns an HTTPTrigger for parse. now returns the scan time.
func New(priority int, rewrites []autoscan.Rewrite, parse Parser, now func() time.Time) (autoscan.HTTPTrigger, error) {
	rewriter, err := autoscan.NewRewriter(rewrites)
	if err != nil {
		return nil, err
	}

	trigger := func(callback autoscan.ProcessorFunc) http.Handler {
		return handler{
			priority: priority,
			rewrite:  rewriter,
			parse:    parse,
			now:      now,
			callback: callback,
		}
	}

	return trigger, nil
}

type handler struct {
	priority int
	rewrite  autoscan.Rewriter
	parse    Parser
	now      func() time.Time
	callback autoscan.ProcessorFunc
}

func (h handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rlog := hlog.FromRequest(r)

	event, err := h.parse(r)
	if err != nil {
		rlog.Error().Err(err).Msg("Invalid request")
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	if strings.EqualFold(event.Type, "Test") {
		rlog.Info().Msg("Received test event")
		rw.WriteHeader(http.StatusOK)
		return
	}

	scans := h.scans(event.Folders)
	if len(scans) == 0 {
		rlog.Debug().Str("event", event.Type).Msg("No folders to scan")
		rw.WriteHeader(http.StatusOK)
		return
	}

	if err := h.callback(scans...); err != nil {
		rlog.Error().Err(err).Msg("Processor could not process scans")
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, scan := range scans {
		rlog.Info().
			Str("path", scan.Folder).
			Str("event", event.Type).
			Msg("Scan moved to processor")
	}

	rw.WriteHeader(http.StatusOK)
}

// scans rewrites each folder and returns one scan for each unique result.
// It keeps the order of the folders and skips empty folders.
func (h handler) scans(folders []string) []autoscan.Scan {
	seen := make(map[string]bool, len(folders))
	scans := make([]autoscan.Scan, 0, len(folders))

	for _, folder := range folders {
		if folder == "" {
			continue
		}

		folder = h.rewrite(folder)
		if seen[folder] {
			continue
		}

		seen[folder] = true
		scans = append(scans, autoscan.Scan{
			Folder:   folder,
			Priority: h.priority,
			Time:     h.now(),
		})
	}

	return scans
}

// DecodeJSON decodes the JSON body of r into v and logs v at trace level.
func DecodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding request: %w", err)
	}

	hlog.FromRequest(r).Trace().Interface("event", v).Msg("Received JSON body")
	return nil
}

// A File is one file in a Lidarr or Readarr event.
type File struct {
	Path string
}

// FileEvent returns the Event for Lidarr and Readarr. Both send a Download
// event with a list of files. A Download event without files is an error.
// Any other event type has no folders to scan.
func FileEvent(eventType string, files []File) (Event, error) {
	event := Event{Type: eventType}
	if !strings.EqualFold(eventType, "Download") {
		return event, nil
	}

	if len(files) == 0 {
		return event, ErrMissingFields
	}

	for _, f := range files {
		event.Folders = append(event.Folders, path.Dir(f.Path))
	}

	return event, nil
}
