package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"regexp"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

// Session centralizes all of the resources necessary for a single request or operation.
type Session struct {
	db      *sql.DB
	ring    *secrets.DecoderRing
	cli     *client.Client
	conn    *dbus.Conn
	secrets *secrets.Bag
	Log     *logrus.Logger
}

// NewSession establishes all of the connections necessary to perform an operation.
func NewSession(db *sql.DB, ring *secrets.DecoderRing, dockerAPIVersion string, log *logrus.Logger) (*Session, error) {
	if log == nil {
		log = logrus.StandardLogger()
	}

	log.Debug("Creating Docker client.")
	cli, err := client.NewClientWithOpts(client.WithVersion(dockerAPIVersion), client.FromEnv)
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
		ring:    ring,
		cli:     cli,
		conn:    conn,
		secrets: secrets,
		Log:     log,
	}, nil
}

// PullAllImages concurrently pulls the latest versions of all Docker container images used by desired SystemD units
// referenced by the current system state. Call this between ReadDesiredState and ReadImages to desire the most recently
// published version of each image.
func (s Session) PullAllImages(state DesiredState) []error {
	errs := make([]error, 0)

	imageRefs := make(map[string]bool, len(state.Units))
	for _, unit := range state.Units {
		if unit.Container != nil && len(unit.Container.ImageName) > 0 && len(unit.Container.ImageTag) > 0 {
			ref := unit.Container.ImageName + ":" + unit.Container.ImageTag
			imageRefs[ref] = true
			s.Log.WithField("ref", ref).Debug("Scheduling docker pull.")
		}
	}

	s.Log.WithField("count", len(imageRefs)).Debug("Beginning docker pulls.")
	results := make(chan error, len(imageRefs))
	for ref := range imageRefs {
		go s.pullImage(ref, results)
	}
	for i := 0; i < len(imageRefs); i++ {
		err := <-results
		if err != nil {
			errs = append(errs, err)
		}
	}
	s.Log.WithField("count", len(imageRefs)).Debug("Docker pulls complete.")

	return errs
}

var (
	rxUpToDate = regexp.MustCompile(`Status: Image is up to date`)
	rxDownloadedNewer = regexp.MustCompile(`Status: Downloaded newer image`)
)

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

	if rxUpToDate.Match(payload) {
		s.Log.WithField("ref", ref).Debug("Container image already current.")
	} else if rxDownloadedNewer.Match(payload) {
		s.Log.WithField("ref", ref).Info("Container image updated.")
	} else {
		s.Log.WithField("ref", ref).Warningf("Unrecognized ImagePull payload:\n%s\n---\n", payload)
	}

	done <- nil
}

// CreateNetwork ensures that the expected Docker backplane network is present.
func (s Session) CreateNetwork() error {
	networks, err := s.cli.NetworkList(context.Background(), types.NetworkListOptions{})
	if err != nil {
		return err
	}

	for _, network := range networks {
		if network.Name == "local" {
			// Network already exists
			s.Log.WithFields(logrus.Fields{
				"networkID":     network.ID,
				"networkName":   network.Name,
				"networkDriver": network.Driver,
			}).Info("Network already exists.")
			return nil
		}
	}

	response, err := s.cli.NetworkCreate(context.Background(), "local", types.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "bridge",
		IPAM: &network.IPAM{
			Driver: "default",
		},
		Internal: false,
	})
	if err != nil {
		return err
	}

	s.Log.WithField("networkID", response.ID).Debug("Network created.")
	return nil
}

// Prune removes stopped containers and unused container images to reclaim disk space.
func (s Session) Prune() {
  cr, err := s.cli.ContainersPrune(context.Background(), filters.NewArgs())
  if err != nil {
    s.Log.WithError(err).Warning("Unable to prune containers.")
  } else {
    s.Log.WithFields(logrus.Fields{
      "containers": len(cr.ContainersDeleted),
      "spaceReclained": cr.SpaceReclaimed,
    }).Debug("Containers removed.")
  }

  ir, err := s.cli.ImagesPrune(context.Background(), filters.NewArgs())
  if err != nil {
    s.Log.WithError(err).Warning("Unable to prune images.")
  } else {
    s.Log.WithFields(logrus.Fields{
      "images": len(ir.ImagesDeleted),
      "spaceReclaimed": cr.SpaceReclaimed,
    }).Debug("Images removed.")
  }
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

// ListSecretKeys enumerates the known secret keys.
func (s Session) ListSecretKeys() []string {
	return s.secrets.Keys()
}

// SetSecrets adds or updates the values associated with many secrets at once, then persists
// them to the database.
func (s Session) SetSecrets(secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}

	for key, value := range secrets {
		s.secrets.Set(key, value)
	}

	return s.secrets.SaveToDatabase(s.db, s.ring, false)
}

// DeleteSecrets removes the values associated with many secret keys, then persists the changed
// bag to the database.
func (s Session) DeleteSecrets(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		s.secrets.Delete(key)
	}

	return s.secrets.SaveToDatabase(s.db, s.ring, true)
}

// SyncSettings configures synchronization behavior.
type SyncSettings struct {
	UID int
	GID int
}

// Synchronize brings local Docker images up to date, then reads desired and actual state, computes a
// Delta between them, and applies it. The applied Delta is returned.
func (s *Session) Synchronize(settings SyncSettings) (*Delta, []error) {
	uid := -1
	gid := -1
	if settings.UID != 0 {
		uid = settings.UID
	}
	if settings.GID != 0 {
		gid = settings.GID
	}

	s.Log.Info("Reading desired state.")
	desired, err := s.ReadDesiredState()
	if err != nil {
		return nil, []error{err}
	}

	s.Log.Info("Pulling referenced images.")
	if errs := s.PullAllImages(*desired); len(errs) > 0 {
		return nil, append(errs, errors.New("pull errors"))
	}

	s.Log.Info("Reading updated docker images.")
	if err = desired.ReadImages(s); err != nil {
		return nil, []error{err, errors.New("unable to pull docker images")}
	}

	s.Log.Info("Reading actual state.")
	actual, err := s.ReadActualState()
	if err != nil {
		return nil, []error{err, errors.New("unable to read system state")}
	}

	s.Log.Info("Computing delta.")
	delta := s.Between(desired, actual)

	if errs := delta.Apply(uid, gid); len(errs) > 0 {
		return nil, append(errs, errors.New("unable to apply delta"))
	}

  s.Log.Info("Pruning unused docker data.")
  s.Prune()

	return &delta, nil
}

// Close disposes of any connection resources acquired by NewSession.
func (s Session) Close() error {
	s.conn.Close()
	return s.cli.Close()
}
