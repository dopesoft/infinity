package server

import (
	"encoding/json"
	"net/http"
	"time"
)

type healthResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	UptimeMS int64  `json:"uptime_ms"`
	Time     string `json:"time"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:   "ok",
		Version:  s.cfg.Version,
		UptimeMS: time.Since(s.started).Milliseconds(),
		Time:     time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
