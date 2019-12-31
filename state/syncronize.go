package state

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

// SyncSettings configures synchronization behavior.
type SyncSettings struct {
	UID int
	GID int
}

var dfPercentRx = regexp.MustCompile(`(\d+)%`)

// ReadDiskUsage reads the current usage level of the disk partition that stores Docker images and returns it as a
// percentage.
func (s SessionLease) ReadDiskUsage() (int, error) {
	out, err := exec.Command("df", "/var/lib/docker").Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			s.Log.WithField("exitCode", exitError.ExitCode()).Warnf("df command exited abnormally:\n%s\n", exitError.Stderr)
		}
		return 0, err
	}
	s.Log.Debugf("df /var/lib/docker:\n%s\n", out)

	matches := dfPercentRx.FindAllSubmatch(out, 2)
	if matches == nil {
		return 0, fmt.Errorf("Unable to parse partition use percentage from df output: %s", out)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("Found multiple percentages in df output: %s", out)
	}
	match := matches[0][1]
	i64, err := strconv.ParseInt(string(match), 10, 32)
	return int(i64), err
}

// Synchronize brings local Docker images up to date, then reads desired and actual state, computes a
// Delta between them, and applies it. The applied Delta is returned.
func (s *SessionLease) Synchronize(settings SyncSettings) (*Delta, []error) {
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

	s.Log.Info("Reading actual state.")
	actual, err := s.ReadActualState()
	if err != nil {
		return nil, []error{err, errors.New("unable to read system state")}
	}

	s.Log.Info("Reading original docker images.")
	if errs := actual.ReadImages(s, *desired); len(errs) > 0 {
		return nil, append(errs, errors.New("unable to read original images"))
	}

	s.Log.Info("Pulling referenced images.")
	if errs := s.PullAllImages(*desired); len(errs) > 0 {
		return nil, append(errs, errors.New("pull errors"))
	}

	s.Log.Info("Reading updated docker images.")
	if err = desired.ReadImages(s); err != nil {
		return nil, []error{err, errors.New("unable to pull docker images")}
	}

	s.Log.Info("Computing delta.")
	delta := s.Between(desired, actual)

	if errs := delta.Apply(s, uid, gid); len(errs) > 0 {
		return nil, append(errs, errors.New("unable to apply delta"))
	}

	usage, err := s.ReadDiskUsage()
	if err != nil {
		s.Log.WithError(err).Warn("Unable to read disk usage")
	} else if usage >= 70 {
    s.Log.WithField("usage", usage).Warn("Disk is getting full: prune advised.")
		// s.Log.Info("Pruning unused docker data.")
		// s.Prune()
	} else {
		s.Log.WithField("usage", usage).Info("No prune necessary yet.")
	}

	return &delta, nil
}
