package state

import (
	"database/sql"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/secrets"
)

// Session centralizes all of the resources necessary for a single request or operation.
type Session struct {
	db   *sql.DB
	ring *secrets.DecoderRing
	cli  *client.Client
	conn *dbus.Conn
}

// NewSession establishes all of the connections necessary to perform an operation.
func NewSession(db *sql.DB, ring *secrets.DecoderRing, dockerAPIVersion string) (*Session, error) {
	log := logrus.StandardLogger()

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

	return &Session{
		db:   db,
		ring: ring,
		cli:  cli,
		conn: conn,
	}, nil
}

// Close disposes of any connection resources acquired by NewSession.
func (s Session) Close() error {
	s.conn.Close()
	return s.cli.Close()
}
