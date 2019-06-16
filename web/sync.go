package web

import (
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
)

type syncReport struct {
	ts      time.Time
	elapsed time.Duration
	message string
}

type syncProgress struct {
	reportChan chan syncReport
	deltaChan  chan *state.Delta
	errsChan   chan []error

	reports []syncReport
	delta   *state.Delta
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
	r.progress.reportChan <- report
}

func beginSyncReporter(server *Server) *syncReporter {
	p := syncProgress{}
	r := syncReporter{progress: &p}

	server.currentSync = &p

	return &r
}

func (s *Server) performSync() {
	reporter := state.MakeCompositeReporter(
		beginSyncReporter(s),
		state.LogProgressReporter{},
	)

	session, err := s.newSession()
	if err != nil {
		log.WithError(err).Error("Unable to establish session.")
		s.currentSync.errsChan <- []error{err}
		return
	}

	delta, errs := session.Synchronize(reporter)
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
		s.currentSync.errsChan <- errs
		return
	}

	s.currentSync.deltaChan <- delta
}
