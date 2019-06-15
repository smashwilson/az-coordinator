package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	_ "github.com/lib/pq"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
	"github.com/smashwilson/az-coordinator/state"
)

type server struct {
	opts *options
	db   *sql.DB
	ring *secrets.DecoderRing
}

func newServer(opts *options, db *sql.DB, ring *secrets.DecoderRing) server {
	s := server{
		opts: opts,
		db:   db,
		ring: ring,
	}

	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/desired", s.protected(s.handleDesiredRoot))
	http.HandleFunc("/actual", s.protected(s.handleListActual))
	http.HandleFunc("/diff", s.protected(s.handleDiff))
	http.HandleFunc("/sync", s.protected(s.handleSync))

	return s
}

func (s server) listen() error {
	log.WithField("address", s.opts.ListenAddress).Info("Now serving.")
	return http.ListenAndServeTLS(s.opts.ListenAddress, secrets.FilenameTLSCertificate, secrets.FilenameTLSKey, nil)
}

func (s server) protected(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, password, ok := r.BasicAuth(); !ok || password != s.opts.AuthToken {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
			return
		}

		handler(w, r)
	}
}

func (s server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func (s server) handleDiff(w http.ResponseWriter, r *http.Request) {
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

func (s server) handleSync(w http.ResponseWriter, r *http.Request) {
	//
}
