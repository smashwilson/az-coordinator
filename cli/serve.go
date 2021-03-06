package cli

import (
	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
	"github.com/smashwilson/az-coordinator/web"
)

func serve() {
	r := prepare(needs{
		options: true,
		ring:    true,
		session: true,
		db:      true,
	})
	r.options.CloudwatchLogger(log.StandardLogger())

	log.Info("Performing initial sync.")
	delta, errs := r.session.Synchronize(state.SyncSettings{})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
		log.WithField("errorCount", len(errs)).Fatal("Unable to synchronize.")
	} else {
		log.WithField("delta", delta).Debug("Delta applied.")
	}
	r.session.Release()

	s, err := web.NewServer(r.options, r.db, r.ring)
	if err != nil {
		log.WithError(err).Fatal("Unable to create server.")
	}
	if err := s.Listen(); err != nil {
		log.WithError(err).Fatal("Unable to bind socket.")
	}
}
