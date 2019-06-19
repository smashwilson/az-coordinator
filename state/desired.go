package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	log "github.com/sirupsen/logrus"
)

// UnitType is an enumeration used to choose which template should be used to create a DesiredSystemdUnit's unit
// file.
type UnitType int

const (
	// TypeSimple units manage a persistent Docker container as a daemon.
	TypeSimple UnitType = iota

	// TypeTimer units fire another unit on a schedule.
	TypeTimer

	// TypeOneShot units execute a container and expect it to terminate in an order fashion.
	TypeOneShot

	// TypeSelf is the special unit used to managed the az-coordinator binary itself.
	TypeSelf
)

var typesByName = map[string]UnitType{
	"simple":  TypeSimple,
	"oneshot": TypeOneShot,
	"timer":   TypeTimer,
	"self":    TypeSelf,
}

var namesByType = map[UnitType]string{
  TypeSimple: "simple",
  TypeOneShot: "oneshot",
  TypeTimer: "timer",
  TypeSelf: "self",
}

// UnitTypeNamed returns a valid UnitType matching a string name, or returns an error if the type name is not valid.
func UnitTypeNamed(typeName string) (UnitType, error) {
  if tp, ok := typesByName[typeName]; ok {
    return tp, nil
  }
  return 0, fmt.Errorf("Unrecognized type name: %s", typeName)
}

// UnmarshalJSON parses a JSON string into a UnitType.
func (t *UnitType) UnmarshalJSON(b []byte) error {
  var s string
  if err := json.Unmarshal(b, &s); err != nil {
    return err
  }
  tp, ok := typesByName[s]
  if  !ok {
    return fmt.Errorf("Invalid unit type: %s", s)
  }
  *t = tp
  return nil
}

// MarshalJSON serializes a UnitType as a JSON string.
func (t *UnitType) MarshalJSON() ([]byte, error) {
  return json.Marshal(namesByType[*t])
}

// DesiredState describes the target state of the system based on the contents of the coordinator database.
type DesiredState struct {
	Units []DesiredSystemdUnit `json:"units"`
	Files map[string][]byte    `json:"-"`
}

// DesiredDockerContainer contains information about the Docker container image to be used by a SystemD unit.
type DesiredDockerContainer struct {
	Name      string `json:"name,omitempty"`
	ImageName string `json:"image_name,omitempty"`
	ImageTag  string `json:"image_tag,omitempty"`
  ImageID   string `json:"-"`
}

// DesiredSystemdUnit contains information about a SystemD unit managed by the coordinator.
type DesiredSystemdUnit struct {
	ID        *int                   `json:"id,omitempty"`
	Path      string                 `json:"path"`
	Type      UnitType               `json:"type"`
	Container DesiredDockerContainer `json:"container,omitempty"`
	Secrets   []string               `json:"secrets,omitempty"`
	Env       map[string]string      `json:"env,omitempty"`
	Ports     map[int]int            `json:"ports,omitempty"`
	Volumes   map[string]string      `json:"volumes,omitempty"`
	Schedule  string                 `json:"calendar,omitempty"`
}

func (session Session) readDesiredUnits(whereClause string, queryArgs ...interface{}) ([]DesiredSystemdUnit, error) {
	var db = session.db

	unitRows, err := db.Query(`
    	SELECT
      		id, path, type,
      		container_name, container_image_name, container_image_tag,
      		secrets, env, ports, volumes,
      		schedule
		FROM state_systemd_units
  	`+whereClause, queryArgs...)
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
		}

		if err = json.Unmarshal(rawEnv, &unit.Env); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed env column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawEnv)
		}

		if err = json.Unmarshal(rawPorts, &unit.Ports); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed ports column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawPorts)
		}

		if err = json.Unmarshal(rawVolumes, &unit.Volumes); err != nil {
			log.WithError(err).WithField("unit", unit.UnitName()).Warn("Malformed volumes column in state_systemd_units row")
			log.Warnf("Contents:\n%s\n---\n", rawVolumes)
		}

		units = append(units, unit)
	}

	return units, nil
}

// ReadDesiredState queries the database for the currently configured desired system state. DesiredDockerContainers
// within the returned state will have no ImageID.
func (session Session) ReadDesiredState() (*DesiredState, error) {
	var secrets = session.secrets

	units, err := session.readDesiredUnits("")
	if err != nil {
		return nil, err
	}

	files, err := secrets.DesiredTLSFiles()
	if err != nil {
		return nil, err
	}

	return &DesiredState{Units: units, Files: files}, nil
}

