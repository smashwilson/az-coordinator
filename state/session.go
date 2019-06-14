package state

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

// Session centralizes all of the resources necessary for a single request or operation.
type Session struct {
	db      *sql.DB
	cli     *client.Client
	conn    *dbus.Conn
	secrets *secrets.SecretsBag
}

// NewSession establishes all of the connections necessary to perform an operation.
func NewSession(db *sql.DB, ring *secrets.DecoderRing) (*Session, error) {
	log.Debug("Creating Docker client.")
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	log.Debug("Establishing system DBus connection.")
	conn, err := dbus.NewSystemConnection()
	if err != nil {
		return nil, err
	}

	log.Debug("Loading latest secrets from database.")
	secrets, err := secrets.LoadFromDatabase(db, ring)
	if err != nil {
		return nil, err
	}

	return &Session{
		db:      db,
		cli:     cli,
		conn:    conn,
		secrets: secrets,
	}, nil
}

// PullAllImages concurrently pulls the latest versions of all Docker container images used by desired SystemD units
// referenced by the current system state. Call this between ReadDesiredState and ReadImages to desire the most recently
// published version of each image.
func (s Session) PullAllImages(state DesiredState) []error {
	errs := make([]error, 0)

	imageRefs := make(map[string]bool, len(state.Units))
	for _, unit := range state.Units {
		ref := unit.Container.ImageName + ":" + unit.Container.ImageTag
		imageRefs[ref] = true
	}

	results := make(chan error, len(imageRefs))
	for ref := range imageRefs {
		go s.pullImage(ref, results)
	}
	for i := 0; i < len(imageRefs); i++ {
		errs = append(errs, <-results)
	}

	return errs
}

func (s Session) pullImage(ref string, done chan<- error) {
	progress, err := s.cli.ImagePull(context.Background(), ref, types.ImagePullOptions{})
	if err != nil {
		done <- err
		return
	}
	defer progress.Close()

	payload, err := ioutil.ReadAll(progress)
	if err != nil {
		done <- err
		return
	}
	log.Debugf("ImagePull payload:\n%s\n---\n", payload)

	done <- nil
}

// ValidateSecretKeys returns an error if any of the keys requested in a set are not loaded in the
// session's SecretBag and nil if all are present.
func (s Session) ValidateSecretKeys(secretKeys []string) error {
	missing := make([]string, 0)
	for _, key := range secretKeys {
		if !s.secrets.Has(key) {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("Unrecognized secret keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Close disposes of any connection resources acquired by NewSession.
func (s Session) Close() error {
	s.conn.Close()
	return s.cli.Close()
}
