package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/l3uddz/autoscan/processor"
)

// Handler holds dependencies for API endpoints
type Handler struct {
	proc     *processor.Processor
	registry *RewriteRegistry
	logHub   *LogHub
}

// New creates a new API handler
func New(proc *processor.Processor, registry *RewriteRegistry, hub *LogHub) *Handler {
	return &Handler{
		proc:     proc,
		registry: registry,
		logHub:   hub,
	}
}

// Routes returns the API router
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Scan endpoints
	r.Get("/scans", h.listScans)
	r.Post("/scans", h.addScan)

	// Config endpoint (for UI dropdowns)
	r.Get("/config", h.getConfig)

	// Rewrite testing
	r.Post("/rewrite", h.testRewrite)

	// WebSocket for live logs
	r.Get("/logs", h.logsWebSocket)

	return r
}
