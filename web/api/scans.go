package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autoscan"
)

// ScanResponse represents a scan in API responses
type ScanResponse struct {
	Folder   string    `json:"folder"`
	Priority int       `json:"priority"`
	Time     time.Time `json:"time"`
}

// ScansListResponse is the response for GET /api/scans
type ScansListResponse struct {
	Scans []ScanResponse `json:"scans"`
	Total int            `json:"total"`
}

// AddScanRequest is the request body for POST /api/scans
type AddScanRequest struct {
	Folder   string `json:"folder"`
	Priority int    `json:"priority"`
}

// listScans handles GET /api/scans
func (h *Handler) listScans(w http.ResponseWriter, r *http.Request) {
	scans, err := h.proc.GetAllScans()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get scans")
		http.Error(w, "failed to retrieve scans", http.StatusInternalServerError)
		return
	}

	response := ScansListResponse{
		Scans: make([]ScanResponse, len(scans)),
		Total: len(scans),
	}

	for i, s := range scans {
		response.Scans[i] = ScanResponse{
			Folder:   s.Folder,
			Priority: s.Priority,
			Time:     s.Time,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode scans response")
	}
}

// addScan handles POST /api/scans
func (h *Handler) addScan(w http.ResponseWriter, r *http.Request) {
	var req AddScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Folder == "" {
		http.Error(w, "folder is required", http.StatusBadRequest)
		return
	}

	scan := autoscan.Scan{
		Folder:   req.Folder,
		Priority: req.Priority,
		Time:     time.Now(),
	}

	if err := h.proc.Add(scan); err != nil {
		log.Error().Err(err).Str("folder", req.Folder).Msg("Failed to add scan")
		http.Error(w, "failed to add scan", http.StatusInternalServerError)
		return
	}

	log.Info().Str("folder", req.Folder).Int("priority", req.Priority).Msg("Scan added via UI")
	w.WriteHeader(http.StatusCreated)
}
