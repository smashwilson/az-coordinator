package state

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// UpdatedContainer captures information about a container image that has been modified.
type UpdatedContainer DesiredDockerContainer

// RepositoryURL generates a URL to the GitHub repository that created this container.
func (c UpdatedContainer) RepositoryURL() string {
	return fmt.Sprintf("https://github.com/%s", c.Repository)
}

// CommitURL generates a permalink to the git commit on GitHub.
func (c UpdatedContainer) CommitURL() string {
	return fmt.Sprintf("https://github.com/%s/commit/%s", c.Repository, c.GitOID)
}

// BranchURL generates a link to the git branch on GitHub.
func (c UpdatedContainer) BranchURL() string {
	return fmt.Sprintf("https://github.com/%s/tree/%s", c.Repository, c.GitRef)
}

// PullRequestURL generates a link to the open pull request (if any).
func (c UpdatedContainer) PullRequestURL() string {
	return fmt.Sprintf("https://github.com/%s/pull/%s", c.Repository, c.GitRef)
}

// Delta is a JSON-serializable structure enumerating the changes necessary to bring the actual system state
// in alignment with the desired state.
type Delta struct {
	UnitsToAdd     []DesiredSystemdUnit `json:"units_to_add"`
	UnitsToChange  []DesiredSystemdUnit `json:"units_to_change"`
	UnitsToRestart []DesiredSystemdUnit `json:"units_to_restart"`
	UnitsToRemove  []ActualSystemdUnit  `json:"units_to_remove"`
	FilesToWrite   []string             `json:"files_to_write"`

	UpdatedContainers []UpdatedContainer `json:"-"`

	fileContent map[string][]byte
}

// Between compares desired and actual system state and produces a Delta necessary to convert the observed actual
// state to the desired state.
func (session *SessionLease) Between(desired *DesiredState, actual *ActualState) Delta {
	var (
		log = session.Log

		unitsToAdd     = make([]DesiredSystemdUnit, 0)
		unitsToChange  = make([]DesiredSystemdUnit, 0)
		unitsToRestart = make([]DesiredSystemdUnit, 0)
		unitsToRemove  = make([]ActualSystemdUnit, 0)
		filesToWrite   = make([]string, 0, len(desired.Files))

		updatedContainers = make([]UpdatedContainer, 0)

		fileContentByPath = make(map[string][]byte, len(desired.Files))
		desiredByName     = make(map[string]DesiredSystemdUnit)
		desiredRemaining  = make(map[string]bool)
	)

	for filePath, desiredContent := range desired.Files {
		log.WithField("filePath", filePath).Debug("Verifying expected file.")
		actualContent, ok := actual.Files[filePath]
		if !ok || !bytes.Equal(desiredContent, actualContent) {
			filesToWrite = append(filesToWrite, filePath)
			fileContentByPath[filePath] = desiredContent
			log.WithField("filePath", filePath).Debug("File was absent or different.")
		} else {
			log.WithField("filePath", filePath).Debug("Nothing to do.")
		}
	}

	for _, unit := range desired.Units {
		desiredByName[unit.UnitName()] = unit
		desiredRemaining[unit.UnitName()] = true
	}

	for _, actual := range actual.Units {
		if desired, ok := desiredByName[actual.UnitName()]; ok {
			log.WithField("unitName", actual.UnitName()).Debug("Verifying systemd unit.")
			desiredRemaining[desired.UnitName()] = false

			willUpdate := false
			shouldRestart := false

			// Determine if the ID of the running Docker container image will change.
			if desired.Container != nil {
				if desired.Container.ImageID != actual.ImageID && len(desired.Container.ImageID) > 0 {
					willUpdate = true
					shouldRestart = true
					log.WithFields(logrus.Fields{
						"unitName":  actual.UnitName(),
						"actualID":  actual.ImageID,
						"desiredID": desired.Container.ImageID,
					}).Debug("Container image ID differs.")
				}
			}

			// Determine if the actual unit needs to be reloaded to match the desired one.
			var expected bytes.Buffer
			if errs := session.WriteUnit(desired, &expected); len(errs) > 0 {
				for _, err := range errs {
					log.WithError(err).WithField("unit", desired.UnitName()).Warn("Unable to render expected unit file contents.")
				}
			} else if !bytes.Equal(expected.Bytes(), actual.Content) {
				log.WithField("unitName", actual.UnitName()).Debug("Unit content differs.")
				willUpdate = true
				shouldRestart = true
			}

			// Schedule the unit for restart if a volume-mounted file is due to be modified.
			for hostPath := range desired.Volumes {
				if _, ok := fileContentByPath[hostPath]; ok {
					// A mounted file has been written. Restart the unit to pick it up.
					log.WithFields(logrus.Fields{
						"unitName":        actual.UnitName(),
						"mountedFilePath": hostPath,
					}).Debug("Mounted volume file has been changed.")
					shouldRestart = true
					break
				}
			}

			if willUpdate && desired.Container != nil {
				unitsToChange = append(unitsToChange, desired)
				updatedContainers = append(updatedContainers, UpdatedContainer(*desired.Container))
			} else if shouldRestart {
				unitsToRestart = append(unitsToRestart, desired)
			}

			// Otherwise: everything is fine, nothing to do.
			if !willUpdate && !shouldRestart {
				log.WithField("unitName", actual.UnitName()).Debug("Nothing to do.")
			}
		} else {
			// Unit is no longer desired.
			log.WithField("unitName", actual.UnitName()).Debug("Unit is no longer desired.")
			unitsToRemove = append(unitsToRemove, actual)
		}
	}

	// Create remaining units anew.
	for desiredName, remaining := range desiredRemaining {
		if remaining {
			if desired, ok := desiredByName[desiredName]; ok {
				log.WithField("unitName", desired.UnitName()).Debug("Unit is not yet present.")
				unitsToAdd = append(unitsToAdd, desired)
			}
		}
	}

	return Delta{
		UnitsToAdd:        unitsToAdd,
		UnitsToChange:     unitsToChange,
		UnitsToRestart:    unitsToRestart,
		UnitsToRemove:     unitsToRemove,
		FilesToWrite:      filesToWrite,
		UpdatedContainers: updatedContainers,
		fileContent:       fileContentByPath,
	}
}

