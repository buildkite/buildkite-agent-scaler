#!/bin/bash
set -eux

version=$(awk -F\" '/const Version/ {print $2}' version/version.go)
dist_file="dist/buildkite-ecs-agent-scaler-v${version}-${BUILDKITE_BUILD_NUMBER:-dev}-lambda.zip"

docker run --rm \
	-e HANDLER=handler \
	-e PACKAGE=handler \
	-e GOPATH=/go \
	-e LDFLAGS='' \
	-v $PWD:/go/src/github.com/buildkite/buildkite-ecs-agent-scaler \
	-w /go/src/github.com/buildkite/buildkite-ecs-agent-scaler \
	eawsy/aws-lambda-go-shim:latest make all

mkdir -p dist/
mv handler.zip "$dist_file"

# buildkite-agent artifact upload "$dist_file"
