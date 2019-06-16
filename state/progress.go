package state

import log "github.com/sirupsen/logrus"

// ProgressReporter is used to monitor a synchronization action.
type ProgressReporter interface {
	Report(description string)
}

// LogProgressReporter is a ProgressReporter that emits progress reports to the default logrus reporter.
type LogProgressReporter struct{}

// Report writes a message to the log.
func (r LogProgressReporter) Report(description string) {
	log.Debug(description)
}

// CompositeProgressReporter is a multiplexer that distributes log messages to a collection of other ProgressReporters.
type CompositeProgressReporter struct {
	reporters []ProgressReporter
}

// MakeCompositeReporter assembles a CompositeProgressReporter that dispatches messages to each ProgressReporter in sequence.
func MakeCompositeReporter(reporters ...ProgressReporter) CompositeProgressReporter {
	return CompositeProgressReporter{reporters: reporters}
}

// Report dispatches a progress report to a set of ProgressReporters.
func (r CompositeProgressReporter) Report(description string) {
	for _, reporter := range r.reporters {
		reporter.Report(description)
	}
}
