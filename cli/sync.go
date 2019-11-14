package cli

import (
	"encoding/json"
	"os"

	"github.com/smashwilson/az-coordinator/slack"
	"github.com/smashwilson/az-coordinator/state"

	log "github.com/sirupsen/logrus"
)

func sync() {
	r := prepare(needs{options: true, session: true})
	defer r.session.Release()
	delta, errs := r.session.Synchronize(state.SyncSettings{})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
	} else {
		log.WithField("delta", delta).Debug("Delta applied.")
	}

	if len(r.options.SlackWebhookURL) > 0 {
		slack.ReportSync(r.options.SlackWebhookURL, delta, errs)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.WithError(err).Fatal("Unable to write JSON.")
	}
}
