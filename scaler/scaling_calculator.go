package scaler

import (
	"fmt"
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
	// Default to 0.90 (90%) if not set; use negative value (e.g. -1) to disable
	threshold := sc.availabilityThreshold
	if threshold == 0 {
		threshold = 0.9
	}

	// Skip availability check if explicitly disabled (negative value)
	if threshold > 0 && agentsRequired > 0 && availabilityPercentage < threshold {
		missingAgents := expectedAgents - actualAgents
		log.Printf("↳ 🚨 Availability below threshold (%.2f%% < %.2f%%), missing %d agents",
			availabilityPercentage*100, threshold*100, missingAgents)

		// Elastic CI Mode has an additional gate: only act if below minimum percentage
		// Default to 0.5 (50%) for Elastic CI Mode, 0.0 (always act) for non-Elastic CI Mode
		minPercentage := 0.0
		if sc.elasticCIMode {
			minPercentage = sc.minAgentsPercentage
			if minPercentage <= 0 {
				minPercentage = 0.5
			}
		}

		shouldBoost := availabilityPercentage < minPercentage
		if shouldBoost {
			// Only boost if ASG has converged (actual == desired), otherwise let ASG finish scaling
			if asg.ActualCount == asg.DesiredCount {
				currentJobBasedDesired := desired

				// Add an extra instance to help recover from low availability
				// This handles cases where instances are healthy but agents aren't connecting
				availabilityTarget := asg.DesiredCount + 1
				if asg.DesiredCount == 0 {
					availabilityTarget = 1
				}

				if availabilityTarget > currentJobBasedDesired {
					instancesAdded := availabilityTarget - currentJobBasedDesired
					desired = availabilityTarget

					modePrefix := ""
					thresholdDesc := fmt.Sprintf("%.2f%% threshold", threshold*100)
					if sc.elasticCIMode {
						modePrefix = "[Elastic CI Mode] "
						thresholdDesc = fmt.Sprintf("%.2f%% required", minPercentage*100)
					}
					log.Printf("↳ 📈 %sBoosting desired instances for low availability: %d -> %d (+%d instances). Reason: %d agents online vs %d expected from %d instances (%.2f%% < %s). ASG Desired: %d, Job-based need: %d",
						modePrefix, currentJobBasedDesired, desired, instancesAdded, actualAgents, expectedAgents, asg.DesiredCount, availabilityPercentage*100, thresholdDesc, asg.DesiredCount, currentJobBasedDesired)
				}
			} else {
				modePrefix := ""
				if sc.elasticCIMode {
					modePrefix = "[Elastic CI Mode] "
				}
				log.Printf("↳ ⏳ %sNot boosting for low availability - ASG is still converging (%d actual vs %d desired)", modePrefix, asg.ActualCount, asg.DesiredCount)
			}
		} else if sc.elasticCIMode {
			log.Printf("↳ ✅ [Elastic CI Mode] Not adding instance despite low availability - sufficient percentage of agents online (%.2f%% >= %.2f%%)",
				availabilityPercentage*100, minPercentage*100)
		}
	}

	log.Printf("↳ 🧮 Agents required %d, Instances required %d", agentsRequired, desired)

	return desired
}
