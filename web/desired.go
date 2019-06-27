package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
)

func (s Server) handleDesiredRoot(w http.ResponseWriter, r *http.Request) {
	s.cors(w, r, methodHandlerMap{
		http.MethodGet:  func() { s.handleListDesired(w, r) },
		http.MethodPost: func() { s.handleCreateDesired(w, r) },
	})
}

var desiredRx = regexp.MustCompile(`^/desired/(\d+)$`)

func (s Server) handleDesired(w http.ResponseWriter, r *http.Request) {
	rawID, ok := extractID(desiredRx, w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(rawID, 10, 32)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Non-numeric desired unit ID (%s)", rawID)
		return
	}

	s.cors(w, r, methodHandlerMap{
		http.MethodPut:    func() { s.handleUpdateDesired(w, r, int(id)) },
		http.MethodDelete: func() { s.handleDeleteDesired(w, r, int(id)) },
	})
}

func (s Server) handleListDesired(w http.ResponseWriter, r *http.Request) {
	session, err := s.newSession()
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

func (s Server) handleCreateDesired(w http.ResponseWriter, r *http.Request) {
	type createRequestContainer struct {
		Name      string `json:"name"`
		ImageName string `json:"image_name"`
		ImageTag  string `json:"image_tag"`
	}

	type createRequest struct {
		Path      string                 `json:"path"`
		Type      state.UnitType         `json:"type"`
		Container *createRequestContainer `json:"container,omitempty"`
		Secrets   []string               `json:"secrets"`
		Env       map[string]string      `json:"env"`
		Ports     map[int]int            `json:"ports"`
		Volumes   map[string]string      `json:"volumes"`
		Schedule  string                 `json:"calendar"`
	}

	session, err := s.newSession()
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

	builder := state.BuildDesiredUnit()
	errs := make([]error, 0)
	tried := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	tried(builder.Path(desiredReq.Path))
	tried(builder.Type(desiredReq.Type))
	if desiredReq.Container != nil {
		tried(builder.Container(desiredReq.Container.ImageName, desiredReq.Container.ImageTag, desiredReq.Container.Name))
	}
	tried(builder.Secrets(desiredReq.Secrets, *session))
	tried(builder.Volumes(desiredReq.Volumes))
	tried(builder.Env(desiredReq.Env))
	tried(builder.Ports(desiredReq.Ports))
	tried(builder.Schedule(desiredReq.Schedule))

	desired, err := builder.Build()
	tried(err)

	if len(errs) > 0 {
		var message strings.Builder
		message.WriteString("Invalid desired unit:\n")
		for i, err := range errs {
			log.WithError(err).Warn("Invalid desired unit.")
			message.WriteString(err.Error())
			if i != len(errs)-1 {
				message.WriteString("\n")
			}
		}

		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(message.String()))
		return
	}

	if err = desired.MakeDesired(*session); err != nil {
		log.WithError(err).Error("Unable to serialize desired unit.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to store desired unit in the database"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(&desired)
}

func (s Server) handleUpdateDesired(w http.ResponseWriter, r *http.Request, id int) {
	type updateRequestContainer struct {
		Name      string `json:"name"`
		ImageName string `json:"image_name"`
		ImageTag  string `json:"image_tag"`
	}

	type updateRequest struct {
		Type      state.UnitType         `json:"type"`
		Container updateRequestContainer `json:"container"`
		Secrets   []string               `json:"secrets"`
		Env       map[string]string      `json:"env"`
		Ports     map[int]int            `json:"ports"`
		Volumes   map[string]string      `json:"volumes"`
		Schedule  string                 `json:"calendar,omitempty"`
	}

	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session"))
		return
	}

	unit, err := session.ReadDesiredUnit(id)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
			"id":  id,
		}).Error("Unable to load a desired unit.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Something went wrong with the database"))
		return
	}

	if unit == nil {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Desired unit not found"))
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var updateReq updateRequest
	if err = decoder.Decode(&updateReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to parse request body as JSON: %v", err)
		return
	}

	// Normalize and sanitize the request to modify the loaded unit.
	builder := state.ModifyDesiredUnit(unit)
	errs := make([]error, 0)
	tried := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	tried(builder.Type(updateReq.Type))
	tried(builder.Container(updateReq.Container.ImageName, updateReq.Container.ImageTag, updateReq.Container.Name))
	tried(builder.Secrets(updateReq.Secrets, *session))
	tried(builder.Volumes(updateReq.Volumes))
	tried(builder.Env(updateReq.Env))
	tried(builder.Ports(updateReq.Ports))
	tried(builder.Schedule(updateReq.Schedule))
	_, err = builder.Build()
	tried(err)

	if len(errs) > 0 {
		var message strings.Builder
		message.WriteString("Invalid desired unit:\n")
		for i, err := range errs {
			log.WithError(err).Warn("Invalid desired unit.")
			message.WriteString(err.Error())
			if i != len(errs)-1 {
				message.WriteString("\n")
			}
		}

		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(message.String()))
		return
	}

	if err = unit.Update(*session); err != nil {
		log.WithError(err).Error("Unable to serialize desired unit.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to store the updated unit in the database"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(unit)
}

func (s Server) handleDeleteDesired(w http.ResponseWriter, r *http.Request, id int) {
	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish a session.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to establish a session"))
		return
	}

	if err := session.UndesireUnit(id); err != nil {
		log.WithError(err).Error("Unable to delete unit.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to delete unit"))
	}

	w.WriteHeader(http.StatusCreated)
}
