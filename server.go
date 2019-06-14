package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

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

func (s server) handleDesiredRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListDesired(w, r)
	case http.MethodPost:
		s.handleCreateDesired(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
	}
}

func (s server) handleListDesired(w http.ResponseWriter, r *http.Request) {
	session, err := state.NewSession(s.db, s.ring, s.opts.DockerAPIVersion)
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session"))
		return
	}

	desired, err := session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Error("Unable to load the desired system state.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to load the desired system state"))
		return
	}

	if err = json.NewEncoder(w).Encode(&desired); err != nil {
		log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to serialize JSON"))
		return
	}
}

func (s server) handleCreateDesired(w http.ResponseWriter, r *http.Request) {
	type createRequestContainer struct {
		Name      string `json:"name"`
		ImageName string `json:"image_name"`
		ImageTag  string `json:"image_tag"`
	}

	type createRequest struct {
		Path      string                 `json:"path"`
		TypeName  string                 `json:"type"`
		Container createRequestContainer `json:"container"`
		Secrets   []string               `json:"secrets"`
		Env       map[string]string      `json:"env"`
		Ports     map[int]int            `json:"ports"`
		Volumes   map[string]string      `json:"volumes"`
		Schedule  string                 `json:"calendar,omitempty"`
	}

	session, err := state.NewSession(s.db, s.ring, s.opts.DockerAPIVersion)
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session"))
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var desiredReq createRequest
	if err = decoder.Decode(&desiredReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to parse request body as JSON: %v", err)
		return
	}

	var desired state.DesiredSystemdUnit

	// Normalize and sanitize the request to populate desired.

	// Path
	desiredReq.Path = filepath.Clean(desiredReq.Path)
	if !strings.HasPrefix(desiredReq.Path, "/etc/systemd/system/az-") {
		log.WithField("path", desiredReq.Path).Warn("Attempt to create desired unit file in invalid location.")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Attempt to create desired unit with invalid path"))
		return
	}
	desired.Path = desiredReq.Path

	// Type
	if desired.Type, err = state.GetTypeWithName(desiredReq.TypeName); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	// Container.ImageName, Container.ImageTag, and Container.Name
	if desired.Type == state.TypeSimple || desired.Type == state.TypeOneShot {
		if !strings.HasPrefix(desiredReq.Container.ImageName, "quay.io/smashwilson/az-") {
			log.WithField("image-name", desiredReq.Container.ImageName).Warn("Attempt to create desired unit with invalid container image.")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Attempt to create desired unit with invalid container image"))
			return
		}

		if len(desiredReq.Container.ImageTag) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Attempt to create desired unit without an image tag"))
			return
		}

		if desired.Type == state.TypeSimple && len(desiredReq.Container.Name) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Attempt to create desired unit without a container name"))
			return
		}
	} else {
		if len(desiredReq.Container.ImageName) > 0 || len(desiredReq.Container.ImageTag) > 0 || len(desiredReq.Container.Name) > 0 {
			log.WithFields(log.Fields{
				"name":      desiredReq.Container.Name,
				"imageName": desiredReq.Container.ImageName,
				"imageTag":  desiredReq.Container.ImageTag,
				"unitType":  desiredReq.TypeName,
			}).Warn("Attempt to specify container information for unit type that does not use one")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Attempt to specify container information for unit type that does not use one"))
			return
		}
	}
	desired.Container.Name = desiredReq.Container.Name
	desired.Container.ImageName = desiredReq.Container.ImageName
	desired.Container.ImageTag = desiredReq.Container.ImageTag

	// Secrets
	if err = session.ValidateSecretKeys(desiredReq.Secrets); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid secret keys: %v", err)
		return
	}
	desired.Secrets = desiredReq.Secrets

	// Volumes
	var badVolumes []string
	desired.Volumes = make(map[string]string, len(desiredReq.Volumes))
	for hostPath, containerPath := range desiredReq.Volumes {
		normalizedHostPath := filepath.Clean(hostPath)
		if !strings.HasPrefix(normalizedHostPath, "/etc/ssl/az/") {
			badVolumes = append(badVolumes, hostPath)
		} else {
			desired.Volumes[normalizedHostPath] = containerPath
		}
	}
	if len(badVolumes) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid host volumes: %v", strings.Join(badVolumes, ", "))
		return
	}

	// Copy remaining fields as-is
	desired.Env = desiredReq.Env
	desired.Ports = desiredReq.Ports
	desired.Schedule = desiredReq.Schedule

	if err = desired.MakeDesired(*session); err != nil {
		log.WithError(err).Error("Unable to serialize desired unit.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to store desired unit in the database"))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(&desired)
}

func (s server) handleListActual(w http.ResponseWriter, r *http.Request) {
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

	if err = json.NewEncoder(w).Encode(&actual); err != nil {
		log.WithError(err).Error("Unable to serialize JSON.")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Unable to serialize JSON.\n")
		return
	}
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
