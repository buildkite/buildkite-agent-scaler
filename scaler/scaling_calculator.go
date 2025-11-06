package scaler

import (
	"log"
	"math"
	"time"

	"github.com/buildkite/buildkite-agent-scaler/buildkite"
)

type ScalingCalculator struct {
	includeWaiting        bool
	agentsPerInstance     int
	availabilityThreshold float64 // Availability threshold, e.g. 0.9 for 90%
	minAgentsPercentage   float64 // Minimum acceptable percentage of expected agents, e.g. 0.5 for 50%
	elasticCIMode         bool    // Special mode for Elastic CI Stack with additional safety checks

	// Metrics cache to prevent inconsistent calculations
	lastMetricsTimestamp time.Time
	lastAgentCount       int64
	lastInstanceCount    int64
}

func (sc *ScalingCalculator) perInstance(count int64) int64 {
	if sc.agentsPerInstance <= 0 {
		log.Printf("⚠️  Invalid agentsPerInstance value %d, defaulting to 1", sc.agentsPerInstance)
		return count // Default to 1:1 mapping
	}

	result := int64(math.Ceil(float64(count) / float64(sc.agentsPerInstance)))

	if result < 0 || result > 1000 {
		log.Printf("⚠️  Calculated unreasonable instance count %d, capping at 1000", result)
		return 1000
	}

	return result
}

