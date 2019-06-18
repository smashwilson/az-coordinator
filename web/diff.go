package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
)

func (s Server) handleDiffRoot(w http.ResponseWriter, r *http.Request) {
	s.cors(w, r, methodHandlerMap{
		http.MethodGet: func() { s.handleGetDiff(w, r) },
	})
}

func (s Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	session, err := state.NewSession(s.db, s.ring, s.opts.DockerAPIVersion)
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

	desired, err := session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Error("Unable to load the desired system state.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to load the desired system state.\n")
		return
	}

	delta := session.Between(desired, actual)
	if err = json.NewEncoder(w).Encode(&delta); err != nil {
		log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to serialize JSON.\n")
		return
	}
}
