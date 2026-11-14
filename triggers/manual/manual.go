package manual

import (
	_ "embed"
	"errors"
	"net/http"
	"path"
	"time"

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

// New creates an autoscan-compatible HTTP Trigger for manual webhooks.
// GET returns the web page and HEAD returns 200. Any other method scans
// the folders in the dir query parameters.
func New(c Config) (autoscan.HTTPTrigger, error) {
	scanTrigger, err := webhook.New(webhook.Config{
		Priority:  c.Priority,
		Rewrite:   c.Rewrite,
		Verbosity: c.Verbosity,
	}, parse, now)
	if err != nil {
		return nil, err
	}

	trigger := func(callback autoscan.ProcessorFunc) http.Handler {
		scanHandler := scanTrigger(callback)

		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				rw.Header().Set("Content-Type", "text/html")
				_, _ = rw.Write(template)
			case http.MethodHead:
				rw.WriteHeader(http.StatusOK)
			default:
				scanHandler.ServeHTTP(rw, r)
			}
		})
	}

	return trigger, nil
}

var errNoDirectory = errors.New("manual webhook should receive at least one directory")

func parse(r *http.Request) (webhook.Event, error) {
	directories := r.URL.Query()["dir"]
	if len(directories) == 0 {
		return webhook.Event{}, errNoDirectory
	}

	hlog.FromRequest(r).Trace().Strs("dirs", directories).Msg("Received directories")

	event := webhook.Event{Type: "Manual"}
	for _, dir := range directories {
		event.Folders = append(event.Folders, path.Clean(dir))
	}

	return event, nil
}

var now = time.Now
