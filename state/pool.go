package state

import (
	"sync"

	"github.com/smashwilson/az-coordinator/secrets"

	"github.com/sirupsen/logrus"
)

// SessionLease wraps a Session temporarily acquired from a Pool. Call Release() when done.
type SessionLease struct {
	*Session

	pool    *Pool
	secrets *secrets.Bag
	Log     *logrus.Logger
}

// Lease creates a stand-alone session that is separate from any Pool. It will be closed when released.
func (session *Session) Lease() *SessionLease {
	return &SessionLease{
		Session: session,
		pool:    nil,
		secrets: nil,
		Log:     logrus.StandardLogger(),
	}
}

type poolEntry struct {
	session *Session
	used    bool
}

// Pool maintains a burstable pool of pre-connected Sessions.
type Pool struct {
	creator   func() (*Session, error)
	lock      sync.Mutex
	available []*poolEntry

	low int
}

// NewPool creates and pre-allocates a pool of a given size.
func NewPool(creator func() (*Session, error), low int) (*Pool, error) {
	available := make([]*poolEntry, 0, low*2)
	for i := 0; i < low; i++ {
		session, err := creator()
		if err != nil {
			return nil, err
		}
		available = append(available, &poolEntry{session: session, used: false})
	}

	return &Pool{
		creator:   creator,
		low:       low,
		available: available,
	}, nil
}

// Take allocates and returns a session from the pool if one is already available and not in use. Otherwise, it
// attempts to allocate a new session and place it in the pool.
func (pool *Pool) Take() (*SessionLease, error) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	for _, entry := range pool.available {
		if !entry.used {
			entry.used = true
			return &SessionLease{Session: entry.session, pool: pool, Log: logrus.StandardLogger()}, nil
		}
	}

	logrus.WithField("pool size", len(pool.available)).Info("Allocating additional session.")
	overage, err := pool.creator()
	if err != nil {
		return nil, err
	}

	pool.available = append(pool.available, &poolEntry{session: overage, used: true})
	return &SessionLease{Session: overage, pool: pool, Log: logrus.StandardLogger()}, nil
}

// Return returns a session borrowed from the pool with Take.
func (pool *Pool) Return(session *Session) {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	keep := make([]*poolEntry, 0, pool.low)
	closed := 0

	for _, entry := range pool.available {
		if entry.session == session {
			entry.used = false
		}

		if len(keep) <= pool.low || entry.used {
			keep = append(keep, entry)
		} else if !entry.used {
			if err := entry.session.Close(); err != nil {
				logrus.WithError(err).Warn("Unable to close session.")
			}
			closed++
		}
	}

	pool.available = keep

	if closed > 0 {
		logrus.WithFields(logrus.Fields{
			"pool size": len(keep),
			"closed":    closed,
		}).Info("Unused overage sessions closed.")
	}
}

// WithLogger uses a non-standard logger for any log messages emitted through this session for the duration of its
// current lease.
func (lease *SessionLease) WithLogger(logger *logrus.Logger) *SessionLease {
	lease.Log = logger
	return lease
}

// GetSecrets returns the secrets Bag that's cached for the duration of this lease, loading them from the database if
// necessary.
func (lease *SessionLease) GetSecrets() (*secrets.Bag, error) {
	if lease.secrets != nil {
		return lease.secrets, nil
	}

	bag, err := secrets.LoadFromDatabase(lease.db, lease.ring)
	if err != nil {
		return nil, err
	}

	lease.secrets = bag
	return bag, err
}

// Release resets a session to its original state and returns it to the pool to make it available for other callers.
func (lease *SessionLease) Release() {
	if lease.pool != nil {
		lease.pool.Return(lease.Session)
	} else {
		lease.Session.Close()
	}
}
