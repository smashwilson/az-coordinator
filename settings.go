package main

import (
	"encoding/json"
	"log"
	"os"
)

type options struct {
	ListenAddress string `json:"listen_address"`
	DatabaseURL   string `json:"database_url"`
	AuthToken     string `json:"auth_token"`
	MasterKeyId   string `json:"master_key_id"`
}

func getEnvironmentSetting(varName string, defaultValue string) string {
	if value, ok := os.LookupEnv(varName); ok {
		return value
	} else {
		return defaultValue
	}
}

func loadOptions() (*options, error) {
	optionsFilePath := getEnvironmentSetting("AZ_OPTIONS", "/etc/az-coordinator/options.json")
	log.Printf("Loading configuration options from [%v].\n", optionsFilePath)

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
