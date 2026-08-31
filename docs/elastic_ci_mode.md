# Graceful Scale-In for Elastic CI Stack
## Problem
Scaling issue here where there are more than one agent per instance. We currently have an open issue with how the number of agent instances are determined from our agent scaler before scaling out. It does not look into the actual live agents, rather, it checks on the configured `AgentsPerInstance`.
When the `ScaleInIdlePeriod` is met on an instance, this agent gets terminated.
One instance might no longer have the 4 agents running, but now 3. Our agent scaler thinks since there is one instance still up, it assumes that there are 4 agents running.

### The impact

Job dispatch delays, other issues related to job processing.

## Summary of the solution
- Introduces a new **experimental** `ELASTIC_CI_MODE=true` setting that enables graceful scale-in for Elastic CI Stack
- Scaling calculation now takes into account number of online agents in the queue
- Instead of directly reducing ASG capacity, sends `SIGTERM` to `buildkite-agent` processes allowing jobs to complete before instance termination
- Add scan for dangling instances (in case if `buildkite-agent` is not running, and post-exit action doesn't terminate EC2 instance

## Technical Details
- Graceful termination uses SSM to send `SIGTERM` to `buildkite-agent`
- Adds support for the [Elastic CI Stack's](https://github.com/buildkite/elastic-ci-stack-for-aws) `/usr/local/bin/stop-agent-gracefully` script
- More predictable scale-in behavior with `MinimumInstanceUptime` and `MaxDanglingInstancesToCheck`

### Scale-in protection

The Elastic CI Stack launches instances with `NewInstancesProtectedFromScaleIn: true` (CloudFormation
parameter `InstanceScaleInProtection`, default `true`). Draining agents are supposed to leave via
the stack `PostStop` `terminate-instance` call, which decrements desired capacity and is not blocked
by protection.

Elastic CI Mode previously also lowered desired capacity immediately after sending SIGTERM. This
gave both the scaler and each instance ownership of the same decrement. Once the scaler had lowered
desired capacity to `MinSize`, `PostStop` failed with `ValidationError` because another decrement
would violate the group's minimum. Protected instances then remained `InService` after their agents
had stopped.

Elastic CI Mode now leaves desired capacity unchanged when at least one selected instance accepted
SIGTERM. Those instances decrement it through `PostStop` after their jobs drain. If none can
self-terminate (for example, an all-Windows batch or complete SSM failure), the scaler clears their
protection and owns the desired-capacity decrement. A mixed batch waits for successful graceful
terminations to complete and retries the remaining instances on a later poll.

The scaler also clears protection (`autoscaling:SetInstanceProtection`) before reclaiming a dangling
instance whose agent is already stopped:

- Dangling instances (`buildkite-agent` not running), immediately before `SetInstanceHealth`
- Scale-in candidates that cannot self-terminate, before the scaler lowers desired capacity for an
  all-fallback batch

Instances that accepted SIGTERM stay protected while they drain. When the ASG is already at the
computed desired count but still has extra running instances and **zero** agents, the scaler also
runs the shorter (10-minute uptime) dangling scan instead of returning at the converging guard.


```
                         ┌────────────────────────┐
                         │ buildkite-agent-scaler │
                         └────────────────────────┘
                                     │
                                     ▼
                 ┌────────────────────────────────────────┐
                 │                                        │
                 │   Collect metrics from Buildkite API   │
                 │             * Running jobs: 10         │
                 │            * Scheduled jobs: 5         │
                 │                                        │
                 └────────────────────────────────────────┘
                                     │
                                     ▼
                 ┌────────────────────────────────────────┐
                 │       Calculate desired capacity       │
                 │           * Current instances: 8       │
                 │            * Calculated need: 5        │
                 │     * Need to scale in by 3 instances  │
                 │                                        │
                 └────────────────────────────────────────┘
                                     │
                                     ▼
                 ┌────────────────────────────────────────┐
                 │         Scale-in safety checks         │
                 │          * Check cooldown period       │
                 │           (ScaleInCooldown)            │
                 │       * Check pending ASG activities   │
                 │         * Verify metrics freshness     │
                 └────────────────────────────────────────┘
                                     │
                                     ▼
                 ┌────────────────────────────────────────┐
                 │                                        │
                 │    Select instances for termination    │
                 │    * Sort by launch time (oldest first)│
                 │        * Select n oldest instances     │
                 │                                        │
                 └───────────────────┬────────────────────┘
                                     │
                 ┌───────────────────┴────────────────────┐
                 │                                        │
                 ▼                                        ▼
┌────────────────────────────────┐       ┌────────────────────────────────┐
│  Regular ASG (Standard Mode)   │       │  Elastic CI Stack (Elastic CI  │
│                                │       │             Mode)              │
└────────────────────────────────┘       └────────────────────────────────┘
                │                                         │
                ▼                                         ▼
┌────────────────────────────────┐       ┌────────────────────────────────┐
│           Scale-in:            │       │       Graceful scale-in:       │
│     * Reduce desired capacity  │       │      * Connect to EC2 via SSM  │
│     * ASG terminates instances │       │         * Send SIGTERM to      │
│       * Running jobs may be    │       │   buildkite-agent, informing   │
│ interrupted when running more  │       │buildkite-agent to shutdown once│
│          than 1 agent          │       │    it finishes current jobs    │
└────────────────────────────────┘       └────────────────────────────────┘
                │                                         │
                ▼                                         ▼
┌────────────────────────────────┐       ┌────────────────────────────────┐
│                                │       │        On EC2 instance:        │
│        On EC2 instance:        │       │    * Agent disconnects all but │
│               * Use            │       │        active sessions         │
│--disconnect-after-idle-timeout │       │      * Finishes current jobs   │
│                                │       │    * Self-terminates when jobs │
│                                │       │             finish             │
└────────────────────────────────┘       └────────────────────────────────┘
```

## Configuration Parameters

### Availability Monitoring (applies to all modes)
- `AVAILABILITY_THRESHOLD`: Minimum agent availability percentage before triggering scale-out (default 50%, i.e. with 4 agents per instance, we scale out when fewer than 2 agents are online). Set to `0` to disable availability-based scaling.

### Elastic CI Mode Specific
**Note:** The following settings apply **only when `ELASTIC_CI_MODE=true`**

- `ELASTIC_CI_MODE`: Enable enhanced safety features, only for [Elastic CI Stack](https://github.com/buildkite/elastic-ci-stack-for-aws)! (boolean)
- `DANGLING_CHECK_MINIMUM_INSTANCE_UPTIME`: Minimum instance uptime before checking for dangling instances (default 1h)
- `MAX_DANGLING_INSTANCES_TO_CHECK`: Maximum number of instances to scan for dangling detection (default 5)
- `SCALE_IN_COOLDOWN_PERIOD`: Time to wait between scale-in operations (default 1h for Elastic CI Mode, 0 otherwise)
