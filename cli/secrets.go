package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

func setSecrets() {
	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "set-secrets requires at least one argument: the path to a JSON file.\n")
		writeHelp(os.Stderr, 1)
	}

	var r = prepare(needs{options: true, db: true})

	var toLoad map[string]string
	inf, err := os.Open(flag.Arg(1))
	if err != nil {
		log.WithError(err).WithField("path", flag.Arg(1)).Fatal("Unable to load secrets file.")
	}
	decoder := json.NewDecoder(inf)
	if err = decoder.Decode(&toLoad); err != nil {
		log.WithError(err).WithField("path", flag.Arg(1)).Fatal("Unable to parse secrets file.")
	}

	log.Info("Creating decoder ring.")
	ring, err := secrets.NewDecoderRing(r.options.MasterKeyID, r.options.AWSRegion)
	if err != nil {
		log.WithError(err).Fatal("Unable to create decoder ring.")
	}

	log.Info("Loading and decrypting existing secrets.")
	bag, err := secrets.LoadFromDatabase(r.db, ring)
	if err != nil {
		log.WithError(err).Fatal("Unable to load and decrypt existing secrets.")
	}
	log.WithField("count", bag.Len()).Info("Secrets loaded successfully.")

	for k, v := range toLoad {
		bag.Set(k, v)
	}

	if err = bag.SaveToDatabase(r.db, ring); err != nil {
		log.WithError(err).Fatal("Unable to encrypt and save new secrets.")
	}

	log.WithFields(log.Fields{"count": bag.Len(), "added": len(toLoad)}).Info("Secrets added successfully.")
}
