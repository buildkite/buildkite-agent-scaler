#!/usr/bin/env bash

set -eufo pipefail

source .buildkite/lib/release-dry-run.sh

VERSION=$(buildkite-agent meta-data get version)

buildkite-agent artifact download "packaged*.yml" .

# Determine SAR application IDs based on release type
if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  # Edge builds
  APP_ID=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler-edge
  APP_ID_ARM64=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler-arm64-edge
else
  # Release builds
  APP_ID=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler
  APP_ID_ARM64=arn:aws:serverlessrepo:us-east-1:172840064832:applications/buildkite-agent-scaler-arm64
fi

echo --- ":aws::lambda: Publishing version $VERSION to SAR"
release_dry_run aws serverlessrepo create-application-version \
  --application-id "$APP_ID" \
  --template-body file://packaged.yml \
  --semantic-version "${VERSION#v}" \
  --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"

echo --- ":aws::lambda: Publishing arm64 version $VERSION to SAR"
release_dry_run aws serverlessrepo create-application-version \
  --application-id "$APP_ID_ARM64" \
  --template-body file://packaged-arm64.yml \
  --semantic-version "${VERSION#v}" \
  --source-code-url "https://github.com/buildkite/buildkite-agent-scaler/tree/$(git rev-parse HEAD)/"