// ReadDesiredUnit queries the database to load one specific desired systemd unit. It returns nil if no unit with the
// requested id exists.
func (session Session) ReadDesiredUnit(id int) (*DesiredSystemdUnit, error) {
	units, err := session.readDesiredUnits("WHERE id = $1", id)
	if err != nil {
		return nil, err
	}

	if len(units) == 0 {
		return nil, nil
	}

	return &units[0], nil
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

// UndesireUnit requests that a unit should no longer be present on the system by removing it from the database.
func (session Session) UndesireUnit(id int) error {
	var db = session.db

	_, err := db.Exec(`
		DELETE FROM state_systemd_units WHERE id = $1
	`, id)
	return err
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

	_, err = db.Exec(`
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
		unit.ID,
	)
	return err
}

// UnitName derives the SystemD logical unit name from the path of its source on disk.
func (unit DesiredSystemdUnit) UnitName() string {
	return path.Base(unit.Path)
}

// DesiredSystemdUnitBuilder incrementally constructs and validates a DesiredUnit.
type DesiredSystemdUnitBuilder struct {
	unit *DesiredSystemdUnit
}

// BuildDesiredUnit creates a builder that can construct a new DesiredSystemdUnit.
func BuildDesiredUnit() DesiredSystemdUnitBuilder {
	return DesiredSystemdUnitBuilder{
		unit: &DesiredSystemdUnit{},
	}
}

// ModifyDesiredUnit creates a builder that modifies an existing DesiredSystemdUnit.
func ModifyDesiredUnit(unit *DesiredSystemdUnit) DesiredSystemdUnitBuilder {
	return DesiredSystemdUnitBuilder{
		unit: unit,
	}
}

func (builder *DesiredSystemdUnitBuilder) validate() error {
	// Check container data validity.
	switch builder.unit.Type {
	case TypeSimple:
		if len(builder.unit.Container.Name) == 0 {
			return errors.New("invalid empty container name")
		}

		fallthrough
	case TypeOneShot:
		if !strings.HasPrefix(builder.unit.Container.ImageName, "quay.io/smashwilson/az-") {
			log.WithField("imageName", builder.unit.Container.ImageName).Warn("Attempt to create desired unit with invalid container image.")
			return errors.New("invalid container image name")
		}

		if len(builder.unit.Container.ImageTag) == 0 {
			return errors.New("invalid empty container image tag")
		}
	default:
		if len(builder.unit.Container.Name) > 0 || len(builder.unit.Container.ImageTag) > 0 || len(builder.unit.Container.Name) > 0 {
			return errors.New("attempt to specify container information for unit type that does not use one")
		}
	}

	// Check schedule.
	if builder.unit.Type == TypeTimer {
		if len(builder.unit.Schedule) == 0 {
			return errors.New("timer units must have a schedule")
		}
	} else {
		if len(builder.unit.Schedule) > 0 {
			return errors.New("non-timer units may not have a schedule")
		}
	}

	return nil
}

// Path populates the path on disk to the unit file. Must be within the directory `/etc/systemd/system` and
// begin with `az-`.
func (builder *DesiredSystemdUnitBuilder) Path(path string) error {
	path = filepath.Clean(path)
	dirName, fileName := filepath.Split(path)

	if dirName != "/etc/systemd/system/" {
		log.WithField("path", path).Warn("Attempt to create desired unit file in invalid directory.")
		return errors.New("attempt to create desired unit in invalid directory")
	}

	if !strings.HasPrefix(fileName, "az-") {
		log.WithField("path", path).Warn("Attempt to create desired unit file with invalid prefix.")
		return errors.New("Attempt to create desired unit with invalid filename")
	}

	builder.unit.Path = path
	return nil
}

// Type populates the template type based on a human-friendly name. If the container has also been set, the type is
// also used to assert the validity of the presence of container data.
func (builder *DesiredSystemdUnitBuilder) Type(typeName string) error {
	t, err := UnitTypeNamed(typeName)
	if err != nil {
		return err
	}
	builder.unit.Type = t
	return nil
}

// Container validates and populates information about the container used by this service. The container's image must
// begin with `quay.io/smashwilson/az-`. If the type has already been set, it is used to validate whether or not
// a container is expected to be set or not.
func (builder *DesiredSystemdUnitBuilder) Container(imageName string, imageTag string, name string) error {
	builder.unit.Container.ImageName = imageName
	builder.unit.Container.ImageTag = imageTag
	builder.unit.Container.Name = name
	return nil
}

// Secrets populates the secrets requested by this unit.
func (builder *DesiredSystemdUnitBuilder) Secrets(keys []string, session Session) error {
	if err := session.ValidateSecretKeys(keys); err != nil {
		return err
	}

	builder.unit.Secrets = keys
	return nil
}

// Volumes validates and populates the volume mountings requested for the desired unit. Volumes must mount only host
// paths beneath `/etc/ssl/az/`.
func (builder *DesiredSystemdUnitBuilder) Volumes(volumes map[string]string) error {
	var badVolumes []string
	builder.unit.Volumes = make(map[string]string, len(volumes))
	for hostPath, containerPath := range volumes {
		normalizedHostPath := filepath.Clean(hostPath)
		if !strings.HasPrefix(normalizedHostPath, "/etc/ssl/az/") {
			badVolumes = append(badVolumes, hostPath)
		} else {
			builder.unit.Volumes[normalizedHostPath] = containerPath
		}
	}
	if len(badVolumes) > 0 {
		return fmt.Errorf("invalid host volumes: %s", strings.Join(badVolumes, ", "))
	}
	return nil
}

// Env populates the environment variable map given to the container or process.
func (builder *DesiredSystemdUnitBuilder) Env(env map[string]string) error {
	builder.unit.Env = env
	return nil
}

// Ports populates the port map used to make container services available to the outside world.
func (builder *DesiredSystemdUnitBuilder) Ports(ports map[int]int) error {
	builder.unit.Ports = ports
	return nil
}

// Schedule populates the frequency with with a timer unit will fire.
func (builder *DesiredSystemdUnitBuilder) Schedule(schedule string) error {
	builder.unit.Schedule = schedule
	return nil
}

// Build performs final validation checks and, if successful, returns the constructed DesiredSystemdUnit.
func (builder *DesiredSystemdUnitBuilder) Build() (*DesiredSystemdUnit, error) {
	if err := builder.validate(); err != nil {
		return nil, err
	}
	return builder.unit, nil
}