// CoordinatorRestartNeeded returns true if this Delta will require the coordinator itself to restart.
func (d Delta) CoordinatorRestartNeeded(session *SessionLease) bool {
	bag, err := session.GetSecrets()
	if err != nil {
		session.Log.WithError(err).Warn("Unable to load secrets.")
		return false
	}

	for _, filePath := range d.FilesToWrite {
		if bag.IsTLSFile(filePath) {
			return true
		}
	}
	return false
}

// Apply enacts the changes described by a Delta on the system. Individual operations that fail append errors to
// the returned error slice, but do not prevent subsequent operations from being attempted.
func (d Delta) Apply(session *SessionLease, uid, gid int) []error {
	var (
		errs         = make([]error, 0)
		log          = session.Log
		needsReload  = false
		restartUnits = make([]string, 0, len(d.UnitsToChange)+len(d.UnitsToRestart))
	)

	for filePath, fileContent := range d.fileContent {
		dir := filepath.Dir(filePath)

		if err := os.MkdirAll(dir, 0750); err != nil {
			errs = append(errs, err)
			continue
		}

		if uid != -1 || gid != -1 {
			if err := os.Chown(dir, uid, gid); err != nil {
				errs = append(errs, err)
				continue
			}
			log.WithFields(logrus.Fields{
				"dirPath": dir,
				"uid":     uid,
				"gid":     gid,
			}).Info("Directory ownership modified.")
		}

		if err := ioutil.WriteFile(filePath, fileContent, 0600); err != nil {
			errs = append(errs, err)
			continue
		}
		log.WithField("filePath", filePath).Info("File content written.")

		if uid != -1 || gid != -1 {
			if err := os.Chown(filePath, uid, gid); err != nil {
				errs = append(errs, err)
				continue
			}
			log.WithFields(logrus.Fields{
				"filePath": filePath,
				"uid":      uid,
				"gid":      gid,
			}).Info("File ownership modified.")
		}
	}

	for _, unit := range d.UnitsToAdd {
		needsReload = true
		f, err := os.OpenFile(unit.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			errs = append(errs, fmt.Errorf("Unable to create unit file %s (%v)", unit.Path, err))
			continue
		}

		errs = append(errs, session.WriteUnit(unit, f)...)
		f.Close()

		log.WithFields(logrus.Fields{
			"unitName":     unit.UnitName(),
			"unitFilePath": unit.Path,
		}).Info("Unit file created.")

		if uid != -1 || gid != -1 {
			if err := os.Chown(unit.Path, uid, gid); err != nil {
				errs = append(errs, err)
				continue
			}

			log.WithFields(logrus.Fields{
				"unitFilePath": unit.Path,
				"uid":          uid,
				"gid":          gid,
			}).Info("Unit file ownership modified.")
		}
	}

	for _, unit := range d.UnitsToChange {
		needsReload = true
		restartUnits = append(restartUnits, unit.UnitName())

		f, err := os.OpenFile(unit.Path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			errs = append(errs, fmt.Errorf("Unable to overwrite unit file %s (%v)", unit.Path, err))
			continue
		}

		errs = append(errs, session.WriteUnit(unit, f)...)
		f.Close()

		log.WithFields(logrus.Fields{
			"unitName":     unit.UnitName(),
			"unitFilePath": unit.Path,
		}).Info("Unit file modified.")

		if uid != -1 || gid != -1 {
			if err := os.Chown(unit.Path, uid, gid); err != nil {
				errs = append(errs, err)
				continue
			}

			log.WithFields(logrus.Fields{
				"unitFilePath": unit.Path,
				"uid":          uid,
				"gid":          gid,
			}).Info("Unit file ownership modified.")
		}
	}

	for _, unit := range d.UnitsToRestart {
		restartUnits = append(restartUnits, unit.UnitName())
	}

	// Stop and disable unit files we intend to remove.
	if len(d.UnitsToRemove) > 0 {
		stops := make(chan string, len(d.UnitsToRemove))
		disableUnitNames := make([]string, 0, len(d.UnitsToRemove))
		for _, unit := range d.UnitsToRemove {
			disableUnitNames = append(disableUnitNames, unit.UnitName())

			log.WithField("unitName", unit.UnitName()).Debug("Stopping unit.")
			if _, err := session.conn.StopUnit(unit.UnitName(), "replace", stops); err != nil {
				errs = append(errs, fmt.Errorf("Unable to stop unit %s (%v)", unit.UnitName(), err))
				stops <- ""

				log.WithField("unitName", unit.UnitName()).Info("Killing unit.")
				session.conn.KillUnit(unit.Path, 9)
				log.WithField("unitName", unit.UnitName()).Info("Unit killed.")
			}
		}
		for i := 0; i < len(d.UnitsToRemove); i++ {
			<-stops
		}
		log.WithField("count", len(d.UnitsToRemove)).Debug("Units stopped or killed.")

		log.WithField("unitPaths", disableUnitNames).Debug("Disabling units.")
		if _, err := session.conn.DisableUnitFiles(disableUnitNames, false); err != nil {
			errs = append(errs, fmt.Errorf("Unable to disable units %v (%v)", disableUnitNames, err))
		}
		log.WithField("count", len(disableUnitNames)).Debug("Units disabled.")
	} else {
		log.Debug("No units to remove.")
	}

	// Reload to pick up any rewritten unit files.
	if needsReload {
		log.Debug("Reloading systemd unit files.")
		if err := session.conn.Reload(); err != nil {
			errs = append(errs, fmt.Errorf("Unable to trigger a systemd reload (%v)", err))
			return errs
		}
		log.Debug("Reloaded successfully.")
	}

	// Start and enable newly created units.
	if len(d.UnitsToAdd) > 0 {
		log.WithField("count", len(d.UnitsToAdd)).Debug("Starting and enabling units.")

		starts := make(chan string, len(d.UnitsToAdd))
		enablePaths := make([]string, 0, len(d.UnitsToAdd))
		for _, unit := range d.UnitsToAdd {
			enablePaths = append(enablePaths, unit.Path)
			log.WithField("unitName", unit.UnitName()).Debug("Starting unit.")
			if _, err := session.conn.StartUnit(unit.UnitName(), "replace", starts); err != nil {
				errs = append(errs, fmt.Errorf("Unable to start unit %s (%v)", unit.UnitName(), err))
				starts <- ""
			}
		}
		for i := 0; i < len(d.UnitsToAdd); i++ {
			<-starts
		}
		log.WithField("count", len(d.UnitsToAdd)).Info("Units started.")

		log.WithField("count", len(enablePaths)).Info("Enabling units.")
		if _, _, err := session.conn.EnableUnitFiles(enablePaths, false, true); err != nil {
			errs = append(errs, fmt.Errorf("Unable to enable units %v (%v)", enablePaths, err))
		}
		log.WithField("count", len(enablePaths)).Debug("Units enabled.")
	} else {
		log.Debug("No units to start and enable.")
	}

	// Restart changed units and units whose containers have been updated.
	if len(restartUnits) > 0 {
		log.WithField("count", len(restartUnits)).Debug("Restarting units.")

		restarts := make(chan string, len(restartUnits))
		for _, unitName := range restartUnits {
			log.WithField("unitName", unitName).Debug("Restarting unit.")
			if _, err := session.conn.RestartUnit(unitName, "replace", restarts); err != nil {
				errs = append(errs, fmt.Errorf("Unable to restart unit %s (%v)", unitName, err))
				restarts <- ""
			}
		}

		for i := 0; i < len(restartUnits); i++ {
			<-restarts
		}
		log.WithField("count", len(restartUnits)).Info("Units restarted.")
	} else {
		log.Debug("No units to restart.")
	}

	if len(d.UnitsToRemove) > 0 {
		log.WithField("count", len(d.UnitsToRemove)).Debug("Removing unit files.")
		for _, unit := range d.UnitsToRemove {
			log.WithField("unitFilePath", unit.Path).Debug("Removing unit file.")
			if err := os.Remove(unit.Path); err != nil {
				errs = append(errs, fmt.Errorf("Unable to remove unit source for %s (%v)", unit.Path, err))
			}
			log.WithField("unitFilePath", unit.Path).Info("Removed unit file.")
		}
	} else {
		log.Debug("No unit files to remove.")
	}

	if d.CoordinatorRestartNeeded(session) {
		log.Info("Restarting coordinator.")
		os.Exit(0)
	}

	return errs
}

func (d Delta) String() string {
	b := strings.Builder{}

	writeDesiredUnit := func(u DesiredSystemdUnit) {
		b.WriteString(u.Path)
		if u.Container != nil && len(u.Container.ImageName) > 0 && len(u.Container.ImageTag) > 0 {
			fmt.Fprintf(&b, " container=(%s:%s)", u.Container.ImageName, u.Container.ImageTag)
		}
		b.WriteString("\n")
	}

	writeActualUnit := func(u ActualSystemdUnit) {
		fmt.Fprintf(&b, "%s contentlen=%d\n", u.Path, len(u.Content))
	}

	for _, u := range d.UnitsToAdd {
		b.WriteString("add unit: ")
		writeDesiredUnit(u)
	}

	for _, u := range d.UnitsToChange {
		b.WriteString("change unit: ")
		writeDesiredUnit(u)
	}

	for _, u := range d.UnitsToRemove {
		b.WriteString("remove unit: ")
		writeActualUnit(u)
	}

	for _, f := range d.FilesToWrite {
		fmt.Fprintf(&b, "write file: %s contentlen=%d\n", f, len(d.fileContent[f]))
	}

	return b.String()
}
