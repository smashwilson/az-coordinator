package state

import (
	"io/ioutil"
	"path"

	log "github.com/sirupsen/logrus"
)

type ActualState struct {
	Units []ActualSystemdUnit `json:"units"`
}

type ActualSystemdUnit struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

func (session Session) ReadActualState() (*ActualState, error) {
	listedUnits, err := session.conn.ListUnitFilesByPatterns(
		[]string{"inactive", "deactivating", "failed", "error", "active", "reloading", "activating"},
		[]string{"az-*"},
	)
	if err != nil {
		return nil, err
	}

	units := make([]ActualSystemdUnit, len(listedUnits))
	for _, listedUnit := range listedUnits {
		content, err := ioutil.ReadFile(listedUnit.Path)
		if err != nil {
			log.WithError(err).WithField("path", listedUnit.Path).Warn("Unable to read unit file contents.")
			content = nil
		}

		units = append(units, ActualSystemdUnit{
			Path:    listedUnit.Path,
			Content: content,
		})
	}

	return &ActualState{Units: units}, nil
}

func (unit ActualSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
