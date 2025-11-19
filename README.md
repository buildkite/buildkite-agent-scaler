# Buildkite Agent Scaler

An AWS lambda function that handles the scaling of an
[Amazon Autoscaling Group](https://docs.aws.amazon.com/autoscaling/ec2/userguide/AutoScalingGroup.html)
(ASG) based on metrics provided by the Buildkite Agent Metrics API.

In practice, we've seen 300% faster initial scale-ups with this lambda vs native AutoScaling rules.
🚀

## Why?

The [Elastic CI Stack][] depends on being able to scale up quickly from zero instances in response
to scheduled Buildkite jobs. Amazon's AutoScaling primitives have a number of limitations that we
wanted more granular control over:

* The median time for a scaling event to be triggered was 2 minutes, due to needing two samples with
  a minimum period of 60 seconds between.
* Scaling can either be by a fixed rate, a fixed step size or tracking, but tracking doesn't work
  well with custom metrics like we use.

## How does it work?

The lambda (or cli version) polls the Buildkite Metrics API every 10 seconds, and based on the
results sets the `DesiredCount` to exactly what is needed. This allows much faster scale up.

## Configuration

### Availability-based scaling

The scaler monitors agent availability to handle situations where EC2 instances are healthy but Buildkite agents aren't connecting. This can happen due to network issues, agent configuration problems, or instance startup delays.

**`AVAILABILITY_THRESHOLD`** (default: `0.5`)

When jobs are queued, the scaler checks if the percentage of connected agents meets this threshold. For example, with 4 agents per instance and 2 instances running (8 expected agents), if only 3 agents are online, that's 37.5% availability.

When availability drops below the threshold and the ASG has converged (actual instances match desired), the scaler adds one instance to help recover availability.

Set `AVAILABILITY_THRESHOLD=0` to disable availability-based scaling. The scaler will then scale based only on job count.

**Threshold tuning:**

* **Lower threshold (e.g., 0.3)**: Tolerates slower agent connection times, reduces instance churn
* **Higher threshold (e.g., 0.8)**: Aggressive scaling to maintain high availability when agents are expected to connect quickly
* **Disabled (0)**: Job-based scaling only, suitable when agents connect reliably

## Gracefully scaling in

:construction: For [Elastic CI Stack][], there's now available a dedicated and experimental mode configured with `ELASTIC_CI_MODE` variable. You can read more about it [in here](./docs/elastic_ci_mode.md). :construction:
___

Whilst the lambda does support scaling in via setting `DesiredCount`, Amazon ASGs appear to not send
[Lifecycle Hooks][] before terminating instances, so jobs in progress are interrupted.

Instead, in the [Elastic CI Stack][] we run the scaler with scale-in disabled (`DISABLE_SCALE_IN`)
and rely on the
[recent addition in buildkite-agent v3.10.0](https://github.com/buildkite/agent/releases/tag/v3.10.0)
of `--disconnect-after-idle-timeout` in the Agent combined with a
[systemd PostStop script](https://github.com/buildkite/elastic-ci-stack-for-aws/blob/00c45ab47160b1d1d44c0b3bea8456456444c60e/packer/linux/conf/bin/bk-install-elastic-stack.sh#L136-L143)
to terminate the instance and atomically decrease the `DesiredCount` after the agent has been idle
for a time period. We've found it to work really well, and is less complicated than relying on
[lifecycled] and [Lifecycle Hooks][].

See the [forum post](https://forum.buildkite.community/t/experimental-lambda-based-scaler/425) for more details.

## Publishing Cloudwatch Metrics

The scaler collects its own metrics and doesn't require [buildkite-agent-metrics][]. It supports
optionally publishing the metrics it collects back to Cloudwatch, although it only supports a subset
of the metrics that the [buildkite-agent-metrics][] binary collects:

* Buildkite > (Org, Queue) > `ScheduledJobsCount`
* Buildkite > (Org, Queue) > `RunningJobCount`

## Running as an AWS Lambda

An AWS Lambda bundle is created and published as part of the build process. The lambda will require
the following IAM permissions:

* `cloudwatch:PutMetricData`
* `autoscaling:DescribeAutoScalingGroups`
* `autoscaling:DescribeScalingActivities`
* `autoscaling:SetDesiredCapacity`

Its handler is `bootstrap`, it uses a `provided.al2` runtime and requires the following env vars:

* `BUILDKITE_AGENT_TOKEN` or `BUILDKITE_AGENT_TOKEN_SSM_KEY`
* `BUILDKITE_QUEUE`
* `AGENTS_PER_INSTANCE`
* `ASG_NAME`

If `BUILDKITE_AGENT_TOKEN_SSM_KEY` is set, the token will be read from
[AWS Systems Manager Parameter Store GetParameter](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html)
which [can also read from AWS Secrets Manager](https://docs.aws.amazon.com/systems-manager/latest/userguide/integration-ps-secretsmanager.html).

```bash
aws lambda create-function \
  --function-name buildkite-agent-scaler \
  --memory 128 \
  --role arn:aws:iam::account-id:role/execution_role \
  --runtime provided.al2 \
  --zip-file fileb://handler.zip \
  --handler bootstrap
```

## Development

This project uses [mise](https://mise.jdx.dev/) to manage development tooling ensuring all the tooling needed is installed with one step, and in expected versions.
To install mise, execute [./bin/mise](./bin/mise) bootstrap script or follow [mise documentation](https://mise.jdx.dev/installing-mise.html).
Run `mise install` to install all the required tooling defined in [mise.toml](./mise.toml).

### Running agent-scaler locally

```
$ mise exec go -- go run . \
  --asg-name elastic-runners-AgentAutoScaleGroup-XXXXX
  --agent-token "$BUILDKITE_AGENT_TOKEN"
```

## Using Clusters

The `BUILDKITE_AGENT_TOKEN` is scoped to a specific cluster. It's best to create a unique token for
the cluster being targeted by the scaler.

The scaler is set up automatically by the [Elastic CI Stack][]'s CloudFormation templates, which
reference the agent token and a queue name. A Lambda function running the scaler is then generated
using these references (e.g., `BUILDKITE_AGENT_TOKEN_SSM_KEY` and `BUILDKITE_QUEUE`).

## Copyright

Copyright (c) 2014-2019 Buildkite Pty Ltd. See [LICENSE](./LICENSE.txt) for details.

[Elastic CI Stack]: https://github.com/buildkite/elastic-ci-stack-for-aws
[buildkite-agent-metrics]: https://github.com/buildkite/buildkite-agent-metrics
[Lifecycle Hooks]: https://docs.aws.amazon.com/autoscaling/ec2/userguide/lifecycle-hooks.html
[lifecycled]: https://github.com/buildkite/lifecycled
