package scaler

import (
	"log"
	"math"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScalingCalculator struct {
	includeWaiting    bool
	agentsPerInstance int
}

// Calculate how many instances are needed for a given agent count
// Takes into account the actual agent distribution across instances
func (sc *ScalingCalculator) perInstance(count int64, metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	// Calculate effective agents per instance
	effectiveAgentsPerInstance := sc.agentsPerInstance

	// If we have actual agents and instances, calculate the real ratio
	if metrics.TotalAgents > 0 && asg.DesiredCount > 0 {
		effectiveRatio := float64(metrics.TotalAgents) / float64(asg.DesiredCount)
		
		// Use the smaller of:
		// 1. The configured agents per instance (to avoid under-capacity)
		// 2. The actual observed ratio (to avoid over-provisioning during termination)
		// But ensure we never go below 1 agent per instance
		if effectiveRatio < float64(sc.agentsPerInstance) && effectiveRatio >= 1.0 {
			actualAgentsPerInstance := int(math.Ceil(effectiveRatio))
			effectiveAgentsPerInstance = actualAgentsPerInstance
			log.Printf("Using actual agent ratio: %d agents across %d instances (%.1f per instance vs %d configured)",
				metrics.TotalAgents, asg.DesiredCount, effectiveRatio, sc.agentsPerInstance)
		}
	}

	if effectiveAgentsPerInstance <= 0 {
		return count
	}

	return int64(math.Ceil(float64(count) / float64(effectiveAgentsPerInstance)))
}

func (sc *ScalingCalculator) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	log.Printf("Calculating desired instance count for Buildkite Jobs")

	// Calculate total agents required based on jobs
	agentsRequired := metrics.ScheduledJobs + metrics.RunningJobs

	// If waiting jobs are greater than running jobs then optionally
	// use waiting jobs for scaling so that we have instances booted
	// by the time we get to them. This is a gamble, as if the instances
	// scale down before the jobs get scheduled, it's a huge waste.
	// Optionally include waiting jobs when they exceed running jobs
	if sc.includeWaiting && metrics.WaitingJobs > metrics.RunningJobs {
		agentsRequired = metrics.ScheduledJobs + metrics.WaitingJobs
	}

	var desired int64
	if agentsRequired > 0 {
		desired = sc.perInstance(agentsRequired, metrics, asg)
	}

	isUsingActualRatio := metrics.TotalAgents > 0 && asg.DesiredCount > 0 &&
		float64(metrics.TotalAgents)/float64(asg.DesiredCount) < float64(sc.agentsPerInstance)

	effectiveAgentsStr := "configured"
	if isUsingActualRatio {
		effectiveAgentsStr = "actual"
	}

	log.Printf("↳ 🧮 Agents required %d, Instances required %d (using %s agents per instance)",
		agentsRequired, desired, effectiveAgentsStr)

	return desired
}
