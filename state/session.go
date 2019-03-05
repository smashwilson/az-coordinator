package state

import (
	"context"
	"io/ioutil"
	"log"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type Session struct {
	cli         *client.Client
	conn        *dbus.Conn
	needsReload bool
}

func NewSession() (*Session, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	conn, err := dbus.NewSystemConnection()
	if err != nil {
		return nil, err
	}

	return &Session{cli: cli, conn: conn, needsReload: false}, nil
}

func (s Session) PullImage(image DesiredDockerImage) error {
	ref := image.Name + ":" + image.Tag + "@" + image.Digest
	progress, err := s.cli.ImagePull(context.Background(), ref, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer progress.Close()

	payload, err := ioutil.ReadAll(progress)
	if err != nil {
		return err
	}
	log.Printf("ImagePull payload:\n%s\n---\n", payload)

	return nil
}

func (s Session) RemoveImage(image ActualDockerImage) error {
	_, err := s.cli.ImageRemove(context.Background(), image.ID, types.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *Session) CreateUnit(unit DesiredSystemdUnit) error {
	return nil
}

func (s *Session) ModifyUnit(unit DesiredSystemdUnit) error {
	return nil
}

func (s *Session) DeleteUnit(unit ActualSystemdUnit) error {
	return nil
}

func (s Session) Close() error {
	s.conn.Close()
	return s.cli.Close()
}
