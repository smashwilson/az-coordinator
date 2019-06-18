package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s *Server) handleSecretsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListSecrets(w, r)
	case http.MethodPost:
		s.handleCreateSecrets(w, r)
	case http.MethodDelete:
		s.handleDeleteSecrets(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
	}
}

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish session."))
		return
	}

	keys := session.ListSecretKeys()
	encoder := json.NewEncoder(w)
	if err = encoder.Encode(keys); err != nil {
		log.WithFields(log.Fields{
			"err":        err,
			"secretKeys": keys,
		}).Error("Unable to serialize secret keys to JSON")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to serialize secret keys to JSON"))
		return
	}
}

func (s *Server) handleCreateSecrets(w http.ResponseWriter, r *http.Request) {
	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish session."))
		return
	}

	toCreate := make(map[string]string)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&toCreate); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to deserialize secrets map: %v", err)
		return
	}

	if err := session.SetSecrets(toCreate); err != nil {
		log.WithError(err).Error("Unable to persist secret changes.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to persist secret changes."))
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDeleteSecrets(w http.ResponseWriter, r *http.Request) {
	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish session."))
		return
	}

	toDelete := make([]string, 0, 10)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&toDelete); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to deserialize secret keys to delete: %v", err)
		return
	}

	if err := session.DeleteSecrets(toDelete); err != nil {
		log.WithError(err).Error("Unable to persist secret changes.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to persist secret changes."))
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
