package state

import (
	"io/ioutil"
	"path"

	log "github.com/sirupsen/logrus"
)

// ActualState represents a view of SystemD units and files presently on the host as of the time ReadActualState() is called.
type ActualState struct {
	// Units is a list of ActualSystemdUnits that are loaded and active.
	Units []ActualSystemdUnit `json:"units"`

	// Files is a map of paths and content of files that are currently on the filesystem.
	Files map[string][]byte `json:"-"`
}

// ActualSystemdUnit is information about a SystemD unit that is currently loaded on this host.
type ActualSystemdUnit struct {
	// Name is the name of the unit as it's known to SystemD, like "docker.service".
	Name string `json:"name"`

	// Path is the path to the source of this unit on disk.
	Path string `json:"path"`

	// Content is the current content of the unit file on disk.
	Content []byte `json:"content"`
}

// ReadActualState introspects SystemD and the filesystem to construct an ActualState instance that captures a
// snapshot of the aspects of the host state that we care about managing.
func (session Session) ReadActualState() (*ActualState, error) {
	var (
		conn    = session.conn
		secrets = session.secrets
	)

	listedUnits, err := conn.ListUnitFilesByPatterns(
		[]string{"inactive", "deactivating", "failed", "error", "active", "reloading", "activating"},
		[]string{"az-*"},
	)
	if err != nil {
		return nil, err
	}

	units := make([]ActualSystemdUnit, 0, len(listedUnits))
	for _, listedUnit := range listedUnits {
		content, readErr := ioutil.ReadFile(listedUnit.Path)
		if readErr != nil {
			log.WithError(readErr).WithField("path", listedUnit.Path).Warn("Unable to read unit file contents.")
			content = nil
		}

		units = append(units, ActualSystemdUnit{
			Path:    listedUnit.Path,
			Content: content,
		})
	}

	files, err := secrets.ActualTLSFiles()
	if err != nil {
		return nil, err
	}

	return &ActualState{Units: units, Files: files}, nil
}

// UnitName derives the internal name that SystemD uses for a unit from the path to its source file.
func (unit ActualSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
