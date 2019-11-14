package state

import (
	"context"
	"io/ioutil"
	"regexp"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/sirupsen/logrus"
)

// PullAllImages concurrently pulls the latest versions of all Docker container images used by desired SystemD units
// referenced by the current system state. Call this between ReadDesiredState and ReadImages to desire the most recently
// published version of each image.
func (s SessionLease) PullAllImages(state DesiredState) []error {
	errs := make([]error, 0)

	imageRefs := make(map[string]bool, len(state.Units))
	for _, unit := range state.Units {
		if unit.Container != nil && len(unit.Container.ImageName) > 0 && len(unit.Container.ImageTag) > 0 {
			ref := unit.Container.ImageName + ":" + unit.Container.ImageTag
			imageRefs[ref] = true
			s.Log.WithField("ref", ref).Debug("Scheduling docker pull.")
		}
	}

	s.Log.WithField("count", len(imageRefs)).Debug("Beginning docker pulls.")
	results := make(chan error, len(imageRefs))
	for ref := range imageRefs {
		go s.pullImage(ref, results)
	}
	for i := 0; i < len(imageRefs); i++ {
		err := <-results
		if err != nil {
			errs = append(errs, err)
		}
	}
	s.Log.WithField("count", len(imageRefs)).Debug("Docker pulls complete.")

	return errs
}

var (
	rxUpToDate        = regexp.MustCompile(`Status: Image is up to date`)
	rxDownloadedNewer = regexp.MustCompile(`Status: Downloaded newer image`)
)

func (s SessionLease) pullImage(ref string, done chan<- error) {
	progress, err := s.cli.ImagePull(context.Background(), ref, types.ImagePullOptions{})
	if err != nil {
		done <- err
		return
	}
	defer progress.Close()

	payload, err := ioutil.ReadAll(progress)
	if err != nil {
		done <- err
		return
	}

	if rxUpToDate.Match(payload) {
		s.Log.WithField("ref", ref).Debug("Container image already current.")
	} else if rxDownloadedNewer.Match(payload) {
		s.Log.WithField("ref", ref).Info("Container image updated.")
	} else {
		s.Log.WithField("ref", ref).Warningf("Unrecognized ImagePull payload:\n%s\n---\n", payload)
	}

	done <- nil
}

// CreateNetwork ensures that the expected Docker backplane network is present.
func (s SessionLease) CreateNetwork() error {
	networks, err := s.cli.NetworkList(context.Background(), types.NetworkListOptions{})
	if err != nil {
		return err
	}

	for _, network := range networks {
		if network.Name == "local" {
			// Network already exists
			s.Log.WithFields(logrus.Fields{
				"networkID":     network.ID,
				"networkName":   network.Name,
				"networkDriver": network.Driver,
			}).Info("Network already exists.")
			return nil
		}
	}

	response, err := s.cli.NetworkCreate(context.Background(), "local", types.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "bridge",
		IPAM: &network.IPAM{
			Driver: "default",
		},
		Internal: false,
	})
	if err != nil {
		return err
	}

	s.Log.WithField("networkID", response.ID).Debug("Network created.")
	return nil
}

// Prune removes stopped containers and unused container images to reclaim disk space.
func (s SessionLease) Prune() {
	cr, err := s.cli.ContainersPrune(context.Background(), filters.NewArgs())
	if err != nil {
		s.Log.WithError(err).Warning("Unable to prune containers.")
	} else {
		s.Log.WithFields(logrus.Fields{
			"containers":     len(cr.ContainersDeleted),
			"spaceReclained": cr.SpaceReclaimed,
		}).Debug("Containers removed.")
	}

	ir, err := s.cli.ImagesPrune(context.Background(), filters.NewArgs())
	if err != nil {
		s.Log.WithError(err).Warning("Unable to prune images.")
	} else {
		s.Log.WithFields(logrus.Fields{
			"images":         len(ir.ImagesDeleted),
			"spaceReclaimed": cr.SpaceReclaimed,
		}).Debug("Images removed.")
	}
}
