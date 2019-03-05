package state

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path"
	"regexp"

	"github.com/coreos/go-systemd/dbus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

var tagRx = regexp.MustCompile(`\A([^:])+:(.+)\z`)

type ActualState struct {
	Images []ActualDockerImage `json:"images"`
	Units  []ActualSystemdUnit `json:"units"`
}

type ActualDockerImage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

type ActualSystemdUnit struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

func ActualFromSystem(session *Session) (*ActualState, error) {
	images, err := loadActualDockerImages(session.cli)
	if err != nil {
		return nil, err
	}

	units, err := loadActualSystemdUnits(session.conn)
	if err != nil {
		return nil, err
	}

	return &ActualState{Images: images, Units: units}, nil
}

func loadActualDockerImages(cli *client.Client) ([]ActualDockerImage, error) {
	imageSummaries, err := cli.ImageList(context.Background(), types.ImageListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "azurefire"),
		),
	})
	if err != nil {
		return nil, err
	}

	images := make([]ActualDockerImage, len(imageSummaries))
	for _, imageSummary := range imageSummaries {
		if len(imageSummary.RepoTags) < 1 {
			log.Printf("Image %v has no tags. Skipping.\n", imageSummary.ID)
			continue
		}
		tag := imageSummary.RepoTags[0]

		if len(imageSummary.RepoDigests) < 1 {
			log.Printf("Image %v (%v) has no digests. Skipping.\n", tag, imageSummary.ID)
			continue
		}
		digest := imageSummary.RepoDigests[0]

		matches := tagRx.FindStringSubmatch(tag)
		if matches == nil {
			log.Printf("Image name cannot be parsed from tag %v. Skipping.\n", tag)
			continue
		}

		image := ActualDockerImage{
			ID:     imageSummary.ID,
			Name:   matches[1],
			Tag:    matches[2],
			Digest: digest,
		}
		images = append(images, image)
	}

	return images, nil
}

func loadActualSystemdUnits(conn *dbus.Conn) ([]ActualSystemdUnit, error) {
	listedUnits, err := conn.ListUnitFilesByPatterns(
		[]string{"inactive", "deactivating", "failed", "error", "active", "reloading", "activating"},
		[]string{"az-*"},
	)
	if err != nil {
		return nil, err
	}

	units := make([]ActualSystemdUnit, len(listedUnits))
	for _, listedUnit := range listedUnits {
		content, err := ioutil.ReadFile(listedUnit.Path)
		if err != nil {
			log.Printf("Unable to read unit file contents at %v: %v\n", listedUnit.Path, err)
			content = nil
		}

		units = append(units, ActualSystemdUnit{
			Path:    listedUnit.Path,
			Content: content,
		})
	}

	return units, nil
}

func (unit ActualSystemdUnit) Name() string {
	return path.Base(unit.Path)
}

func (unit ActualSystemdUnit) DeleteFromSystem(conn *dbus.Conn) (bool, error) {
	resChan := make(chan string)
	if _, err := conn.StopUnit(unit.Path, "replace", resChan); err != nil {
		log.Printf("Unable to stop unit %v: %v\n", unit, err)
		conn.KillUnit(unit.Path, 9)
	}
	<-resChan

	if _, err := conn.DisableUnitFiles([]string{unit.Path}, false); err != nil {
		log.Printf("Unable to disable %v: %v.\n", unit, err)
	}

	if err := os.Remove(unit.Path); err != nil {
		log.Printf("Unable to remove source for %v: %v.\n", unit, err)
	}

	return true, nil
}
