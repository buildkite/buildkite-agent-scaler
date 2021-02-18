package scaler

import (
	"math"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScalingCalculator interface {
	DesiredCount(*buildkite.AgentMetrics, *AutoscaleGroupDetails) int64
}

type AbsoluteScaling struct {
	includeWaiting    bool
	agentsPerInstance int
}

func (as *AbsoluteScaling) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	count := metrics.ScheduledJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	if as.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		count += metrics.WaitingJobs
	} else {
		count += metrics.RunningJobs
	}

	var desired int64
	if count > 0 {
		desired = int64(math.Ceil(float64(count) / float64(as.agentsPerInstance)))
	}

	return desired
}

type RelativeScaling struct {
	includeWaiting    bool
	agentsPerInstance int
}

func (rs *RelativeScaling) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	jobCount := metrics.ScheduledJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	if rs.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		jobCount += metrics.WaitingJobs - metrics.RunningJobs
	}

	agentsAvailable := metrics.IdleAgents + (asg.Pending * int64(rs.agentsPerInstance))
	agentsRequired := jobCount - agentsAvailable

	desiredCount := asg.DesiredCount

	if agentsRequired > 0 {
		delta := int64(math.Ceil(float64(agentsRequired) / float64(rs.agentsPerInstance)))
		desiredCount = asg.DesiredCount + delta
	}

	if agentsRequired < 0 {
		delta := int64(math.Ceil(float64(agentsRequired) / float64(rs.agentsPerInstance)))
		desiredCount = asg.DesiredCount + delta
	}

	return desiredCount
}
