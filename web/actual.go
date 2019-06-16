package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s Server) handleListActual(w http.ResponseWriter, r *http.Request) {
	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to establish a session.\n")
		return
	}

	actual, err := session.ReadActualState()
	if err != nil {
		log.WithError(err).Error("Unable to load the actual system state.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to load the actual system state.\n")
		return
	}

	if err = json.NewEncoder(w).Encode(&actual); err != nil {
		log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to serialize JSON.\n")
		return
	}
}