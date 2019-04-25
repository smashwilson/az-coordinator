package state

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
)

type Delta struct {
	UnitsToAdd     []DesiredSystemdUnit `json:"units_to_add"`
	UnitsToChange  []DesiredSystemdUnit `json:"units_to_change"`
	UnitsToRestart []DesiredSystemdUnit `json:"units_to_restart"`
	UnitsToRemove  []ActualSystemdUnit  `json:"units_to_remove"`

	session *Session
}

func (session *Session) Between(desired *DesiredState, actual *ActualState) Delta {
	var (
		unitsToAdd     = make([]DesiredSystemdUnit, 0)
		unitsToChange  = make([]DesiredSystemdUnit, 0)
		unitsToRestart = make([]DesiredSystemdUnit, 0)
		unitsToRemove  = make([]ActualSystemdUnit, 0)

		desiredByName    = make(map[string]DesiredSystemdUnit)
		desiredRemaining = make(map[string]bool)
	)

	for _, unit := range desired.Units {
		desiredByName[unit.UnitName()] = unit
		desiredRemaining[unit.UnitName()] = true
	}

	for _, actual := range actual.Units {
		if desired, ok := desiredByName[actual.UnitName()]; ok {
			desiredRemaining[desired.UnitName()] = false

			// Determine if the actual unit needs to be reloaded to match the desired one.

			// Check unit file content first.
			var expected bytes.Buffer
			if errs := session.WriteUnit(desired, &expected); len(errs) > 0 {
				for _, err := range errs {
					log.WithError(err).WithField("unit", desired.UnitName()).Warn("Unable to render expected unit file contents.")
				}
				continue
			}
			if !bytes.Equal(expected.Bytes(), actual.Content) {
				unitsToChange = append(unitsToChange, desired)
				continue
			}

			if desired.Container.Name == "" {
				// If the desired unit doesn't specify a container name, then the running container will have an automatically
				// assigned one. Usually this means it's a one-shot.
				continue
			}

			// Check the image ID associated with a running container next.
			container, err := session.cli.ContainerInspect(context.Background(), desired.Container.Name)
			if err != nil {
				if client.IsErrNotFound(err) {
					// It's not running. Definitely restart the thing.
					unitsToRestart = append(unitsToRestart, desired)
					continue
				}
				log.WithError(err).WithField("containerName", desired.Container.Name).Warn("Unable to inspect container.")
				continue
			}

			if container.Image != desired.Container.ImageID {
				// A newer image has been pulled. Restart the unit to pick it up.
				unitsToRestart = append(unitsToRestart, desired)
			}

			// Otherwise: everything is fine, nothing to do.
		} else {
			// Unit is no longer desired.
			unitsToRemove = append(unitsToRemove, actual)
		}
	}

	// Create remaining units anew.
	for desiredName, remaining := range desiredRemaining {
		if remaining {
			if desired, ok := desiredByName[desiredName]; ok {
				unitsToAdd = append(unitsToAdd, desired)
			}
		}
	}

	return Delta{
		UnitsToAdd:     unitsToAdd,
		UnitsToChange:  unitsToChange,
		UnitsToRestart: unitsToRestart,
		UnitsToRemove:  unitsToRemove,
		session:        session,
	}
}

func (d Delta) Apply() []error {
	var (
		errs         = make([]error, 0)
		session      = d.session
		needsReload  = false
		restartUnits = make([]string, len(d.UnitsToChange)+len(d.UnitsToRestart))
	)

	for _, unit := range d.UnitsToAdd {
		f, err := os.OpenFile(unit.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			errs = append(errs, fmt.Errorf("Unable to create unit file %s (%v)", unit.Path, err))
			continue
		}

		errs = append(errs, session.WriteUnit(unit, f)...)
		f.Close()
	}

	for _, unit := range d.UnitsToChange {
		needsReload = true
		restartUnits = append(restartUnits, unit.UnitName())

		f, err := os.OpenFile(unit.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			errs = append(errs, fmt.Errorf("Unable to overwrite unit file %s (%v)", unit.Path, err))
			continue
		}

		errs = append(errs, d.session.WriteUnit(unit, f)...)
		f.Close()
	}

	for _, unit := range d.UnitsToRestart {
		restartUnits = append(restartUnits, unit.UnitName())
	}

	// Stop and disable unit files we intend to remove.
	stops := make(chan string, len(d.UnitsToRemove))
	disablePaths := make([]string, len(d.UnitsToRemove))
	for _, unit := range d.UnitsToRemove {
		disablePaths = append(disablePaths, unit.Path)

		if _, err := session.conn.StopUnit(unit.UnitName(), "replace", stops); err != nil {
			errs = append(errs, fmt.Errorf("Unable to stop unit %s (%v)", unit.UnitName(), err))
			session.conn.KillUnit(unit.Path, 9)
		}
	}
	for i := 0; i < len(d.UnitsToRemove); i++ {
		<-stops
	}
	if _, err := session.conn.DisableUnitFiles(disablePaths, false); err != nil {
		errs = append(errs, fmt.Errorf("Unable to disable units %v (%v)", disablePaths, err))
	}

	// Reload to pick up any rewritten unit files.
	if needsReload {
		if err := session.conn.Reload(); err != nil {
			errs = append(errs, err)
			return errs
		}
	}

	// Start and enable newly created units.
	starts := make(chan string, len(d.UnitsToAdd))
	enablePaths := make([]string, len(d.UnitsToAdd))
	for _, unit := range d.UnitsToAdd {
		enablePaths = append(enablePaths, unit.Path)
		if _, err := session.conn.StartUnit(unit.UnitName(), "replace", starts); err != nil {
			errs = append(errs, fmt.Errorf("Unable to start unit %s (%v)", unit.UnitName(), err))
		}
	}
	for i := 0; i < len(d.UnitsToAdd); i++ {
		<-starts
	}
	session.conn.EnableUnitFiles(enablePaths, false, true)

	// Restart changed units and units whose containers have been updated.
	restarts := make(chan string, len(restartUnits))
	for _, unitName := range restartUnits {
		if _, err := session.conn.RestartUnit(unitName, "replace", restarts); err != nil {
			errs = append(errs, fmt.Errorf("Unable to restart unit %s (%v)", unitName, err))
		}
	}

	for _, unit := range d.UnitsToRemove {
		if err := os.Remove(unit.Path); err != nil {
			errs = append(errs, fmt.Errorf("Unable to remove unit source for %s (%v)", unit.Path, err))
		}
	}

	return errs
}