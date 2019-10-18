package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/smashwilson/az-coordinator/state"
)

type jo map[string]interface{}

type slackPayload struct {
	Blocks []jo   `json:"blocks"`
	Text   string `json:"text,omitempty"`
}

func newSlackPayload(blockCount int) slackPayload {
	return slackPayload{
		Blocks: make([]jo, 0, blockCount),
	}
}

func (payload *slackPayload) appendMarkdownBlock(markdown string) {
	payload.Blocks = append(payload.Blocks, jo{
		"type": "section",
		"text": jo{
			"type":     "mrkdwn",
			"text":     markdown,
			"verbatim": true,
		},
	})
}

func (payload *slackPayload) appendErrorBlock(err error) {
	payload.appendMarkdownBlock(fmt.Sprintf(":exclamation: Error: %s", err))
}

func (payload *slackPayload) appendContainerBlock(container state.UpdatedContainer) {
	status := strings.Builder{}
	fmt.Fprintf(&status, ":octocat: <%s|*%s*> :", container.RepositoryURL(), container.Repository)
	fmt.Fprintf(&status, " :commit: <%s|`%s`>", container.CommitURL(), container.GitOID)
	if container.GitRef != "master" {
		fmt.Fprintf(&status, " :ref: <%s|`%s`>", container.BranchURL(), container.GitRef)
		fmt.Fprintf(&status, " <%s|:pull_request:>", container.PullRequestURL())
	}
	payload.appendMarkdownBlock(status.String())
}

func (payload *slackPayload) appendDivider() {
	payload.Blocks = append(payload.Blocks, jo{"type": "divider"})
}

func (payload slackPayload) render() ([]byte, error) {
	return json.Marshal(payload)
}

func generatePayload(d *state.Delta, errs []error) slackPayload {
	var updatedContainers []state.UpdatedContainer
	if d != nil {
		updatedContainers = d.UpdatedContainers
	}

	payload := newSlackPayload(len(updatedContainers) + len(errs))

	if len(errs) > 0 && len(updatedContainers) > 0 {
		payload.appendMarkdownBlock(":warning: *Partially successful deployment.*")
		payload.Text = "Partially successful deployment."
	} else if len(updatedContainers) > 0 {
		payload.appendMarkdownBlock(":recycle: *Successful deployment.*")
		payload.Text = "Successful deployment."
	} else if len(errs) > 0 {
		payload.appendMarkdownBlock(":rotating_light: *Failed deployment.*")
		payload.Text = "Failed deployment."
	}

	if len(errs) > 0 {
		payload.appendDivider()
		for _, err := range errs {
			payload.appendErrorBlock(err)
		}
	}

	if len(updatedContainers) > 0 {
		payload.appendDivider()
		for _, container := range updatedContainers {
			payload.appendContainerBlock(container)
		}
	}

	return payload
}

func sendPayload(payload slackPayload, webhookURL string) error {
	body, err := payload.render()
	if err != nil {
		return err
	}

	logrus.Debugf("Sending data to Slack webhook:\n%s", string(body))

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.WithError(err).Warning("Unable to read Slack response body")
	}
	logrus.Debugf("Received response from Slack:\n%s", string(respBody))

	return nil
}

// ReportSync reports the result of a state sync operation to a Slack webhook.
func ReportSync(webhookURL string, d *state.Delta, errs []error) {
	if len(errs) == 0 && (d == nil || len(d.UpdatedContainers) == 0) {
		logrus.Debug("Nothing to report.")
		return
	}

	payload := generatePayload(d, errs)
	err := sendPayload(payload, webhookURL)
	if err != nil {
		logrus.WithError(err).Warning("Unable to produce payload for Slack webhook.")
	}
}
