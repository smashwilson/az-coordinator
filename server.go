package main

import (
	"database/sql"
	"net/http"

	_ "github.com/lib/pq"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

type server struct {
	opts *options
	db   *sql.DB
}

func newServer(opts *options, db *sql.DB) server {
	s := server{opts: opts, db: db}

	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/status", s.protected(s.handleStatus))
	http.HandleFunc("/update", s.protected(s.handleUpdate))

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
		}

		handler(w, r)
	}
}

func (s server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func (s server) handleStatus(w http.ResponseWriter, r *http.Request) {
	//
}

func (s server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	//
}
