package main

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/hlog"
	"github.com/rs/zerolog/log"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/processor"
	"github.com/l3uddz/autoscan/triggers/lidarr"
	"github.com/l3uddz/autoscan/triggers/manual"
	"github.com/l3uddz/autoscan/triggers/radarr"
	"github.com/l3uddz/autoscan/triggers/readarr"
	"github.com/l3uddz/autoscan/triggers/sonarr"
)

func getRouter(c config, proc *processor.Processor) chi.Router {
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

	// HTTP-Triggers
	r.Route("/triggers", func(r chi.Router) {
		// Use Basic Auth middleware if username and password are set.
		if c.Auth.Username != "" && c.Auth.Password != "" {
			r.Use(middleware.BasicAuth("Autoscan 1.x", map[string]string{c.Auth.Username: c.Auth.Password}))
		}

		// Mixed-style Manual HTTP-trigger
		r.Route("/manual", func(r chi.Router) {
			trigger, err := manual.New(c.Triggers.Manual)
			if err != nil {
				log.Fatal().Err(err).Str("trigger", "manual").Msg("Failed initialising trigger")
			}

			r.HandleFunc("/", trigger(proc.Add).ServeHTTP)
		})

		// OLD-style HTTP-triggers. Can be converted to the /{trigger}/{id} format in a 2.0 release.
		addTriggers(r, proc, c.Triggers.Lidarr, func(t lidarr.Config) string { return t.Name }, lidarr.New)
		addTriggers(r, proc, c.Triggers.Radarr, func(t radarr.Config) string { return t.Name }, radarr.New)
		addTriggers(r, proc, c.Triggers.Readarr, func(t readarr.Config) string { return t.Name }, readarr.New)
		addTriggers(r, proc, c.Triggers.Sonarr, func(t sonarr.Config) string { return t.Name }, sonarr.New)
	})

	return r
}

// addTriggers registers a POST route for each trigger configuration.
// The route is the trigger name. Autoscan stops if a trigger fails.
func addTriggers[C any](r chi.Router, proc *processor.Processor, configs []C, name func(C) string, create func(C) (autoscan.HTTPTrigger, error)) {
	for _, c := range configs {
		trigger, err := create(c)
		if err != nil {
			log.Fatal().Err(err).Str("trigger", name(c)).Msg("Failed initialising trigger")
		}

		r.Post("/"+name(c), trigger(proc.Add).ServeHTTP)
	}
}

// Other Handlers
func healthHandler(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
}
