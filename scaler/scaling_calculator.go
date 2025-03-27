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
	// Get the effective agents per instance ratio based on actual reported agents
	var effectiveAgentsPerInstance int
	if metrics.TotalAgents > 0 && asg.DesiredCount > 0 {
		effectiveRatio := float64(metrics.TotalAgents) / float64(asg.DesiredCount)
		effectiveAgentsPerInstance = int(math.Ceil(effectiveRatio))

		// If there are fewer agents per instance than configured, use the lower value
		// This happens during graceful termination when agents self-terminate
		if effectiveAgentsPerInstance < sc.agentsPerInstance {
			log.Printf("Fewer agents than expected: %d agents across %d instances (%.1f per instance vs %d configured)",
				metrics.TotalAgents, asg.DesiredCount, effectiveRatio, sc.agentsPerInstance)
		} else {
			effectiveAgentsPerInstance = sc.agentsPerInstance
		}
	} else {
		// Fall back to configured value if we don't have instances or agents
		effectiveAgentsPerInstance = sc.agentsPerInstance
	}

	if effectiveAgentsPerInstance <= 0 {
		return count
	}

	return int64(math.Ceil(float64(count) / float64(effectiveAgentsPerInstance)))
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

	var desired int64
	if agentsRequired > 0 {
		desired = sc.perInstance(agentsRequired, metrics, asg)
	}

	effectiveAgentsStr := "configured"
	if metrics.TotalAgents > 0 && asg.DesiredCount > 0 &&
		float64(metrics.TotalAgents)/float64(asg.DesiredCount) < float64(sc.agentsPerInstance) {
		effectiveAgentsStr = "actual"
	}

	log.Printf("↳ 🧮 Agents required %d, Instances required %d (using %s agents per instance)",
		agentsRequired, desired, effectiveAgentsStr)

	return desired
}
