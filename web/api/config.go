package api

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// TriggerInfo represents a trigger in the config response
type TriggerInfo struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// TargetInfo represents a target in the config response
type TargetInfo struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// ConfigResponse is the response for GET /api/config
type ConfigResponse struct {
	Triggers []TriggerInfo `json:"triggers"`
	Targets  []TargetInfo  `json:"targets"`
}

// getConfig handles GET /api/config
func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	triggers := h.registry.GetTriggers()
	targets := h.registry.GetTargets()

	response := ConfigResponse{
		Triggers: make([]TriggerInfo, len(triggers)),
		Targets:  make([]TargetInfo, len(targets)),
	}

	for i, t := range triggers {
		response.Triggers[i] = TriggerInfo{
			Type: t.Kind,
			Name: t.Name,
		}
	}

	for i, t := range targets {
		response.Targets[i] = TargetInfo{
			Type: t.Kind,
			Name: t.Name,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode config response")
	}
}
