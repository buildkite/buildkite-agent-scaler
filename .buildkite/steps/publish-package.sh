#!/usr/bin/env bash

set -eufo pipefail

VERSION=$(buildkite-agent meta-data get version)

buildkite-agent artifact download 'packaged.yml' .

if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  aws s3 cp \
    --acl public-read \
    packaged.yml \
    "s3://buildkite-serverless-apps-us-east-1/packaged/elastic-ci/agent-scaler/$VERSION/packaged.yml"

else
  aws serverlessrepo create-application-version \
    --application-id arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler \
    --template-body file://packaged.yml \
    --semantic-version "$VERSION" \
    --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"
fi
