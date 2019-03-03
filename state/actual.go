package state

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"regexp"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

var tagRx = regexp.MustCompile(`\A([^:])+:(.+)\z`)

func ActualFromSystem() (*State, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	imageSummaries, err := cli.ImageList(context.Background(), types.ImageListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "azurefire"),
		),
	})
	if err != nil {
		return nil, err
	}

	images := make([]DockerImage, len(imageSummaries))
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

		image := DockerImage{
			ID:     &imageSummary.ID,
			Name:   matches[1],
			Tag:    matches[2],
			Digest: digest,
		}
		images = append(images, image)
	}

	// TODO: Read active units from systemd + filesystem

	return &State{Images: images}, nil
}

func (image DockerImage) PullToSystem() (bool, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return false, err
	}

	ref := image.Name + ":" + image.Tag + "@" + image.Digest
	progress, err := cli.ImagePull(context.Background(), ref, types.ImagePullOptions{})
	if err != nil {
		return false, err
	}
	defer progress.Close()

	payload, err := ioutil.ReadAll(progress)
	if err != nil {
		return false, err
	}
	log.Printf("ImagePull payload:\n%s\n---\n", payload)

	// TODO: return "false" if the image was already present

	return true, nil
}

func (image DockerImage) RemoveFromSystem() (bool, error) {
	if image.ID == nil {
		return false, fmt.Errorf("Unable to remove image %v because it has no ID", image)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return false, err
	}

	_, err = cli.ImageRemove(context.Background(), *image.ID, types.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func (unit SystemdUnit) CreateOnSystem() (bool, error) {
	return false, nil
}

func (unit SystemdUnit) ModifyOnSystem() (bool, error) {
	return false, nil
}

func (unit SystemdUnit) DeleteFromSystem() (bool, error) {
	return false, nil
}
