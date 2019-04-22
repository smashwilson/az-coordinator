package main

import (
	"database/sql"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
	"github.com/smashwilson/az-coordinator/state"
)

type needs struct {
	options bool
	db      bool
	session bool
}

type results struct {
	options *options
	db      *sql.DB
	session *state.Session
}

func Prepare(n needs) results {
	var (
		r   results
		err error
	)

	if n.options || n.db || n.session {
		r.options, err = loadOptions()
		if err != nil {
			log.WithError(err).Fatal("Unable to load options.")
		}
	}

	if n.db || n.session {
		log.Info("Connecting to database.")
		r.db, err = sql.Open("postgres", r.options.DatabaseURL)
		if err != nil {
			log.WithError(err).Fatal("Unable to connect to database.")
		}
	}

	if n.session {
		log.Info("Creating decoder ring.")
		ring, rErr := secrets.NewDecoderRing(r.options.MasterKeyId)
		if rErr != nil {
			log.WithError(rErr).Fatal("Unable to create decoder ring.")
		}

		r.session, err = state.NewSession(r.db, ring)
		if err != nil {
			log.WithError(err).Fatal("Unable to create session.")
		}
	}

	return r
}
