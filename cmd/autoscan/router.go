package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/hlog"
	"github.com/rs/zerolog/log"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/processor"
	"github.com/l3uddz/autoscan/triggers/inotify"
	"github.com/l3uddz/autoscan/triggers/lidarr"
	"github.com/l3uddz/autoscan/triggers/manual"
	"github.com/l3uddz/autoscan/triggers/radarr"
	"github.com/l3uddz/autoscan/triggers/readarr"
	"github.com/l3uddz/autoscan/triggers/sonarr"
)

// getRouter returns the HTTP routes. GET /status also reports watchers,
// the inotify triggers. It returns an error if a trigger configuration is
// not valid.
func getRouter(c config, proc *processor.Processor, watchers []*inotify.Trigger) (chi.Router, error) {
	manualTrigger, err := manual.New(c.Triggers.Manual, proc.Add)
	if err != nil {
		return nil, fmt.Errorf("trigger manual: %w", err)
	}

	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Recoverer)

	// Logging-related middleware
	r.Use(hlog.NewHandler(log.Logger))
	r.Use(hlog.RequestIDHandler("id", "request-id"))
	r.Use(hlog.URLHandler("url"))
	r.Use(hlog.MethodHandler("method"))
	r.Use(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(r).Debug().
			Int("status", status).
			Dur("duration", duration).
			Msg("Request processed")
	}))

	// Health check
	r.Get("/health", healthHandler)

	protected := r.Group(func(r chi.Router) {
		if c.Auth.Username != "" && c.Auth.Password != "" {
			r.Use(middleware.BasicAuth("Autoscan 1.x", map[string]string{c.Auth.Username: c.Auth.Password}))
		}
	})
	protected.Get("/status", statusHandler(proc, watchers))

	// HTTP-Triggers
	protected.Route("/triggers", func(r chi.Router) {
		// Mixed-style Manual HTTP-trigger
		r.Route("/manual", func(r chi.Router) {
			r.Handle("/", manualTrigger)
		})

		// OLD-style HTTP-triggers. Can be converted to the /{trigger}/{id} format in a 2.0 release.
		err = errors.Join(
			addTriggers(r, proc.Add, c.Triggers.Lidarr, func(t lidarr.Config) string { return t.Name }, lidarr.New),
			addTriggers(r, proc.Add, c.Triggers.Radarr, func(t radarr.Config) string { return t.Name }, radarr.New),
			addTriggers(r, proc.Add, c.Triggers.Readarr, func(t readarr.Config) string { return t.Name }, readarr.New),
			addTriggers(r, proc.Add, c.Triggers.Sonarr, func(t sonarr.Config) string { return t.Name }, sonarr.New),
		)
	})
	if err != nil {
		return nil, err
	}

	return r, nil
}

// addTriggers registers a POST route for each trigger configuration.
// The route is the trigger name. It stops at the first trigger that fails.
func addTriggers[C any](r chi.Router, callback autoscan.ProcessorFunc, configs []C, name func(C) string, create func(C, autoscan.ProcessorFunc) (http.Handler, error)) error {
	for _, c := range configs {
		handler, err := create(c, callback)
		if err != nil {
			return fmt.Errorf("trigger %s: %w", name(c), err)
		}

		r.Post("/"+name(c), handler.ServeHTTP)
	}

	return nil
}

// Other Handlers
func healthHandler(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
}

// statusReport is the GET /status response. The inotify state does not change
// can_deliver or the status code, because it does not affect delivery.
type statusReport struct {
	processor.Status
	Inotify []inotifyStatus `json:"inotify"`
}

type inotifyStatus struct {
	ID string `json:"id"`
	inotify.Status
}

func statusHandler(proc *processor.Processor, watchers []*inotify.Trigger) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		status := statusReport{Status: proc.Status(ctx), Inotify: make([]inotifyStatus, 0, len(watchers))}
		for i, watcher := range watchers {
			status.Inotify = append(status.Inotify, inotifyStatus{
				ID:     fmt.Sprintf("inotify-%d", i+1),
				Status: watcher.Status(),
			})
		}

		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		if !status.CanDeliver {
			rw.WriteHeader(http.StatusServiceUnavailable)
		}
		if err := json.NewEncoder(rw).Encode(status); err != nil {
			hlog.FromRequest(r).Debug().Err(err).Msg("Failed writing processor status")
		}
	}
}
