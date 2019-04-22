package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	log "github.com/sirupsen/logrus"
)

type DesiredState struct {
	Units []DesiredSystemdUnit `json:"units"`
}

type DesiredDockerContainer struct {
	Name      string `json:"name"`
	ImageID   string `json:"image_id"`
	ImageName string `json:"image_name"`
	ImageTag  string `json:"image_tag"`
}

type DesiredSystemdUnit struct {
	Path      string                 `json:"path"`
	Type      int                    `json:"type"`
	Container DesiredDockerContainer `json:"container"`
	Secrets   []string               `json:"secrets"`
	Env       map[string]string      `json:"env"`
	Ports     map[int]int            `json:"ports"`
	Schedule  string                 `json:"calendar,omitempty"`
}

func (session Session) ReadDesiredState() (*DesiredState, error) {
	var db = session.db

	unitRows, err := db.Query(`
    SELECT path, type, container_name, container_image_name, container_image_tag, secrets, env, ports, schedule
		FROM state_systemd_units
  `)
	if err != nil {
		return nil, err
	}
	defer unitRows.Close()

	units := make([]DesiredSystemdUnit, 10)
	for unitRows.Next() {
		var (
			rawSecrets []byte
			rawEnv     []byte
			rawPorts   []byte
		)

		unit := DesiredSystemdUnit{}
		if err = unitRows.Scan(
			&unit.Path, &unit.Type, &unit.Container.Name, &unit.Container.ImageName, &unit.Container.ImageTag,
			&rawSecrets, &rawEnv, &rawPorts, &unit.Schedule,
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

		units = append(units, unit)
	}

	return &DesiredState{Units: units}, nil
}

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

func (unit DesiredSystemdUnit) MakeDesired(session Session) error {
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

	err = db.QueryRow(`
    INSERT INTO state_systemd_units
      (path, type, container_name, container_image_name, container_image_tag, secrets, env, ports, schedule)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
    ON CONFLICT DO UPDATE SET
      type = EXCLUDED.type, container_image = EXCLUDED.container_image, container_tag = EXCLUDED.container_tag,
      secrets = EXCLUDED.secrets, env = EXCLUDED.env, ports = EXCLUDED.ports, schedule = EXCLUDED.schedule
  `,
		unit.Path, unit.Type, unit.Container.Name, unit.Container.ImageName, unit.Container.ImageTag,
		rawSecrets, rawEnv, rawPorts, unit.Schedule,
	).Scan()
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func (unit DesiredSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}
