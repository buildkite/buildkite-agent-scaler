#!/bin/bash
set -eux

docker run --rm \
  -e HANDLER=handler \
  -e PACKAGE=handler \
  -e GOPATH=/go \
  -e LDFLAGS='' \
  -v "$PWD:/go/src/github.com/buildkite/buildkite-agent-scaler" \
  -w /go/src/github.com/buildkite/buildkite-agent-scaler \
  golang:1.10 make all

# buildkite-agent artifact upload handler.zip
