# Buildkite Agent Scaler

An AWS lambda function that handles the scaling of an
[Amazon Autoscaling Group](https://docs.aws.amazon.com/autoscaling/ec2/userguide/AutoScalingGroup.html)
(ASG) based on metrics provided by the Buildkite Agent Metrics API.

In practice, we've seen 300% faster initial scale-ups with this lambda vs native AutoScaling rules.
🚀

## Why?

The [Elastic CI Stack][] depends on being able to scale up quickly from zero instances in response
to scheduled Buildkite jobs. Amazon's AutoScaling primatives have a number of limitations that we
wanted more granular control over:

* The median time for a scaling event to be triggered was 2 minutes, due to needing two samples with
  a minimum period of 60 seconds between.
* Scaling can either be by a fixed rate, a fixed step size or tracking, but tracking doesn't work
  well with custom metrics like we use.

## How does it work?

The lambda (or cli version) polls the Buildkite Metrics API every 10 seconds, and based on the
results sets the `DesiredCount` to exactly what is needed. This allows much faster scale up.

## Gracefully scaling in

The lambda supports graceful termination of instances during scale-in to allow jobs in progress to complete before instances are terminated.

### Using ASG Lifecycle Hooks (Recommended)

The recommended approach is to use AWS Auto Scaling Group (ASG) lifecycle hooks for graceful termination:

1. Deploy the `lifecycle-hook.yml` CloudFormation template alongside your Elastic CI Stack
2. Set the following environment variables in the lambda:
   - `GRACEFUL_TERMINATION`: Set to `true` to enable graceful termination

When an instance is selected for scale-in:
1. The ASG lifecycle hook pauses the termination process
2. The stop-agent-gracefully script is triggered
3. Agents receive SIGTERM and complete their current jobs (with a configurable timeout, default 1 hour)
4. The instance completes the lifecycle action and is terminated by ASG

Benefits of lifecycle hooks:
- More reliable and standard AWS approach
- Configurable timeout (default 1 hour)
- Proper handling of ASG termination policies

Note: When lifecycle hooks are available, the scaler automatically uses them for graceful termination. If lifecycle hooks are not found, standard ASG termination will be used without guaranteeing job completion.

### Older Approach Using Agent Self-Termination

As an alternative, in the [Elastic CI Stack][] we run the scaler with scale-in disabled (`DISABLE_SCALE_IN`)
and rely on the
[recent addition in buildkite-agent v3.10.0](https://github.com/buildkite/agent/releases/tag/v3.10.0)
of `--disconnect-after-idle-timeout` in the Agent combined with a
[systemd PostStop script](https://github.com/buildkite/elastic-ci-stack-for-aws/blob/00c45ab47160b1d1d44c0b3bea8456456444c60e/packer/linux/conf/bin/bk-install-elastic-stack.sh#L136-L143)
to terminate the instance and atomically decrease the `DesiredCount` after the agent has been idle
for a time period. We've found it to work really well, and is less complicated than relying on
[lifecycled] and [Lifecycle Hooks][].

See the [forum post](https://forum.buildkite.community/t/experimental-lambda-based-scaler/425) for more details.

## Publishing Cloudwatch Metrics

The scaler collects it's own metrics and doesn't require [buildkite-agent-metrics][]. It supports
optionally publishing the metrics it collects back to Cloudwatch, although it only supports a subset
of the metrics that the [buildkite-agent-metrics][] binary collects:

* Buildkite > (Org, Queue) > `ScheduledJobsCount`
* Buildkite > (Org, Queue) > `RunningJobCount`

## Running as an AWS Lambda

An AWS Lambda bundle is created and published as part of the build process. The lambda will require
the following IAM permissions:

- `cloudwatch:PutMetricData`
- `autoscaling:DescribeAutoScalingGroups`
- `autoscaling:DescribeScalingActivities`
- `autoscaling:SetDesiredCapacity`
- `autoscaling:DetachInstances`
- `autoscaling:DescribeAutoScalingInstances`
- `ec2:TerminateInstances`
- `ssm:SendCommand` (for sending SIGTERM via SSM)

Its handler is `bootstrap`, it uses a `provided.al2` runtime and requires the following env vars:

- `BUILDKITE_AGENT_TOKEN` or `BUILDKITE_AGENT_TOKEN_SSM_KEY`
- `BUILDKITE_QUEUE`
- `AGENTS_PER_INSTANCE`
- `ASG_NAME`

Optional graceful termination environment variable:
- `GRACEFUL_TERMINATION` - Set to `true` to enable graceful termination

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

## Running locally for development

```
$ aws-vault exec my-profile -- go run . \
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
