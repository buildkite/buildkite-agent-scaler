#!/usr/bin/env bash

set -euo pipefail

export AWS_DEFAULT_REGION=us-east-1

EXTRA_REGIONS=(
  us-east-2
  us-west-1
  us-west-2
  af-south-1
  ap-east-1
  ap-south-1
  ap-northeast-2
  ap-northeast-1
  ap-southeast-2
  ap-southeast-1
  ca-central-1
  eu-central-1
  eu-west-1
  eu-west-2
  eu-south-1
  eu-west-3
  eu-north-1
  me-south-1
  sa-east-1
)

VERSION="v${2#v}"
BASE_BUCKET=buildkite-lambdas
BUCKET_PATH=buildkite-agent-scaler

if [[ "${1:-}" == "release" ]]; then
  BUCKET_PATH="${BUCKET_PATH}/${VERSION}"
else
  BUCKET_PATH="${BUCKET_PATH}/builds/${BUILDKITE_BUILD_NUMBER}"
fi

echo "~~~ :buildkite: Downloading lambda"
curl -sSL -o handler.zip "https://github.com/buildkite/buildkite-agent-scaler/releases/download/$VERSION/handler.zip"

echo "~~~ :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
aws s3 cp --acl public-read handler.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip"

for region in "${EXTRA_REGIONS[@]}"; do
  bucket="${BASE_BUCKET}-${region}"
  echo "~~~ :s3: Copying files to ${bucket}"
  aws --region "${region}" s3 cp --acl public-read "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip" "s3://${bucket}/${BUCKET_PATH}/handler.zip"
done
