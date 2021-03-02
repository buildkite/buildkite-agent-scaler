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

func (sc *AbsoluteScaling) perInstance(count int64) int64 {
	return int64(math.Ceil(float64(count) / float64(sc.agentsPerInstance)))
}

func (sc *AbsoluteScaling) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	count := metrics.ScheduledJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	if sc.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		count += metrics.WaitingJobs
	} else {
		count += metrics.RunningJobs
	}

	var desired int64
	if count > 0 {
		desired = sc.perInstance(count)
	}

	return desired
}

type RelativeScaling struct {
	includeWaiting    bool
	agentsPerInstance int
}

func (sc *RelativeScaling) perInstance(count int64) int64 {
	return int64(math.Ceil(float64(count) / float64(sc.agentsPerInstance)))
}

func (sc *RelativeScaling) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	jobCount := metrics.ScheduledJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	if sc.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		jobCount += metrics.WaitingJobs - metrics.RunningJobs
	}

	agentsAvailable := metrics.IdleAgents + (asg.Pending * int64(sc.agentsPerInstance))
	agentsRequired := jobCount - agentsAvailable

	desiredCount := asg.DesiredCount

	if agentsRequired != 0 {
		delta := sc.perInstance(agentsRequired)
		desiredCount = asg.DesiredCount + delta
	}

	return desiredCount
}

type HybridScaling struct {
	includeWaiting    bool
	agentsPerInstance int
}

func (sc *HybridScaling) perInstance(count int64) int64 {
	return int64(math.Ceil(float64(count) / float64(sc.agentsPerInstance)))
}

func (sc *HybridScaling) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
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

	var desired int64
	if agentsRequired > 0 {
		desired = sc.perInstance(agentsRequired)
	}

	// If there are less agents registered than we'd expect based on the size
	// of the autoscaling group, then there may be instances not accepting jobs:
	// possibly because they are in the process of draining jobs for a
	// graceful shutdown. In this case, we should expand the asg further to accommodate.
	anticipated := (asg.DesiredCount - asg.Pending) * int64(sc.agentsPerInstance)
	shortfall := anticipated - metrics.TotalAgents
	if shortfall > 0 {
		desired += sc.perInstance(shortfall)
	}

	return desired
}
