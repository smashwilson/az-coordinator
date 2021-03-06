package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/slack"
	"github.com/smashwilson/az-coordinator/state"
)

type syncReport struct {
	ts      time.Time
	elapsed time.Duration
	message string
	fields  log.Fields
}

type syncReportResponse struct {
	Timestamp int64      `json:"timestamp"`
	Elapsed   int64      `json:"elapsed"`
	Message   string     `json:"message"`
	Fields    log.Fields `json:"fields"`
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
			Fields:    r.fields,
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

type syncHook struct {
	progress *syncProgress
	lastTs   time.Time
}

func (h *syncHook) Levels() []log.Level {
	return log.AllLevels
}

func (h *syncHook) Fire(entry *log.Entry) error {
	var elapsed time.Duration
	if !h.lastTs.IsZero() {
		elapsed = entry.Time.Sub(h.lastTs)
	}

	report := syncReport{
		ts:      entry.Time,
		elapsed: elapsed,
		message: entry.Message,
		fields:  entry.Data,
	}
	h.lastTs = entry.Time
	h.progress.appendReport(report)

	return nil
}

func (s *Server) performSync() {
	logger := log.New()
	logger.SetLevel(log.TraceLevel)
	logger.AddHook(&syncHook{
		progress: s.currentSync,
	})

	s.opts.CloudwatchLogger(logger)

	session, err := s.pool.Take()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		s.currentSync.setErrors([]error{err})
		return
	}
	defer session.Release()
	session.WithLogger(logger)

	delta, errs := session.Synchronize(state.SyncSettings{})
	if len(s.opts.SlackWebhookURL) > 0 {
		slack.ReportSync(s.opts.SlackWebhookURL, delta, errs)
	}

	if len(errs) > 0 {
		for _, err := range errs {
			session.Log.WithError(err).Warn("Synchronization error.")
		}
		s.currentSync.setErrors(errs)
		return
	}

	s.currentSync.setDelta(delta)
}

func (s *Server) handleSyncRoot(w http.ResponseWriter, r *http.Request) {
	s.methods(w, r, methodHandlerMap{
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
