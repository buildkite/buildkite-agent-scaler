package scaler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/version"
)

const (
	DefaultMetricsEndpoint = "https://agent.buildkite.com/v3"
)

type BuildkiteClient struct {
	Endpoint   string
	AgentToken string
	UserAgent  string
	Queue      string
}

func NewBuildkiteClient(agentToken string) *BuildkiteClient {
	return &BuildkiteClient{
		Endpoint:   DefaultMetricsEndpoint,
		UserAgent:  fmt.Sprintf("buildkite-agent-scaler/%s", version.Version),
		AgentToken: agentToken,
	}
}

func (c *BuildkiteClient) GetScheduledJobCount(queue string) (int64, error) {
	log.Printf("Collecting agent metrics for queue %q", queue)
	t := time.Now()

	endpoint, err := url.Parse(c.Endpoint)
	if err != nil {
		return 0, err
	}

	endpoint.Path += "/metrics"

	req, err := http.NewRequest("GET", endpoint.String(), nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.AgentToken))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}

	var resp struct {
		Jobs struct {
			Queues map[string]struct {
				Scheduled int64 `json:"scheduled"`
			} `json:"queues"`
		} `json:"jobs"`
	}

	defer res.Body.Close()
	err = json.NewDecoder(res.Body).Decode(&resp)
	if err != nil {
		return 0, err
	}

	var count int64

	if queue, exists := resp.Jobs.Queues[queue]; exists {
		count = queue.Scheduled
	}

	log.Printf("↳ Got %d (took %v)", count, time.Now().Sub(t))
	return count, nil
}
