# Buildkite Agent Scaler

An AWS lambda function that handles the scaling of an [Amazon Autoscaling Group](https://docs.aws.amazon.com/autoscaling/ec2/userguide/AutoScalingGroup.html) (ASG) based on metrics provided by the Buildkite Agent Metrics API.

In practice, we've seen 300% faster initial scale-ups with this lambda vs native AutoScaling rules. 🚀

## Why?

The [Elastic CI Stack][] depends on being able to scale up quickly from zero instances in response to scheduled Buildkite jobs. Amazon's AutoScaling primatives have a number of limitations that we wanted more granular control over:

* The median time for a scaling event to be triggered was 2 minutes, due to needing two samples with a minimum period of 60 seconds between.
* Scaling can either be by a fixed rate, a fixed step size or tracking, but tracking doesn't work well with custom metrics like we use.

## How does it work?

The lambda (or cli version) polls the Buildkite Metrics API every 10 seconds, and based on the results sets the `DesiredCount` to exactly what is needed. This allows much faster scale up.

## Gracefully scaling in

Whilst the lambda does support scaling in via setting `DesiredCount`, Amazon ASGs appear to not send [Lifecycle Hooks][] before terminating instances, so jobs in progress are interrupted.

Instead, in the [Elastic Stack][] we run the scaler with scale-in disabled (`DISABLE_SCALE_IN`) and rely on the [recent addition in buildkite-agent v3.10.0](https://github.com/buildkite/agent/releases/tag/v3.10.0) of `--disconnect-after-idle-timeout` in the Agent combined with a [systemd PostStop script](https://github.com/buildkite/elastic-ci-stack-for-aws/blob/00c45ab47160b1d1d44c0b3bea8456456444c60e/packer/linux/conf/bin/bk-install-elastic-stack.sh#L136-L143) to terminate the instance and atomically decrease the `DesiredCount` after the agent has been idle for a time period. We've found it to work really well, and is less complicated than relying on `[lifecycled][]` and [Lifecycle Hooks][].

See the [forum post](https://forum.buildkite.community/t/experimental-lambda-based-scaler/425) for more details.

## Publishing Cloudwatch Metrics

The scaler collects it's own metrics and doesn't require the [buildkite-agent-metrics][]. It supports optionally publishing the metrics it collects back to Cloudwatch, although it only supports a subset of the metrics that the [buildkite-agent-metrics][] binary collects:

* Buildkite > (Org, Queue) > `ScheduledJobsCount`
* Buildkite > (Org, Queue) > `RunningJobCount`

## Running as an AWS Lambda

An AWS Lambda bundle is created and published as part of the build process. The lambda will require the following IAM permissions:

- `cloudwatch:PutMetricData`
- `autoscaling:DescribeAutoScalingGroups`
- `autoscaling:SetDesiredCapacity`

It's entrypoint is `handler`, it requires a `go1.x` environment and requires the following env vars:

- `BUILDKITE_AGENT_TOKEN` or `BUILDKITE_AGENT_TOKEN_SSM_KEY`
- `BUILDKITE_QUEUE`
- `AGENTS_PER_INSTANCE`
- `ASG_NAME`

If `BUILDKITE_AGENT_TOKEN_SSM_KEY` is set, the token will be read from [AWS Systems Manager Parameter Store GetParameter](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html) which [can also read from AWS Secrets Manager](https://docs.aws.amazon.com/systems-manager/latest/userguide/integration-ps-secretsmanager.html).

```bash
aws lambda create-function \
  --function-name buildkite-agent-scaler \
  --memory 128 \
  --role arn:aws:iam::account-id:role/execution_role \
  --runtime go1.x \
  --zip-file fileb://handler.zip \
  --handler handler
```

## Running locally for development

```
$ aws-vault exec my-profile -- go run . \
  --asg-name elastic-runners-AgentAutoScaleGroup-XXXXX
  --agent-token "$BUILDKITE_AGENT_TOKEN"
```

## Copyright

Copyright (c) 2014-2019 Buildkite Pty Ltd. See [LICENSE](./LICENSE.txt) for details.

[Elastic CI Stack]: https://github.com/buildkite/elastic-ci-stack-for-aws
[buildkite-agent-metrics]: https://github.com/buildkite/buildkite-agent-metrics
[Lifecycle Hooks]: https://docs.aws.amazon.com/autoscaling/ec2/userguide/lifecycle-hooks.html
[lifecycled]: https://github.com/buildkite/lifecycled
