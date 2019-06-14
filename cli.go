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
	ring    bool
	session bool
}

type results struct {
	options *options
	db      *sql.DB
	ring    *secrets.DecoderRing
	session *state.Session
}

func prepare(n needs) results {
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

	if n.ring || n.session {
		log.WithField("keyID", r.options.MasterKeyID).Info("Creating decoder ring.")
		r.ring, err = secrets.NewDecoderRing(r.options.MasterKeyID, r.options.AWSRegion)
		if err != nil {
			log.WithError(err).Fatal("Unable to create decoder ring.")
		}
	}

	if n.session {
		log.Info("Establishing session.")
		r.session, err = state.NewSession(r.db, r.ring, r.options.DockerAPIVersion)
		if err != nil {
			log.WithError(err).Fatal("Unable to create session.")
		}
	}

	return r
}
