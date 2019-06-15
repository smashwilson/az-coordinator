package cli

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

func sync() {
	r := prepare(needs{session: true})
	delta := performSync(r.session)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(delta); err != nil {
		log.WithError(err).Fatal("Unable to write JSON.")
	}
}
