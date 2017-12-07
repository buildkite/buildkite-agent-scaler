#!/bin/bash
set -eux

docker run --rm \
	-e HANDLER=handler \
	-e PACKAGE=handler \
	-e GOPATH=/go \
	-e LDFLAGS='' \
	-v $PWD:/go/src/github.com/buildkite/buildkite-ecs-agent-scaler \
	-w /go/src/github.com/buildkite/buildkite-ecs-agent-scaler \
	eawsy/aws-lambda-go-shim:latest make all

buildkite-agent artifact upload handler.zip
