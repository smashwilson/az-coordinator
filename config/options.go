package config

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

// Options contains coordinator-specific configuration options loaded as startup from a JSON file.
type Options struct {
	ListenAddress    string `json:"listen_address"`
	DatabaseURL      string `json:"database_url"`
	AuthToken        string `json:"auth_token"`
	MasterKeyID      string `json:"master_key_id"`
	AWSRegion        string `json:"aws_region"`
	DockerAPIVersion string `json:"docker_api_version"`
}

func getEnvironmentSetting(varName string, defaultValue string) string {
	if value, ok := os.LookupEnv(varName); ok {
		return value
	}
	return defaultValue
}

// Load creates an Options struct based on the contents of a JSON file at `/etc/az-coordinator/options.json` or
// the location specified by `AZ_OPTIONS`.
func Load() (*Options, error) {
	optionsFilePath := getEnvironmentSetting("AZ_OPTIONS", "/etc/az-coordinator/options.json")
	log.WithField("path", optionsFilePath).Info("Loading configuration options from file.")

	file, err := os.Open(optionsFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	var o Options
	if err := decoder.Decode(&o); err != nil {
		return nil, err
	}

	return &o, nil
}
