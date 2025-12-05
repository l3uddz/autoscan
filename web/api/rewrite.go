package api

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autoscan"
)

// RewriteEntry stores both rules and the compiled rewriter
type RewriteEntry struct {
	Type    string             // "trigger" or "target"
	Kind    string             // "sonarr", "plex", etc.
	Name    string             // instance name
	Rules   []autoscan.Rewrite // original rules for display
	Rewrite autoscan.Rewriter  // compiled function
}

// RewriteRegistry holds all trigger and target rewrite configurations
type RewriteRegistry struct {
	entries []RewriteEntry
}

// NewRewriteRegistry creates a new empty registry
func NewRewriteRegistry() *RewriteRegistry {
	return &RewriteRegistry{
		entries: make([]RewriteEntry, 0),
	}
}

// AddTrigger adds a trigger's rewrite configuration to the registry
func (r *RewriteRegistry) AddTrigger(kind, name string, rules []autoscan.Rewrite, rewriter autoscan.Rewriter) {
	r.entries = append(r.entries, RewriteEntry{
		Type:    "trigger",
		Kind:    kind,
		Name:    name,
		Rules:   rules,
		Rewrite: rewriter,
	})
}

// AddTarget adds a target's rewrite configuration to the registry
func (r *RewriteRegistry) AddTarget(kind, name string, rules []autoscan.Rewrite, rewriter autoscan.Rewriter) {
	r.entries = append(r.entries, RewriteEntry{
		Type:    "target",
		Kind:    kind,
		Name:    name,
		Rules:   rules,
		Rewrite: rewriter,
	})
}

// GetTriggers returns all trigger entries
func (r *RewriteRegistry) GetTriggers() []RewriteEntry {
	triggers := make([]RewriteEntry, 0)
	for _, e := range r.entries {
		if e.Type == "trigger" {
			triggers = append(triggers, e)
		}
	}
	return triggers
}

// GetTargets returns all target entries
func (r *RewriteRegistry) GetTargets() []RewriteEntry {
	targets := make([]RewriteEntry, 0)
	for _, e := range r.entries {
		if e.Type == "target" {
			targets = append(targets, e)
		}
	}
	return targets
}

// Find looks up a specific entry by type, kind, and name
func (r *RewriteRegistry) Find(typ, kind, name string) *RewriteEntry {
	for i := range r.entries {
		if r.entries[i].Type == typ && r.entries[i].Kind == kind && r.entries[i].Name == name {
			return &r.entries[i]
		}
	}
	return nil
}

// TestRewriteRequest is the request body for POST /api/rewrite
type TestRewriteRequest struct {
	Path        string `json:"path"`
	TriggerKind string `json:"trigger_kind"`
	TriggerName string `json:"trigger_name"`
	TargetKind  string `json:"target_kind"`
	TargetName  string `json:"target_name"`
}

// RewriteRuleInfo represents a rewrite rule in the response
type RewriteRuleInfo struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TestRewriteResponse is the response for POST /api/rewrite
type TestRewriteResponse struct {
	Original       string            `json:"original"`
	AfterTrigger   string            `json:"after_trigger"`
	AfterTarget    string            `json:"after_target"`
	TriggerMatched bool              `json:"trigger_matched"`
	TargetMatched  bool              `json:"target_matched"`
	TriggerRules   []RewriteRuleInfo `json:"trigger_rules,omitempty"`
	TargetRules    []RewriteRuleInfo `json:"target_rules,omitempty"`
}

// testRewrite handles POST /api/rewrite
func (h *Handler) testRewrite(w http.ResponseWriter, r *http.Request) {
	var req TestRewriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	response := TestRewriteResponse{
		Original: req.Path,
	}

	// Apply trigger rewrite if specified
	afterTrigger := req.Path
	if req.TriggerKind != "" && req.TriggerName != "" {
		trigger := h.registry.Find("trigger", req.TriggerKind, req.TriggerName)
		if trigger != nil && trigger.Rewrite != nil {
			afterTrigger = trigger.Rewrite(req.Path)
			response.TriggerMatched = afterTrigger != req.Path
			response.TriggerRules = make([]RewriteRuleInfo, len(trigger.Rules))
			for i, rule := range trigger.Rules {
				response.TriggerRules[i] = RewriteRuleInfo{
					From: rule.From,
					To:   rule.To,
				}
			}
		}
	}
	response.AfterTrigger = afterTrigger

	// Apply target rewrite if specified
	afterTarget := afterTrigger
	if req.TargetKind != "" && req.TargetName != "" {
		target := h.registry.Find("target", req.TargetKind, req.TargetName)
		if target != nil && target.Rewrite != nil {
			afterTarget = target.Rewrite(afterTrigger)
			response.TargetMatched = afterTarget != afterTrigger
			response.TargetRules = make([]RewriteRuleInfo, len(target.Rules))
			for i, rule := range target.Rules {
				response.TargetRules[i] = RewriteRuleInfo{
					From: rule.From,
					To:   rule.To,
				}
			}
		}
	}
	response.AfterTarget = afterTarget

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode rewrite response")
	}
}
