package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
)

type syncReport struct {
	ts      time.Time
	elapsed time.Duration
	message string
}

type syncReportResponse struct {
	Timestamp int64  `json:"timestamp"`
	Elapsed   int64  `json:"elapsed"`
	Message   string `json:"message"`
}

type syncProgressResponse struct {
	InProgress bool                 `json:"in_progress"`
	Reports    []syncReportResponse `json:"reports"`
	Errors     []string             `json:"errors"`
	Delta      *state.Delta         `json:"delta"`
}

type syncProgress struct {
	lock sync.Mutex

	inProgress bool
	reports    []syncReport
	delta      *state.Delta
	errs       []error
}

func (p *syncProgress) request() bool {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.inProgress {
		return false
	}

	p.inProgress = true
	p.reports = make([]syncReport, 0, 10)
	p.delta = nil
	p.errs = make([]error, 0, 10)
	return true
}

func (p *syncProgress) appendReport(r syncReport) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.reports = append(p.reports, r)
}

func (p *syncProgress) setErrors(errs []error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.errs = errs
	p.inProgress = false
}

func (p *syncProgress) setDelta(d *state.Delta) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.delta = d
	p.inProgress = false
}

func (p *syncProgress) response() syncProgressResponse {
	p.lock.Lock()
	defer p.lock.Unlock()

	reports := make([]syncReportResponse, len(p.reports))
	for i, r := range p.reports {
		reports[i] = syncReportResponse{
			Timestamp: r.ts.Unix(),
			Elapsed:   r.elapsed.Nanoseconds() / 1000000,
			Message:   r.message,
		}
	}

	errors := make([]string, len(p.errs))
	for i, e := range p.errs {
		errors[i] = e.Error()
	}

	return syncProgressResponse{
		InProgress: p.inProgress,
		Reports:    reports,
		Delta:      p.delta,
		Errors:     errors,
	}
}

type syncReporter struct {
	progress *syncProgress
	lastTs   time.Time
}

func (r *syncReporter) Report(description string) {
	ts := time.Now()
	var elapsed time.Duration
	if !r.lastTs.IsZero() {
		elapsed = ts.Sub(r.lastTs)
	}

	report := syncReport{
		ts:      ts,
		elapsed: elapsed,
		message: description,
	}
	r.lastTs = ts

	r.progress.appendReport(report)
}

func (s *Server) performSync() {
	reporter := state.MakeCompositeReporter(
		&syncReporter{progress: s.currentSync},
		state.LogProgressReporter{},
	)

	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		s.currentSync.setErrors([]error{err})
		return
	}

	delta, errs := session.Synchronize(state.SyncSettings{Reporter: reporter})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
		s.currentSync.setErrors(errs)
		return
	}

	s.currentSync.setDelta(delta)
}

func (s *Server) handleSyncRoot(w http.ResponseWriter, r *http.Request) {
	s.cors(w, r, methodHandlerMap{
		http.MethodGet:  func() { s.handleGetSync(w, r) },
		http.MethodPost: func() { s.handleCreateSync(w, r) },
	})
}

func (s *Server) handleGetSync(w http.ResponseWriter, r *http.Request) {
	resp := s.currentSync.response()

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(&resp); err != nil {
		log.WithError(err).Error("Unable to serialize sync progress.")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Unable to serialize sync progress as JSON"))
		return
	}
}

func (s *Server) handleCreateSync(w http.ResponseWriter, r *http.Request) {
	starting := s.currentSync.request()
	if !starting {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("Sync already in progress"))
		return
	}

	go s.performSync()

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("Sync started."))
}
