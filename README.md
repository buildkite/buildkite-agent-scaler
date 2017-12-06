# Buildkite ECS Director

An AWS lambda function that watches the build metrics produced by [buildkite-metrics][] and adjusts the capacity of an ECS Service to handle queued builds.

Designed to be used with [https://github.com/buildkite/elastic-ci-stack-for-aws-ecs.

