package buildkite

import (
	"context"
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
	PollDurationHeader = "Buildkite-Agent-Metrics-Poll-Duration"
)

type Client struct {
	Endpoint   string
	AgentToken string
	UserAgent  string
}

func NewClient(agentToken, agentEndpoint string) *Client {
	return &Client{
		Endpoint:   agentEndpoint,
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
	IdleAgents    int64
	BusyAgents    int64
	TotalAgents   int64
}

func (c *Client) GetAgentMetrics(ctx context.Context, queue string) (AgentMetrics, error) {
	log.Printf("Collecting Buildkite metrics for queue %q", queue)

	var resp struct {
		Organization struct {
			Slug string `json:"slug"`
		} `json:"organization"`
		Agents struct {
			Busy  int64 `json:"busy"`
			Idle  int64 `json:"idle"`
			Total int64 `json:"total"`
		} `json:"agents"`
		Jobs struct {
			Scheduled int64 `json:"scheduled"`
			Running   int64 `json:"running"`
			Waiting   int64 `json:"waiting"`
		} `json:"jobs"`
	}

	t := time.Now()
	pollDuration, err := c.queryMetrics(ctx, &resp, queue)
	if err != nil {
		return AgentMetrics{}, err
	}

	queryDuration := time.Since(t)

	var metrics AgentMetrics
	metrics.OrgSlug = resp.Organization.Slug
	metrics.Queue = queue
	metrics.PollDuration = pollDuration

	metrics.IdleAgents = resp.Agents.Idle
	metrics.BusyAgents = resp.Agents.Busy
	metrics.TotalAgents = resp.Agents.Total

	metrics.ScheduledJobs = resp.Jobs.Scheduled
	metrics.RunningJobs = resp.Jobs.Running
	metrics.WaitingJobs = resp.Jobs.Waiting

	log.Printf("↳ Agents: idle=%d, busy=%d, total=%d",
		metrics.IdleAgents, metrics.BusyAgents, metrics.TotalAgents)
	log.Printf("↳ Jobs: scheduled=%d, running=%d, waiting=%d (took %v)",
		metrics.ScheduledJobs, metrics.RunningJobs, metrics.WaitingJobs, queryDuration)

	return metrics, nil
}

func (c *Client) queryMetrics(ctx context.Context, into interface{}, queue string) (pollDuration time.Duration, err error) {
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil {
		return time.Duration(0), err
	}
	endpoint.Path += "/metrics/queue"
	q := url.Values{"name": []string{queue}}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
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
