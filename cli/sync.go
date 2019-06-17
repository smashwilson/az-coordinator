package cli

import (
	"encoding/json"
	"os"

	"github.com/smashwilson/az-coordinator/state"

	log "github.com/sirupsen/logrus"
)

func sync() {
	r := prepare(needs{session: true})
	delta, errs := r.session.Synchronize(state.SyncSettings{})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
	} else {
		log.WithField("delta", delta).Debug("Delta applied.")
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.WithError(err).Fatal("Unable to write JSON.")
	}
}
