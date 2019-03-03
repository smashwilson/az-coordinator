package state

import (
	"database/sql"
	"encoding/json"
	"log"
)

func DesiredFromDatabase(db *sql.DB) (*State, error) {
	imageRows, err := db.Query("SELECT name, tag, digest FROM state_docker_images")
	if err != nil {
		return nil, err
	}
	defer imageRows.Close()

	images := make([]DockerImage, 10)
	for imageRows.Next() {
		image := DockerImage{}
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

	units := make([]SystemdUnit, 10)
	for unitRows.Next() {
		var (
			rawSecrets []byte
			rawEnv     []byte
		)

		unit := SystemdUnit{}
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

	return &State{Images: images, Units: units}, nil
}

func (image DockerImage) MakeDesired(db *sql.DB) error {
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

func (unit SystemdUnit) MakeDesired(db *sql.DB) error {
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
