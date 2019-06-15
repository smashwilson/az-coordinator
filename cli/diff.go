package cli

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

func diff() {
	var r = prepare(needs{session: true})

	log.Info("Reading desired state.")
	desired, err := r.session.ReadDesiredState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read desired state.")
	}

	if err = desired.ReadImages(r.session); err != nil {
		log.WithError(err).Fatal("Unable to read Docker images.")
	}

	log.Info("Reading actual state.")
	actual, err := r.session.ReadActualState()
	if err != nil {
		log.WithError(err).Fatal("Unable to read actual state.")
	}

	log.Info("Computing delta.")
	delta := r.session.Between(desired, actual)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.Fatalf("Unable to write JSON: %v.\n", err)
	}
}
