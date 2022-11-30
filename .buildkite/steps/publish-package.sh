#!/usr/bin/env bash

set -eufo pipefail

VERSION=$(buildkite-agent meta-data get version)

buildkite-agent artifact download 'packaged.yml' .

if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  aws serverlessrepo create-application-version \
    --application-id arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler-edge \
    --template-body file://packaged.yml \
    --semantic-version "$VERSION" \
    --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"
else
  aws serverlessrepo create-application-version \
    --application-id arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler \
    --template-body file://packaged.yml \
    --semantic-version "$VERSION" \
    --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"
fi
