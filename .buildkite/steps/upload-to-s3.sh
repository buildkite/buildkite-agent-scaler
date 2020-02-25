#!/bin/bash
set -eu

# shellcheck disable=SC1090
source "$SEGMENT_LIB_PATH/aws.bash"

export AWS_DEFAULT_REGION=us-west-2
# export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-$SANDBOX_AWS_ACCESS_KEY_ID}
# export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-$SANDBOX_AWS_SECRET_ACCESS_KEY}

VERSION=$(buildkite-agent meta-data get "version")
BASE_BUCKET=segment-lambdas-ci
BUCKET_PATH="buildkite-agent-scaler"

if [[ "${1:-}" == "release" ]] ; then
  BUCKET_PATH="${BUCKET_PATH}/v${VERSION}"
else
  BUCKET_PATH="${BUCKET_PATH}/builds/${BUILDKITE_BUILD_NUMBER}"
fi

function do-upload() {
  echo "--- :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
  aws s3 cp handler.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip"
}

echo "~~~ :buildkite: Downloading artifacts"
buildkite-agent artifact download handler.zip .

run-with-role "default" do-upload
