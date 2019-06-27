package web

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"

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

	currentSync *syncProgress
}

// NewServer creates (but does not start) an HTTP server for the coordinator management interface.
func NewServer(opts *config.Options, db *sql.DB, ring *secrets.DecoderRing) Server {
	s := Server{
		opts:        opts,
		db:          db,
		ring:        ring,
		currentSync: &syncProgress{},
	}

	http.HandleFunc("/", s.wrap(s.handleRoot, false))
	http.HandleFunc("/secrets", s.wrap(s.handleSecretsRoot, true))
	http.HandleFunc("/desired", s.wrap(s.handleDesiredRoot, true))
	http.HandleFunc("/desired/", s.wrap(s.handleDesired, true))
	http.HandleFunc("/actual", s.wrap(s.handleActualRoot, true))
	http.HandleFunc("/diff", s.wrap(s.handleDiffRoot, true))
	http.HandleFunc("/sync", s.wrap(s.handleSyncRoot, true))

	return s
}

// Listen binds a socket to the address requested by the current Options. It only returns if there's an error.
func (s Server) Listen() error {
	log.WithField("address", s.opts.ListenAddress).Info("Now serving.")
	return http.ListenAndServeTLS(s.opts.ListenAddress, secrets.FilenameTLSCertificate, secrets.FilenameTLSKey, nil)
}

var allowedMethods = map[string]bool{
	"GET": true,
	"POST": true,
	"PUT": true,
	"DELETE": true,
	"OPTIONS": true,
}

func buildMethodList() string {
	ms := []string{}
	for m := range allowedMethods {
		ms = append(ms, m)
	}
	return strings.Join(ms, ", ")
}

func (s Server) wrap(handler func(http.ResponseWriter, *http.Request), protected bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		log.WithFields(log.Fields{
			"method": r.Method,
			"username": username,
			"password": password,
			"path": r.URL.Path,
			"headers": r.Header,
		}).Debug("Request.")

		// CORS preflight requests
		w.Header().Set("Access-Control-Allow-Origin", s.opts.AllowedOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", buildMethodList())
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "60")

		if r.Method == http.MethodOptions {
			proposedMethod := r.Header.Get("Access-Control-Request-Method")
			if _, ok := allowedMethods[proposedMethod]; !ok {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("Invalid method requested in CORS preflight request."))
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
		}

		if protected && (!ok || password != s.opts.AuthToken) {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
			return
		}

		handler(w, r)
	}
}

type methodHandlerMap map[string]func()

func (s Server) methods(w http.ResponseWriter, r *http.Request, handlers methodHandlerMap) {
	handler, ok := handlers[r.Method]
	if !ok {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}

	handler()
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
