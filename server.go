package main

import (
	"database/sql"
	"log"
	"net/http"

	_ "github.com/lib/pq"

	"github.com/smashwilson/az-coordinator/secrets"
)

type server struct {
	opts *options
	db   *sql.DB
}

func newServer() (*server, error) {
	opts, err := loadOptions()
	if err != nil {
		return nil, err
	}

	log.Println("Connecting to database")
	db, err := sql.Open("postgres", opts.DatabaseURL)
	if err != nil {
		return nil, err
	}

	log.Println("Creating decoder ring")
	ring, err := secrets.NewDecoderRing(opts.MasterKeyId)
	if err != nil {
		return nil, err
	}

	log.Println("Loading secrets")
	bag, err := secrets.LoadFromDatabase(db, ring)
	if err != nil {
		return nil, err
	}

	if _, err = bag.WriteTLSFiles(); err != nil {
		return nil, err
	}

	s := server{opts: opts, db: db}

	http.HandleFunc("/", s.handleRoot)
	http.HandleFunc("/status", s.protected(s.handleStatus))
	http.HandleFunc("/update", s.protected(s.handleUpdate))

	return &s, nil
}

func (s server) listen() error {
	log.Printf("Serving on address: %s\n", s.opts.ListenAddress)
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
