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
			log.Fatalf("Unable to load options: %v.\n", err)
		}
	}

	if n.db || n.session {
		log.Println("Connecting to database")
		r.db, err = sql.Open("postgres", r.options.DatabaseURL)
		if err != nil {
			log.Fatalf("Unable to connect to database: %v.\n", err)
		}
	}

	if n.session {
		log.Println("Creating decoder ring")
		ring, rErr := secrets.NewDecoderRing(r.options.MasterKeyId)
		if rErr != nil {
			log.Fatalf("Unable to create decoder ring: %v.\n", rErr)
		}

		r.session, err = state.NewSession(r.db, ring)
		if err != nil {
			log.Fatalf("Unable to create session: %v.\n", err)
		}
	}

	return r
}
