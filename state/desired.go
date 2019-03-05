package state

import (
	"database/sql"
	"encoding/json"
	"log"
	"path"

	"github.com/coreos/go-systemd/dbus"
)

const (
	TypeSimple  = iota
	TypeOneShot = iota
)

type DesiredState struct {
	Images []DesiredDockerImage `json:"images"`
	Units  []DesiredSystemdUnit `json:"units"`
}

type DesiredDockerImage struct {
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

type DesiredSystemdUnit struct {
	Path    string            `json:"path"`
	Type    int               `json:"type"`
	Secrets []string          `json:"secrets"`
	Env     map[string]string `json:"env"`
}

func DesiredFromDatabase(db *sql.DB) (*DesiredState, error) {
	imageRows, err := db.Query("SELECT name, tag, digest FROM state_docker_images")
	if err != nil {
		return nil, err
	}
	defer imageRows.Close()

	images := make([]DesiredDockerImage, 10)
	for imageRows.Next() {
		image := DesiredDockerImage{}
		if err = imageRows.Scan(&image.Name, &image.Tag, &image.Digest); err != nil {
			log.Printf("Unable to load state_docker_images row: %v.\n", err)
			continue
		}
		images = append(images, image)
	}

	unitRows, err := db.Query("SELECT path, type, secrets, env FROM state_systemd_units")
	if err != nil {
		return nil, err
	}
	defer unitRows.Close()

	units := make([]DesiredSystemdUnit, 10)
	for unitRows.Next() {
		var (
			rawSecrets []byte
			rawEnv     []byte
		)

		unit := DesiredSystemdUnit{}
		if err = unitRows.Scan(&unit.Path, &unit.Type, &rawSecrets, &rawEnv); err != nil {
			log.Printf("Unable to load state_systemd_units row: %v.\n", err)
			continue
		}

		if err = json.Unmarshal(rawSecrets, &unit.Secrets); err != nil {
			log.Printf("Malformed secrets column in state_systemd_units row [path=%v]: %v\n.", unit.Path, err)
			log.Printf("Contents:\n%s\n---\n", rawSecrets)
			unit.Secrets = make([]string, 0)
		}

		if err = json.Unmarshal(rawEnv, &unit.Env); err != nil {
			log.Printf("Malformed env column in state_systemd_units row [path=%v]: %v\n", unit.Path, err)
			log.Printf("Contents:\n%s\n---\n", rawEnv)
			unit.Env = make(map[string]string, 0)
		}

		units = append(units, unit)
	}

	return &DesiredState{Images: images, Units: units}, nil
}

func (image DesiredDockerImage) MakeDesired(db *sql.DB) error {
	err := db.QueryRow(`
    INSERT INTO state_docker_images (name, tag, digest)
    VALUES($1, $2, $3)
    ON CONFLICT DO UPDATE SET digest = EXCLUDED.digest
  `, image.Name, image.Tag, image.Digest).Scan()
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func (image DesiredDockerImage) Matches(actual ActualDockerImage) bool {
	return image.Name == actual.Name && image.Tag == actual.Tag && image.Digest == actual.Digest
}

func (unit DesiredSystemdUnit) MakeDesired(db *sql.DB) error {
	rawSecrets, err := json.Marshal(unit.Secrets)
	if err != nil {
		return err
	}

	rawEnv, err := json.Marshal(unit.Env)
	if err != nil {
		return err
	}

	err = db.QueryRow(`
    INSERT INTO state_systemd_units (path, type, secrets, env)
    VALUES($1, $2, $3, $4)
    ON CONFLICT DO UPDATE SET type = EXCLUDED.type, secrets = EXCLUDED.secrets, env = EXCLUDED.env
  `, unit.Path, unit.Type, rawSecrets, rawEnv).Scan()
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}

func (unit DesiredSystemdUnit) Name() string {
	return path.Base(unit.Path)
}

func (unit DesiredSystemdUnit) Matches(actual ActualSystemdUnit) bool {
	return false
}

func (unit DesiredSystemdUnit) CreateOnSystem(conn *dbus.Conn) (bool, error) {
	return false, nil
}

func (unit DesiredSystemdUnit) ModifyOnSystem(conn *dbus.Conn) (bool, error) {
	return false, nil
}
