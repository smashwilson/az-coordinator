package state

import (
	"context"
	"database/sql"
	"io/ioutil"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

type Session struct {
	db      *sql.DB
	cli     *client.Client
	conn    *dbus.Conn
	secrets *secrets.SecretsBag
}

func NewSession(db *sql.DB, ring *secrets.DecoderRing) (*Session, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	conn, err := dbus.NewSystemConnection()
	if err != nil {
		return nil, err
	}

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

func (s Session) PullAllImages(state DesiredState) []error {
	errs := make([]error, 0)

	imageRefs := make(map[string]bool, len(state.Units))
	for _, unit := range state.Units {
		ref := unit.Container.ImageName + ":" + unit.Container.ImageTag
		imageRefs[ref] = true
	}

	results := make(chan error, len(imageRefs))
	for ref, _ := range imageRefs {
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

func (s Session) Close() error {
	s.conn.Close()
	return s.cli.Close()
}
