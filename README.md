# Buildkite Agent Scaler

An AWS lambda function that helps orchestrate autoscaling of Amazon Autoscaling Groups (ASG).

This is designed to be run as regularly as possible, probably with a Cloudwatch Scheduled Event.

## Running locally

```
$ aws-vault exec my-profile -- buildkite-agent-scaler --asg-name elastic-runners-AgentAutoScaleGroup-DDJREG62FLNC --agent-token "$BUILDKITE_AGENT_TOKEN"
```


