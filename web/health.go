package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
)

type healthReport struct {
	DiskUsagePercent int `json:"diskUsagePercent"`
}

func (s *Server) handleHealthRoot(w http.ResponseWriter, r *http.Request) {
	s.methods(w, r, methodHandlerMap{
		http.MethodGet:  func() { s.handleGetHealth(w, r) },
		http.MethodPost: func() { s.handlePostHealth(w, r) },
	})
}

func (s *Server) handleGetHealth(w http.ResponseWriter, r *http.Request) {
	session, err := s.pool.Take()
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session."))
		return
	}
	defer session.Release()

	diskUsage, err := session.ReadDiskUsage()
	if err != nil {
		session.Log.WithError(err).Warn("Unable to read disk usage")
	}

	report := healthReport{
		DiskUsagePercent: diskUsage,
	}

	if err = json.NewEncoder(w).Encode(&report); err != nil {
		session.Log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to serialize JSON"))
		return
	}
}

type healthRequest struct {
	Action string `json:"action"`
}

func (s *Server) handlePostHealth(w http.ResponseWriter, r *http.Request) {
	session, err := s.pool.Take()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish session."))
		return
	}
	defer session.Release()

	var req healthRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to deserialize secrets map: %v", err)
		return
	}

	switch req.Action {
	case "prune":
		session.Prune()
		w.Write([]byte("ok"))
		return
	case "":
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("\"action\" is required"))
		return
	default:
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unrecognized health action: %v", req.Action)
		return
	}
}