func (sc *ScalingCalculator) DesiredCount(metrics *buildkite.AgentMetrics, asg *AutoscaleGroupDetails) int64 {
	log.Printf("Calculating desired instance count for Buildkite Jobs")

	// In Elastic CI mode, check if metrics are stale before making scaling decisions
	if sc.elasticCIMode && !metrics.Timestamp.IsZero() {
		metricAge := time.Since(metrics.Timestamp)
		// If metrics are over 2 minutes old, we should be cautious with scaling decisions
		if metricAge > 2*time.Minute {
			log.Printf("⚠️ [Elastic CI Mode] Metrics are %.1f seconds old - too stale for scaling decisions", metricAge.Seconds())
			// For safety, return current desired count to avoid scaling based on stale data
			return asg.DesiredCount
		}
	}

	// For Agent metrics, use cached values if more recent metrics are older than last check
	// This prevents using inconsistent metrics that could lead to invalid scaling decisions
	actualAgents := metrics.TotalAgents
	if sc.elasticCIMode && !sc.lastMetricsTimestamp.IsZero() && !metrics.Timestamp.IsZero() {
		// If our cached metrics are newer than what we just got, use the cached values
		if sc.lastMetricsTimestamp.After(metrics.Timestamp) {
			// Current metrics are older than our cached values
			log.Printf("⚠️ [Elastic CI Mode] Using cached agent count %d instead of stale count %d (cached from %s, metrics from %s)",
				sc.lastAgentCount, actualAgents,
				sc.lastMetricsTimestamp.Format(time.RFC3339),
				metrics.Timestamp.Format(time.RFC3339))
			actualAgents = sc.lastAgentCount
		} else {
			// Update our cache with the newer values
			sc.lastMetricsTimestamp = metrics.Timestamp
			sc.lastAgentCount = actualAgents
			sc.lastInstanceCount = asg.DesiredCount
		}
	} else if !metrics.Timestamp.IsZero() {
		// Initialize cache
		sc.lastMetricsTimestamp = metrics.Timestamp
		sc.lastAgentCount = actualAgents
		sc.lastInstanceCount = asg.DesiredCount
	}

	// Calculate how many agents we expect to have online based on actual running instances
	// If ActualCount is available and non-zero, use it; otherwise fall back to DesiredCount
	instanceCount := asg.ActualCount
	if instanceCount == 0 {
		instanceCount = asg.DesiredCount
	}
	expectedAgents := int64(sc.agentsPerInstance) * instanceCount

	// Calculate availability percentage
	var availabilityPercentage = 1.0
	if expectedAgents > 0 {
		availabilityPercentage = float64(actualAgents) / float64(expectedAgents)
		log.Printf("↳ 🧮 Agent availability: %.2f%% (%d/%d)", availabilityPercentage*100, actualAgents, expectedAgents)
	}

	// Calculate agents required for workload
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

	// Check availability threshold if we have jobs requiring agents
	// Default to 0.90 (90%) if not set; use value > 1.0 to disable
	threshold := sc.availabilityThreshold
	if threshold == 0 {
		threshold = 0.9
	}

	isAvailabilityCheckEnabled := threshold > 0 && threshold <= 1.0
	if isAvailabilityCheckEnabled && agentsRequired > 0 && availabilityPercentage < threshold {
		missingAgents := expectedAgents - actualAgents
		log.Printf("↳ 🚨 Availability below threshold (%.2f%% < %.2f%%), missing %d agents",
			availabilityPercentage*100, threshold*100, missingAgents)

		if sc.elasticCIMode {
			// Elastic CI Mode: Only add instances if we're below the minimum percentage of agents
			// Default to 0.5 (50% of expected agents) if not set
			minPercentage := sc.minAgentsPercentage
			if minPercentage <= 0 {
				minPercentage = 0.5
			}

			// If we have at least the minimum percentage of agents online, we don't need to scale out
			enoughAgentsOnline := availabilityPercentage >= minPercentage

			if !enoughAgentsOnline {
				// Only boost if ASG has converged (actual == desired), otherwise let ASG finish scaling
				if asg.ActualCount == asg.DesiredCount {
					currentJobBasedDesired := desired // Capture 'desired' before modification

					// When availability is critically low, add an extra instance to help recover
					// This handles cases where existing instances are healthy but agents aren't connecting
					availabilityTarget := asg.DesiredCount + 1
					if asg.DesiredCount == 0 {
						// If ASG desires 0, but availability is low (e.g. 0 actual agents from 0 desired),
						// we ensure at least 1 instance is targeted to recover.
						availabilityTarget = 1
					}

					if availabilityTarget > currentJobBasedDesired {
						instancesAdded := availabilityTarget - currentJobBasedDesired
						desired = availabilityTarget
						log.Printf("↳ 📈 [Elastic CI Mode] Boosting desired instances for low availability: %d -> %d (+%d instances). Reason: %d agents online vs %d expected from %d instances (%.2f%% < %.2f%% required). ASG Desired: %d, Job-based need: %d",
							currentJobBasedDesired, desired, instancesAdded, actualAgents, expectedAgents, asg.DesiredCount, availabilityPercentage*100, minPercentage*100, asg.DesiredCount, currentJobBasedDesired)
					}
					// If availabilityTarget <= currentJobBasedDesired, 'desired' (based on jobs) is already sufficient
					// or higher than what this availability rule would set, so no change or log from this specific rule.
				} else {
					log.Printf("↳ ⏳ [Elastic CI Mode] Not boosting for low availability - ASG is still converging (%d actual vs %d desired)", asg.ActualCount, asg.DesiredCount)
				}
			} else {
				log.Printf("↳ ✅ [Elastic CI Mode] Not adding instance despite low availability - sufficient percentage of agents online (%.2f%% >= %.2f%%)",
					availabilityPercentage*100, minPercentage*100)
			}
		} else {
			// Non-Elastic CI Mode: Add an extra instance when availability is low
			// Only boost if ASG has converged (actual == desired), otherwise let ASG finish scaling
			if asg.ActualCount == asg.DesiredCount {
				currentJobBasedDesired := desired
				availabilityTarget := asg.DesiredCount + 1

				if availabilityTarget > currentJobBasedDesired {
					instancesAdded := availabilityTarget - currentJobBasedDesired
					desired = availabilityTarget
					log.Printf("↳ 📈 Boosting desired instances for low availability: %d -> %d (+%d instances). Reason: %d agents online vs %d expected from %d instances (%.2f%% < %.2f%% threshold). ASG Desired: %d, Job-based need: %d",
						currentJobBasedDesired, desired, instancesAdded, actualAgents, expectedAgents, asg.DesiredCount, availabilityPercentage*100, threshold*100, asg.DesiredCount, currentJobBasedDesired)
				}
			} else {
				log.Printf("↳ ⏳ Not boosting for low availability - ASG is still converging (%d actual vs %d desired)", asg.ActualCount, asg.DesiredCount)
			}
		}
	}

	log.Printf("↳ 🧮 Agents required %d, Instances required %d", agentsRequired, desired)

	return desired
}
