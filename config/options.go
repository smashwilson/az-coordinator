package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/kdar/logrus-cloudwatchlogs"
	log "github.com/sirupsen/logrus"
)

// DefaultOptionsPath is the path that will be used to locate the options file if `AZ_OPTIONS` is not specified.
const DefaultOptionsPath = "/etc/az-coordinator/options.json"

var startTime int64

// Options contains coordinator-specific configuration options loaded as startup from a JSON file.
type Options struct {
	ListenAddress    string `json:"listen_address"`
	DatabaseURL      string `json:"database_url"`
	AuthToken        string `json:"auth_token"`
	MasterKeyID      string `json:"master_key_id"`
	AWSRegion        string `json:"aws_region"`
	CloudwatchGroup  string `json:"cloudwatch_group"`
	DockerAPIVersion string `json:"docker_api_version"`
	AllowedOrigin    string `json:"allowed_origin"`
	SlackWebhookURL  string `json:"slack_webhook_url"`

	ProcessStartTime int64  `json:"-"`
	OptionsPath      string `json:"-"`
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
	if startTime == 0 {
		startTime = time.Now().Unix()
	}
	o.ProcessStartTime = startTime

	return &o, nil
}

// CloudwatchLogger configures a logrus logger to emit records to AWS CloudWatch.
func (o Options) CloudwatchLogger(logger *log.Logger) bool {
	if len(o.CloudwatchGroup) == 0 {
		return false
	}

	logStream := fmt.Sprintf("%d.%d", o.ProcessStartTime, os.Getpid())

	logger.WithFields(log.Fields{
		"region":    o.AWSRegion,
		"logGroup":  o.CloudwatchGroup,
		"logStream": logStream,
	}).Info("Initializing AWS logger.")

	cfg := aws.NewConfig().WithRegion(o.AWSRegion)
	hook, err := logrus_cloudwatchlogs.NewHookWithDuration(o.CloudwatchGroup, logStream, cfg, 500*time.Millisecond)
	if err != nil {
		logger.WithError(err).Error("Unable to create CloudWatch hook.")
		return false
	}
	logger.AddHook(hook)
	log.SetOutput(ioutil.Discard)
	log.SetFormatter(&logrus_cloudwatchlogs.DevFormatter{})
	return true
}
