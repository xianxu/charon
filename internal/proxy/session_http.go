package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// /session/{arm,disarm,status} HTTP handlers. Today these are POST-able
// from anywhere on localhost (the proxy listens on 127.0.0.1 by default
// so reachable only from the same uid). #16 C will gate arm/disarm
// behind the unix-socket trust edge so only Charon Security.app can
// drive them; until then the CLI talks to these endpoints directly.

type armRequest struct {
	// TTLSeconds is the requested arm duration. 0 means default
	// (SessionDefaultTTL). Capped at SessionAbsoluteCap.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

type armResponse struct {
	OK     bool          `json:"ok"`
	Status SessionStatus `json:"status"`
}

func (s *Server) handleSessionArm(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req armRequest
	// Empty body is fine — defaults to SessionDefaultTTL.
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid body: %v", err), http.StatusBadRequest)
			return
		}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	s.Session.Arm(ttl)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(armResponse{OK: true, Status: s.Session.Status()})
}

func (s *Server) handleSessionDisarm(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.Session.Disarm()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(armResponse{OK: true, Status: s.Session.Status()})
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	if s.Session == nil {
		http.Error(w, "session not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Session.Status())
}
