#!/usr/bin/env bash

set -euo pipefail

export AWS_DEFAULT_REGION=us-east-1

VERSION="v${BUILDKITE_TAG#v}"
BASE_BUCKET=buildkite-lambdas
BUCKET_PATH=buildkite-agent-scaler

if [[ "${1:-}" == "release" ]]; then
  if [[ "${RELEASE_DRY_RUN:-false}" != true && -z "$BUILDKITE_TAG" ]]; then
    echo ^^^ +++
    echo This step should only be run on a tag >&2
    exit 1
  fi

  echo --- Checking tags
  version=$(awk -F\" '/const Version/ {print $2}' version/version.go)
  if [[ "${RELEASE_DRY_RUN:-false}" != true && "v${version#v}" != "$BUILDKITE_TAG" ]]; then
    echo ^^^ +++
    echo "Error: version.go has not been updated to ${BUILDKITE_TAG#v}" >&2
    exit 1
  fi

  BUCKET_PATH="${BUCKET_PATH}/${VERSION}"
else
  BUCKET_PATH="${BUCKET_PATH}/builds/${BUILDKITE_BUILD_NUMBER}"
fi

echo "~~~ :buildkite: Downloading artifacts"
buildkite-agent artifact download "handler*.zip" .

echo "~~~ :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
# Upload both versions:
# - handler.zip: x86_64 (default/existing architecture)
# - handler-arm64.zip: arm64 (new architecture)
aws s3 cp handler.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip"
aws s3 cp handler-arm64.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler-arm64.zip"
