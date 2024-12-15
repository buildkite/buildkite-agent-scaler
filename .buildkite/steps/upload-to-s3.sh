#!/usr/bin/env bash

set -euo pipefail

export AWS_DEFAULT_REGION=us-east-1

VERSION="v${BUILDKITE_TAG#v}"
BASE_BUCKET=buildkite-lambdas
BUCKET_PATH=buildkite-agent-scaler

if [[ "${1:-}" == "release" ]]; then
  BUCKET_PATH="${BUCKET_PATH}/${VERSION}"
else
  BUCKET_PATH="${BUCKET_PATH}/builds/${BUILDKITE_BUILD_NUMBER}"
fi

echo "~~~ :buildkite: Downloading artifacts"
buildkite-agent artifact download handler.zip .

echo "~~~ :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
aws s3 cp handler.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip"
