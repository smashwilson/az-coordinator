package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/config"
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
	options *config.Options
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
		r.options, err = config.Load()
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

func writeHelp(out io.Writer, exitCode int) {
	fmt.Fprintf(out, "Usage: %s [flags] [command]\n", os.Args[0])
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "Flags:\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "  --verbose,-v  Log everything that can be logged.\n")
	fmt.Fprintf(out, "  --quiet,-q    Log only errors and warnings.\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "Commands:\n")
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "  help         Show this message.\n")
	fmt.Fprintf(out, "  init         Bootstrap the host and database if needed. Run as root.\n")
	fmt.Fprintf(out, "  set-secrets  Add or override existing secrets from a JSON file.\n")
	fmt.Fprintf(out, "  diff         Calculate the actions needed to be taken to bring the system to its desired state.\n")
	fmt.Fprintf(out, "  sync         Bring the system to its desired state. Report the actions taken.\n")
	fmt.Fprintf(out, "  serve        Begin the server that hosts the management API.\n")
	os.Exit(exitCode)
}

func performSync(session *state.Session) state.Delta {
	log.Info("Reading desired state.")
	desired, err := session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read desired state.")
	}

	log.Info("Pulling referenced images.")
	if errs := session.PullAllImages(*desired); len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Pull error")
		}
		log.Warn("Encountered errors when pulling images.")
	}

	log.Info("Reading updated docker images.")
	if err = desired.ReadImages(session); err != nil {
		log.WithError(err).Fatal("Unable to read docker image IDs.")
	}

	log.Info("Reading actual state.")
	actual, err := session.ReadActualState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read actual state.")
	}

	log.Info("Computing delta.")
	delta := session.Between(desired, actual)

	if errs := delta.Apply(); len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Delta application error.")
		}
		log.Warn("Unable to apply delta.")
	}

	return delta
}
