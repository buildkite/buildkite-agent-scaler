#!/usr/bin/env bash

set -eufo pipefail

VERSION=$(buildkite-agent meta-data get version)

buildkite-agent artifact download 'packaged.yml' .

if [[ -z $BUILDKITE_TAG ]]; then
  APP_ID=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler-edge
else
  APP_ID=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler
fi

echo --- ":aws::lambda: Publishing version $VERSION to SAR"
aws serverlessrepo create-application-version \
  --application-id "$APP_ID" \
  --template-body file://packaged.yml \
  --semantic-version "${VERSION#v}" \
  --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"
