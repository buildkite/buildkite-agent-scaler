package scaler

import (
	"log"
	"math"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScalingCalculator struct {
	includeWaiting    bool
	agentsPerInstance int
	permanentAgents   int64
}

func (sc *ScalingCalculator) perInstance(count int64) int64 {
	return int64(math.Ceil(float64(count) / float64(sc.agentsPerInstance)))
}

func (sc *ScalingCalculator) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	log.Printf("Calculating desired instance count for Buildkite Jobs")

	agentsRequired := metrics.ScheduledJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	if sc.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		agentsRequired += metrics.WaitingJobs
	} else {
		agentsRequired += metrics.RunningJobs
	}

	if agentsRequired-sc.permanentAgents < 0 {
		agentsRequired = 0
	} else {
		agentsRequired -= sc.permanentAgents
	}

	var desired int64
	if agentsRequired > 0 {
		desired = sc.perInstance(agentsRequired)
	}

	log.Printf("↳ 🧮 Agents required %d, Instances required %d", agentsRequired, desired)

	return desired
}
