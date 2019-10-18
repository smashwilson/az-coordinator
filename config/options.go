package config

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

// DefaultOptionsPath is the path that will be used to locate the options file if `AZ_OPTIONS` is not specified.
const DefaultOptionsPath = "/etc/az-coordinator/options.json"

// Options contains coordinator-specific configuration options loaded as startup from a JSON file.
type Options struct {
	ListenAddress    string `json:"listen_address"`
	DatabaseURL      string `json:"database_url"`
	AuthToken        string `json:"auth_token"`
	MasterKeyID      string `json:"master_key_id"`
	AWSRegion        string `json:"aws_region"`
	DockerAPIVersion string `json:"docker_api_version"`
	AllowedOrigin    string `json:"allowed_origin"`
	SlackWebhookURL  string `json:"slack_webhook_url"`

	OptionsPath string `json:"-"`
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
	optionsFilePath := getEnvironmentSetting("AZ_OPTIONS", DefaultOptionsPath)
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

	o.OptionsPath = optionsFilePath
	return &o, nil
}
