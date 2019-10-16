package cli

import (
  "os"
  "fmt"
  "time"
  "io/ioutil"

	log "github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
	"github.com/smashwilson/az-coordinator/web"
  "github.com/aws/aws-sdk-go/aws"
  "github.com/kdar/logrus-cloudwatchlogs"
)

func serve() {
	r := prepare(needs{
		options: true,
		ring:    true,
		session: true,
		db:      true,
	})

  if logGroup, ok := os.LookupEnv("AZ_CLOUDWATCH_GROUP"); ok && len(logGroup) > 0 {
    logStream := fmt.Sprintf("%d.%d", time.Now().Unix(), os.Getpid())

    log.WithFields(log.Fields{
      "region": r.options.AWSRegion,
      "logGroup": logGroup,
      "logStream": logStream,
    }).Info("Initializing AWS logger.")

    cfg := aws.NewConfig().WithRegion(r.options.AWSRegion)
    hook, err := logrus_cloudwatchlogs.NewHookWithDuration(logGroup, logStream, cfg, 500 * time.Millisecond)
    if err != nil {
      log.WithError(err).Fatal("Unable to create CloudWatch hook.")
    }
    log.AddHook(hook)
    log.SetOutput(ioutil.Discard)
    log.SetFormatter(&logrus_cloudwatchlogs.DevFormatter{})

    log.Info("Sup, AWS.")
  }

	log.Info("Performing initial sync.")
	delta, errs := r.session.Synchronize(state.SyncSettings{})
	if len(errs) > 0 {
		for _, err := range errs {
			log.WithError(err).Warn("Synchronization error.")
		}
		log.WithField("errorCount", len(errs)).Fatal("Unable to synchronize.")
	} else {
		log.WithField("delta", delta).Debug("Delta applied.")
	}
	r.session.Close()

	s := web.NewServer(r.options, r.db, r.ring)
	if err := s.Listen(); err != nil {
		log.WithError(err).Fatal("Unable to bind socket.")
	}
}
