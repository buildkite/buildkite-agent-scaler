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
- Scale-in sets ASG desired capacity, then marks the extra instances `Unhealthy` so the ASG can terminate them despite scale-in protection
- The stack lifecycle hook (`stop-agent-gracefully`) drains jobs; the scaler does **not** `systemctl stop` the agent (that would run `ExecStopPost` / `terminate-instance` and double-decrement desired capacity)
- Add scan for dangling instances (in case if `buildkite-agent` is not running, and post-exit action doesn't terminate EC2 instance)

## Technical Details
- Elastic CI instances launch with `NewInstancesProtectedFromScaleIn`. `SetInstanceHealth(Unhealthy)` bypasses that protection; lowering desired capacity alone does not.
- Desired capacity is set **before** instances are marked unhealthy. Marking unhealthy first would make the ASG launch replacements.
- Adds support for the [Elastic CI Stack's](https://github.com/buildkite/elastic-ci-stack-for-aws) `/usr/local/bin/stop-agent-gracefully` lifecycle handler
- More predictable scale-in behavior with `MinimumInstanceUptime` and `MaxDanglingInstancesToCheck`

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
│     * Reduce desired capacity  │       │    * SetDesiredCapacity first  │
│     * ASG terminates instances │       │    * Mark N oldest Unhealthy   │
│       * Running jobs may be    │       │      (bypasses scale-in        │
│ interrupted when running more  │       │       protection)              │
│          than 1 agent          │       │    * ASG terminates extras;    │
│                                │       │      no replacements           │
└────────────────────────────────┘       └────────────────────────────────┘
                │                                         │
                ▼                                         ▼
┌────────────────────────────────┐       ┌────────────────────────────────┐
│                                │       │        On EC2 instance:        │
│        On EC2 instance:        │       │    * Lifecycle hook runs       │
│               * Use            │       │      stop-agent-gracefully     │
│--disconnect-after-idle-timeout │       │    * Agent finishes jobs       │
│                                │       │    * ASG completes terminate   │
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
