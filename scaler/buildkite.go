package scaler

import (
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/buildkite/go-buildkite/buildkite"
)

type BuildkiteClient struct {
	client *buildkite.Client
}

func NewBuildkiteClient(accessToken string) (*BuildkiteClient, error) {
	config, err := buildkite.NewTokenConfig(accessToken, false)
	if err != nil {
		return nil, err
	}

	client := buildkite.NewClient(config.Client())
	client.UserAgent = fmt.Sprintf(
		"%s buildkite-agent-scaler/%s",
		client.UserAgent, "dev",
	)

	return &BuildkiteClient{client: client}, nil
}

func (bc *BuildkiteClient) GetScheduledJobCount(orgSlug string, queue string) (int64, error) {
	t := time.Now()
	log.Printf("Getting scheduled job count for org=%s, queue=%s", orgSlug, queue)

	builds, _, err := bc.client.Builds.ListByOrg(orgSlug, &buildkite.BuildsListOptions{
		State: []string{"scheduled"},
	})
	if err != nil {
		return 0, nil
	}

	var count int64

	// intentionally only get the first page
	for _, build := range builds {
		for _, job := range build.Jobs {
			if job.Type != nil && *job.Type == "waiter" {
				continue
			}

			state := ""
			if job.State != nil {
				state = *job.State
			}

			if state == "scheduled" {
				log.Printf("Adding job to stats (id=%q, pipeline=%q, queue=%q, type=%q, state=%q)",
					*job.ID, *build.Pipeline.Name, queueFromJob(job), *job.Type, state)

				count++
			}
		}
	}

	log.Printf("↳ Got %d (took %v)", count, time.Now().Sub(t))
	return count, nil
}

var queuePattern = regexp.MustCompile(`(?i)^queue=(.+?)$`)

func queueFromJob(j *buildkite.Job) string {
	for _, m := range j.AgentQueryRules {
		if match := queuePattern.FindStringSubmatch(m); match != nil {
			return match[1]
		}
	}
	return "default"
}
