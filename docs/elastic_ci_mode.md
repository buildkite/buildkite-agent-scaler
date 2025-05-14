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
- `ELASTIC_CI_MODE`: Enable enhanced safety features, only for [Elastic CI Stack](https://github.com/buildkite/elastic-ci-stack-for-aws)! (boolean)
- `AVAILABILITY_THRESHOLD`: Minimum agent availability percentage (default 90%)
- `MIN_AGENTS_PERCENTAGE`: Minimum acceptable percentage of expected agents — ratio of desired agents number to actual (default 50%, i.e. we tolerate 2 agent instances running on a single EC2 out of desired 4)
- `DANGLING_CHECK_MINIMUM_INSTANCE_UPTIME`: Minimum instance uptime before checking for dangling instances (default 1h)
- `MAX_DANGLING_INSTANCES_TO_CHECK`: Maximum number of instances to scan for dangling detection (default 5)
- `SCALE_IN_COOLDOWN_PERIOD`: Time to wait between scale-in operations (default 1h)
