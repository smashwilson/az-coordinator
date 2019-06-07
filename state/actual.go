package state

import (
	"io/ioutil"
	"path"

	log "github.com/sirupsen/logrus"
)

type ActualState struct {
	Units []ActualSystemdUnit `json:"units"`
	Files map[string][]byte   `json:"-"`
}

type ActualSystemdUnit struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

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

func (unit ActualSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
