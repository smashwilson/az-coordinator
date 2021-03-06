package web

import (
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s Server) handleDiffRoot(w http.ResponseWriter, r *http.Request) {
	s.methods(w, r, methodHandlerMap{
		http.MethodGet: func() { s.handleGetDiff(w, r) },
	})
}

func (s Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	session, err := s.pool.Take()
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session."))
		return
	}
	defer session.Release()

	actual, err := session.ReadActualState()
	if err != nil {
		session.Log.WithError(err).Error("Unable to load the actual system state.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to load the actual system state."))
		return
	}

	desired, err := session.ReadDesiredState()
	if err != nil {
		session.Log.WithError(err).Error("Unable to load the desired system state.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to load the desired system state."))
		return
	}

	if err = desired.ReadImages(session); err != nil {
		session.Log.WithError(err).Error("Unable to read current container images.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to read desired container images."))
		return
	}

	if errs := actual.ReadImages(session, *desired); len(errs) > 0 {
		for _, err := range errs {
			session.Log.WithError(err).Warn("Unable to read actual image.")
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to read running container images."))
		return
	}

	delta := session.Between(desired, actual)
	if err = json.NewEncoder(w).Encode(&delta); err != nil {
		session.Log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to serialize JSON"))
		return
	}
}
