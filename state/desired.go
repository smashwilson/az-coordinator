package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	log "github.com/sirupsen/logrus"
)

// DesiredState describes the target state of the system based on the contents of the coordinator database.
type DesiredState struct {
	Units []DesiredSystemdUnit `json:"units"`
	Files map[string][]byte    `json:"-"`
}

// DesiredDockerContainer contains information about the Docker container image to be used by a SystemD unit.
type DesiredDockerContainer struct {
	Name      string `json:"name"`
	ImageID   string `json:"image_id"`
	ImageName string `json:"image_name"`
	ImageTag  string `json:"image_tag"`
}

// DesiredSystemdUnit contains information about a SystemD unit managed by the coordinator.
type DesiredSystemdUnit struct {
	ID        *int                   `json:"id,omitempty"`
	Path      string                 `json:"path"`
	Type      int                    `json:"type"`
	Container DesiredDockerContainer `json:"container"`
	Secrets   []string               `json:"secrets"`
	Env       map[string]string      `json:"env"`
	Ports     map[int]int            `json:"ports"`
	Volumes   map[string]string      `json:"volumes"`
	Schedule  string                 `json:"calendar,omitempty"`
}

// ReadDesiredState queries the database for the currently configured desired system state. DesiredDockerContainers
// within the returned state will have no ImageID.
func (session Session) ReadDesiredState() (*DesiredState, error) {
	var (
		db      = session.db
		secrets = session.secrets
	)

	unitRows, err := db.Query(`
    SELECT
      id, path, type,
      container_name, container_image_name, container_image_tag,
      secrets, env, ports, volumes,
      schedule
		FROM state_systemd_units
  `)
	if err != nil {
		return nil, err
	}
	defer unitRows.Close()

	units := make([]DesiredSystemdUnit, 0, 10)
	for unitRows.Next() {
		var (
			rawSecrets []byte
			rawEnv     []byte
			rawPorts   []byte
			rawVolumes []byte
		)

		unit := DesiredSystemdUnit{}
		if err = unitRows.Scan(
			&unit.ID, &unit.Path, &unit.Type,
			&unit.Container.Name, &unit.Container.ImageName, &unit.Container.ImageTag,
			&rawSecrets, &rawEnv, &rawPorts, &rawVolumes,
			&unit.Schedule,
		); err != nil {
			log.WithError(err).Warn("Unable to load state_systemd_units row.")
			continue
		}

		if err = json.Unmarshal(rawSecrets, &unit.Secrets); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed secrets column in state_systemd_units row")
			continue
		}

		if err = json.Unmarshal(rawEnv, &unit.Env); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed env column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawEnv)
			continue
		}

		if err = json.Unmarshal(rawPorts, &unit.Ports); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed ports column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawPorts)
			continue
		}

		if err = json.Unmarshal(rawVolumes, &unit.Volumes); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed volumes column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawVolumes)
			continue
		}

		units = append(units, unit)
	}

	files, err := secrets.DesiredTLSFiles()
	if err != nil {
		return nil, err
	}

	return &DesiredState{Units: units, Files: files}, nil
}

// ReadImages queries Docker for the most recently created container images corresponding to the image names and tags requested by
// each DesiredSystemdUnit. This call populates the ImageID of each DesiredDockerContainer.
func (state *DesiredState) ReadImages(session *Session) error {
	for _, unit := range state.Units {
		imageSummaries, err := session.cli.ImageList(context.Background(), types.ImageListOptions{
			Filters: filters.NewArgs(filters.Arg("reference", unit.Container.ImageName+":"+unit.Container.ImageTag)),
		})
		if err != nil {
			return err
		}

		var highest int64
		for _, imageSummary := range imageSummaries {
			if imageSummary.Created > highest {
				unit.Container.ImageID = imageSummary.ID
				highest = imageSummary.Created
			}
		}
	}

	return nil
}

// MakeDesired persists its caller within the database. Future calls to ReadDesiredState will include this unit
// in its output.
func (unit DesiredSystemdUnit) MakeDesired(session Session) error {
	if unit.ID != nil {
		return fmt.Errorf("Attempt to re-persist already persisted unit: %d", unit.ID)
	}

	var db = session.db

	rawSecrets, err := json.Marshal(unit.Secrets)
	if err != nil {
		return err
	}

	rawEnv, err := json.Marshal(unit.Env)
	if err != nil {
		return err
	}

	rawPorts, err := json.Marshal(unit.Ports)
	if err != nil {
		return err
	}

	rawVolumes, err := json.Marshal(unit.Volumes)
	if err != nil {
		return err
	}

	createdRow := db.QueryRow(`
    INSERT INTO state_systemd_units
      (path, type,
        container_name, container_image_name, container_image_tag,
        secrets, env, ports, volumes,
        schedule)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	RETURNING id
  `,
		unit.Path, unit.Type,
		unit.Container.Name, unit.Container.ImageName, unit.Container.ImageTag,
		rawSecrets, rawEnv, rawPorts, rawVolumes,
		unit.Schedule,
	)

	return createdRow.Scan(&unit.ID)
}

// Update modifies an existing unit to match its in-memory representation.
func (unit DesiredSystemdUnit) Update(session Session) error {
	if unit.ID == nil {
		return errors.New("Attempt to update an un-persisted desired unit")
	}

	var db = session.db

	rawSecrets, err := json.Marshal(unit.Secrets)
	if err != nil {
		return err
	}

	rawEnv, err := json.Marshal(unit.Env)
	if err != nil {
		return err
	}

	rawPorts, err := json.Marshal(unit.Ports)
	if err != nil {
		return err
	}

	rawVolumes, err := json.Marshal(unit.Volumes)
	if err != nil {
		return err
	}

	return db.QueryRow(`
	UPDATE state_systemd_units
	SET
		path = $1, type = $2,
		container_name = $3, container_image_name = $4, container_image_tag = $5,
		secrets = $6, env = $7, ports = $8, volumes = $9,
		schedule = $10
	WHERE id = $11
	`,
		unit.Path, unit.Type,
		unit.Container.Name, unit.Container.ImageName, unit.Container.ImageTag,
		rawSecrets, rawEnv, rawPorts, rawVolumes,
		unit.Schedule,
	).Scan()
}

// Undesired requests that a unit should no longer be present on the system by removing it from the database.
func (unit DesiredSystemdUnit) Undesired(session Session) error {
	if unit.ID == nil {
		return nil
	}

	var db = session.db

	return db.QueryRow(`
		DELETE FROM state_systemd_units WHERE id = $1
	`, unit.ID).Scan()
}

// UnitName derives the SystemD logical unit name from the path of its source on disk.
func (unit DesiredSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
