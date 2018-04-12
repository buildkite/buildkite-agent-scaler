# Buildkite Agent Scaler

An AWS lambda function that helps orchestrate autoscaling of Amazon Autoscaling Groups (ASG).

It's function is to listen for SNS events, either from Buildkite or Scheduled Events and then check the agent metrics api and make adjustments to a the desired count of an ASG.

**Still in active development, and not currently working**
