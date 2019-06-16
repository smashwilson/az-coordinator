package web

import (
	"database/sql"
	"net/http"
	"regexp"

	"github.com/smashwilson/az-coordinator/state"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/config"
	"github.com/smashwilson/az-coordinator/secrets"
)

// Server represents the persistent state associated with any HTTP handlers.
type Server struct {
	opts *config.Options
	db   *sql.DB
	ring *secrets.DecoderRing
}

// NewServer creates (but does not start) an HTTP server for the coordinator management interface.
func NewServer(opts *config.Options, db *sql.DB, ring *secrets.DecoderRing) Server {
	s := Server{
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

// Listen binds a socket to the address requested by the current Options. It only returns if there's an error.
func (s Server) Listen() error {
	log.WithField("address", s.opts.ListenAddress).Info("Now serving.")
	return http.ListenAndServeTLS(s.opts.ListenAddress, secrets.FilenameTLSCertificate, secrets.FilenameTLSKey, nil)
}

func (s Server) protected(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, password, ok := r.BasicAuth(); !ok || password != s.opts.AuthToken {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
			return
		}

		handler(w, r)
	}
}

func (s Server) newSession() (*state.Session, error) {
	return state.NewSession(s.db, s.ring, s.opts.DockerAPIVersion)
}

func extractID(rx *regexp.Regexp, w http.ResponseWriter, r *http.Request) (string, bool) {
	ms := rx.FindStringSubmatch(r.URL.Path)
	// (0) full match; (1) extracted id
	if len(ms) != 2 {
		log.WithField("path", r.URL.Path).Error("ID extraction regexp did not match")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not found"))
		return "", false
	}
	return ms[1], true
}
