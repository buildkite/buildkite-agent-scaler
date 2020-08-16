package buildkite

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/version"
)

const (
	DefaultMetricsEndpoint = "https://agent.buildkite.com/v3"
	PollDurationHeader     = `Buildkite-Agent-Metrics-Poll-Duration`
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

type AgentMetrics struct {
	OrgSlug       string
	Queue         string
	ScheduledJobs int64
	RunningJobs   int64
	PollDuration  time.Duration
	WaitingJobs   int64
}

func (c *Client) GetAgentMetrics(queue string) (AgentMetrics, error) {
	log.Printf("Collecting agent metrics for queue %q", queue)

	var resp struct {
		Organization struct {
			Slug string `json:"slug"`
		} `json:"organization"`
		Jobs struct {
			Queues map[string]struct {
				Scheduled int64 `json:"scheduled"`
				Running   int64 `json:"running"`
				Waiting   int64 `json:"waiting"`
			} `json:"queues"`
		} `json:"jobs"`
	}

	t := time.Now()
	pollDuration, err := c.queryMetrics(&resp)
	if err != nil {
		return AgentMetrics{}, err
	}

	queryDuration := time.Now().Sub(t)

	var metrics AgentMetrics
	metrics.OrgSlug = resp.Organization.Slug
	metrics.Queue = queue
	metrics.PollDuration = pollDuration

	if queue, exists := resp.Jobs.Queues[queue]; exists {
		metrics.ScheduledJobs = queue.Scheduled
		metrics.RunningJobs = queue.Running
		metrics.WaitingJobs = queue.Waiting
	}

	log.Printf("↳ Got scheduled=%d, running=%d, waiting=%d (took %v)",
		metrics.ScheduledJobs, metrics.RunningJobs, metrics.WaitingJobs, queryDuration)
	return metrics, nil
}

func (c *Client) queryMetrics(into interface{}) (pollDuration time.Duration, err error) {
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil {
		return time.Duration(0), err
	}
	endpoint.Path += "/metrics"

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return time.Duration(0), err
	}

	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.AgentToken))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Duration(0), err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return time.Duration(0), fmt.Errorf("%s %s: %s", req.Method, endpoint, res.Status)
	}

	// Check if we get a poll duration header from server
	if pollSeconds := res.Header.Get(PollDurationHeader); pollSeconds != "" {
		pollSecondsInt, err := strconv.ParseInt(pollSeconds, 10, 64)
		if err != nil {
			log.Printf("Failed to parse %s header: %v", PollDurationHeader, err)
		} else {
			pollDuration = time.Duration(pollSecondsInt) * time.Second
		}
	}

	return pollDuration, json.NewDecoder(res.Body).Decode(into)
}
