package state

import (
	"context"
	"io/ioutil"
	"path"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
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
	// Path is the path to the source of this unit on disk.
	Path string `json:"path"`

	// ImageID is the ID of the currently running Docker image.
	ImageID string `json:"image_id"`

	// Content is the current content of the unit file on disk.
	Content []byte `json:"-"`
}

// ReadActualState introspects SystemD and the filesystem to construct an ActualState instance that captures a
// snapshot of the aspects of the host state that we care about managing.
func (session Session) ReadActualState() (*ActualState, error) {
	var (
		conn    = session.conn
		secrets = session.secrets
		log     = session.Log
	)

	listedUnits, err := conn.ListUnitFilesByPatterns(nil, []string{"az*"})
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

// ReadImages loads ImageIDs where possible by querying pre-pulled Docker images.
func (state *ActualState) ReadImages(session *Session, desired DesiredState) []error {
	var (
		desiredByName = make(map[string]DesiredSystemdUnit)
		errs          = make([]error, 0)
	)

	for _, unit := range desired.Units {
		desiredByName[unit.UnitName()] = unit
	}

	for i := range state.Units {
		actual := &state.Units[i]
		if desired, ok := desiredByName[actual.UnitName()]; ok {
			if desired.Container == nil {
				continue
			}

			if len(desired.Container.Name) > 0 {
				// Load the image ID associated with a running container.
				container, err := session.cli.ContainerInspect(context.Background(), desired.Container.Name)
				if client.IsErrNotFound(err) {
					// The container isn't running. Fall back to an image query, because that's the image that will be used
					// the next time this container starts anyway.
				} else if err != nil {
					errs = append(errs, err)
					continue
				} else {
					actual.ImageID = container.Image
					continue
				}
			}

			imageSummaries, err := session.cli.ImageList(context.Background(), types.ImageListOptions{
				Filters: filters.NewArgs(filters.Arg("reference", desired.Container.ImageName+":"+desired.Container.ImageTag)),
			})
			if err != nil {
				errs = append(errs, err)
				continue
			}

			var highest int64
			for _, imageSummary := range imageSummaries {
				if imageSummary.Created > highest {
					actual.ImageID = imageSummary.ID
					highest = imageSummary.Created
				}
			}
		}
	}

	return errs
}

// UnitName derives the internal name that SystemD uses for a unit from the path to its source file.
func (unit ActualSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
