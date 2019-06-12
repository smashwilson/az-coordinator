package main

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

type options struct {
	ListenAddress string `json:"listen_address"`
	DatabaseURL   string `json:"database_url"`
	AuthToken     string `json:"auth_token"`
	MasterKeyID   string `json:"master_key_id"`
	AWSRegion     string `json:"aws_region"`
}

func getEnvironmentSetting(varName string, defaultValue string) string {
	if value, ok := os.LookupEnv(varName); ok {
		return value
	}
	return defaultValue
}

func loadOptions() (*options, error) {
	optionsFilePath := getEnvironmentSetting("AZ_OPTIONS", "/etc/az-coordinator/options.json")
	log.WithField("path", optionsFilePath).Info("Loading configuration options from file.")

	file, err := os.Open(optionsFilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	var o options
	if err := decoder.Decode(&o); err != nil {
		return nil, err
	}

	return &o, nil
}
