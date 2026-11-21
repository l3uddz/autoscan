// Package manual is the webhook trigger that scans the folders that a user
// enters on its web page or sends in a request.
package manual

import (
	_ "embed"
	"errors"
	"net/http"

	"github.com/rs/zerolog/hlog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/triggers/webhook"
)

type Config struct {
	Rewrite   []autoscan.Rewrite `yaml:"rewrite"`
	Priority  int                `yaml:"priority"`
	Verbosity string             `yaml:"verbosity"`
}

var (
	//go:embed "template.html"
	template []byte
)

// New returns the HTTP handler for manual webhooks. GET returns the web page
// and HEAD returns 200. Any other method sends the folders in the dir query
// parameters to callback.
func New(c Config, callback autoscan.ProcessorFunc) (http.Handler, error) {
	scanHandler, err := webhook.New(webhook.Config{
		Priority:  c.Priority,
		Rewrite:   c.Rewrite,
		Verbosity: c.Verbosity,
	}, parse, callback)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = rw.Write(template)
		case http.MethodHead:
			rw.WriteHeader(http.StatusOK)
		default:
			scanHandler.ServeHTTP(rw, r)
		}
	}), nil
}

var (
	errNoDirectory    = errors.New("manual webhook should receive at least one directory")
	errEmptyDirectory = errors.New("manual webhook received an empty directory")
)

func parse(r *http.Request) (webhook.Event, error) {
	directories := r.URL.Query()["dir"]
	if len(directories) == 0 {
		return webhook.Event{}, errNoDirectory
	}

	hlog.FromRequest(r).Trace().Strs("dirs", directories).Msg("Received directories")

	event := webhook.Event{Type: "Manual"}
	for _, dir := range directories {
		// CleanFolder turns "" into ".", so check before it.
		if dir == "" {
			return webhook.Event{}, errEmptyDirectory
		}
		event.Folders = append(event.Folders, webhook.CleanFolder(dir))
	}

	return event, nil
}
