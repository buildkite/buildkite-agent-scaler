package buildkite

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

type Client struct {
	Endpoint   string
	AgentToken string
	UserAgent  string
}

func NewClient(agentToken string) *Client {
	return &Client{
		Endpoint:   DefaultMetricsEndpoint,
		UserAgent:  fmt.Sprintf("buildkite-agent-scaler/%s", version.VersionString()),
		AgentToken: agentToken,
	}
}

func (c *Client) GetOrgSlug() (string, error) {
	log.Printf("Querying agent metrics for org slug")

	var resp struct {
		Organization struct {
			Slug string `json:"slug"`
		} `json:"organization"`
	}

	d, err := c.queryMetrics(&resp)
	if err != nil {
		return "", err
	}

	log.Printf("↳ Got %q (took %v)", resp.Organization.Slug, d)
	return resp.Organization.Slug, nil
}

func (c *Client) GetScheduledJobCount(queue string) (int64, error) {
	log.Printf("Collecting agent metrics for queue %q", queue)

	var resp struct {
		Jobs struct {
			Queues map[string]struct {
				Scheduled int64 `json:"scheduled"`
			} `json:"queues"`
		} `json:"jobs"`
	}

	d, err := c.queryMetrics(&resp)
	if err != nil {
		return 0, err
	}

	var count int64

	if queue, exists := resp.Jobs.Queues[queue]; exists {
		count = queue.Scheduled
	}

	log.Printf("↳ Got %d (took %v)", count, d)
	return count, nil
}

func (c *Client) queryMetrics(into interface{}) (time.Duration, error) {
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil {
		return time.Duration(0), err
	}

	endpoint.Path += "/metrics"

	req, err := http.NewRequest("GET", endpoint.String(), nil)
	if err != nil {
		return time.Duration(0), err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.AgentToken))

	t := time.Now()

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Duration(0), err
	}

	d := time.Now().Sub(t)

	defer res.Body.Close()
	return d, json.NewDecoder(res.Body).Decode(into)
}
