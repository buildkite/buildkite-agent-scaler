package scaler

import (
	"errors"
	"fmt"
	"log"
	"regexp"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/buildkite/go-buildkite/buildkite"
	"github.com/cenkalti/backoff"
)

const (
	agentCPURequired    = 1024
	agentMemoryRequired = 2048
)

type Params struct {
	ECSCluster        string
	ECSService        string
	BuildkiteQueue    string
	BuildkiteOrg      string
	BuildkiteApiToken string
	SpotFleetID       string
}

func Run(params Params) error {
	count, err := getScheduledJobCount(params.BuildkiteApiToken, params.BuildkiteOrg, params.BuildkiteQueue)
	if err != nil {
		return err
	}

	sess := session.New()

	currentCount, err := getAgentCount(sess, params.ECSCluster, params.ECSService)
	if err != nil {
		return err
	}

	log.Printf("Changing agent count from %d to %d", currentCount, count)

	// naively, we will set the count to whatever we need. this will need some form of cooldown
	// or step function to avoid huge delta change
	if err = updateAgentCount(sess, params.ECSCluster, params.ECSService, count); err != nil {
		return err
	}

	resources, err := getContainerInstanceResources(sess, params.ECSCluster)
	if err != nil {
		return err
	}

	var totalRequiredCPU int64 = (count * agentCPURequired)
	var totalRequiredMemory int64 = (count * agentMemoryRequired)
	var desiredCapacity int64

	if resources.CPU < totalRequiredCPU {
		log.Printf("Too little CPU (%d vs %d), needs scaling", resources.CPU, totalRequiredCPU)
		needed := (totalRequiredCPU - resources.CPU) / agentCPURequired
		log.Printf("Deficit: %d (%d)", totalRequiredCPU-resources.CPU, needed)
		desiredCapacity += needed
	} else if resources.Memory < totalRequiredMemory {
		log.Printf("Too little Memory (%d vs %d), needs scaling", resources.Memory, totalRequiredMemory)
		needed := (totalRequiredMemory - resources.Memory) / agentMemoryRequired
		log.Printf("Deficit: %d (%d)", totalRequiredMemory-resources.Memory, needed)
		desiredCapacity += needed
	}

	if desiredCapacity > 0 {
		log.Printf("Scaling up spotfleet %q to %d", params.SpotFleetID, desiredCapacity)
		if err = updateSpotCapacity(sess, params.SpotFleetID, desiredCapacity); err != nil {
			return err
		}
	}

	return nil
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

func getScheduledJobCount(accessToken string, orgSlug string, queue string) (int64, error) {
	config, err := buildkite.NewTokenConfig(accessToken, false)
	if err != nil {
		return 0, err
	}

	client := buildkite.NewClient(config.Client())
	client.UserAgent = fmt.Sprintf(
		"%s buildkite-ecs-agent-scaler/%s",
		client.UserAgent, "dev",
	)

	builds, _, err := client.Builds.ListByOrg(orgSlug, &buildkite.BuildsListOptions{
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

	return count, nil
}

type containerInstanceResources struct {
	CPU          int64
	Memory       int64
	Count        int
	RunningTasks int64
	PendingTasks int64
}

func getContainerInstanceResources(sess *session.Session, cluster string) (res containerInstanceResources, err error) {
	svc := ecs.New(sess)

	listResult, err := svc.ListContainerInstances(&ecs.ListContainerInstancesInput{
		Cluster: aws.String(cluster),
	})
	if err != nil {
		return res, err
	}

	// no container instances
	if len(listResult.ContainerInstanceArns) == 0 {
		return containerInstanceResources{}, nil
	}

	result, err := svc.DescribeContainerInstances(&ecs.DescribeContainerInstancesInput{
		Cluster:            aws.String(cluster),
		ContainerInstances: listResult.ContainerInstanceArns,
	})
	if err != nil {
		return res, err
	}

	for _, instance := range result.ContainerInstances {
		res.Count++
		res.RunningTasks += *instance.RunningTasksCount
		res.PendingTasks += *instance.PendingTasksCount

		for _, resource := range instance.RemainingResources {
			switch *resource.Name {
			case "CPU":
				res.CPU += *resource.IntegerValue
			case "MEMORY":
				res.Memory += *resource.IntegerValue
			}
		}
	}

	return res, nil
}

func getAgentCount(sess *session.Session, cluster, service string) (int64, error) {
	ecsSvc := ecs.New(sess)

	results, err := ecsSvc.DescribeServices(&ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: aws.StringSlice([]string{service}),
	})
	if err != nil {
		return 0, err
	}

	if len(results.Services) == 0 {
		return 0, errors.New("No service found")
	}

	return *results.Services[0].DesiredCount, nil
}

func updateAgentCount(sess *session.Session, cluster, service string, count int64) error {
	svc := ecs.New(sess)

	log.Printf("Modifying service %s, setting count=%d", service, count)
	_, err := svc.UpdateService(&ecs.UpdateServiceInput{
		Cluster:      aws.String(cluster),
		Service:      aws.String(service),
		DesiredCount: aws.Int64(count),
	})
	if err != nil {
		return err
	}

	operation := func() error {
		currentCount, err := getAgentCount(sess, cluster, service)
		if err != nil {
			return backoff.Permanent(err)
		}
		log.Printf("Got current %d, waiting until %d", currentCount, count)
		if currentCount != count {
			return errors.New("Not matching yet")
		}
		return nil
	}

	return backoff.Retry(operation, backoff.NewExponentialBackOff())
}

func waitForService(sess *session.Session, cluster, service string) error {
	svc := ecs.New(sess)

	return svc.WaitUntilServicesStable(&ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: aws.StringSlice([]string{service}),
	})
}

func updateSpotCapacity(sess *session.Session, spotFleet string, count int64) error {
	ec2Svc := ec2.New(sess)

	log.Printf("Modifying spotfleet %s, setting count=%d", spotFleet, count)
	_, err := ec2Svc.ModifySpotFleetRequest(&ec2.ModifySpotFleetRequestInput{
		SpotFleetRequestId: aws.String(spotFleet),
		TargetCapacity:     aws.Int64(count),
	})
	if err != nil {
		return err
	}

	// _, err := ec2Svc.DescribeSpotFleetRequests(&ec2.DescribeSpotFleetRequestsInput{
	// 	SpotFleetRequestIds: []*string{
	// 		aws.String(spotFleet),
	// 	},
	// })
	// if err != nil {
	// 	return err
	// }

	return nil
}
