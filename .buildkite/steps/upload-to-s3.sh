#!/bin/bash
set -eu

export AWS_DEFAULT_REGION=us-east-1
# export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-$SANDBOX_AWS_ACCESS_KEY_ID}
# export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-$SANDBOX_AWS_SECRET_ACCESS_KEY}

EXTRA_REGIONS=(
	us-east-2
	us-west-1
	us-west-2
	eu-west-1
	eu-west-2
	eu-central-1
	ap-northeast-1
	ap-northeast-2
	ap-southeast-1
	ap-southeast-2
	ap-south-1
	sa-east-1
)

VERSION=$(awk -F\" '/const Version/ {print $2}' version/version.go)
BASE_BUCKET=buildkite-lambdas
BUCKET_PATH="ecs-agent-scaler"

if [[ "${1:-}" == "release" ]] ; then
  BUCKET_PATH="${BUCKET_PATH}/v${VERSION}"
else
  BUCKET_PATH="${BUCKET_PATH}/builds/${BUILDKITE_BUILD_NUMBER}"
fi

echo "~~~ :buildkite: Downloading artifacts"
buildkite-agent artifact download handler.zip .

echo "--- :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
aws s3 cp --acl public-read handler.zip "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip"

for region in "${EXTRA_REGIONS[@]}" ; do
	bucket="${BASE_BUCKET}-${region}"
	echo "--- :s3: Copying files to ${bucket}"
	aws --region "${region}" s3 cp --acl public-read "s3://${BASE_BUCKET}/${BUCKET_PATH}/handler.zip" "s3://${bucket}/${BUCKET_PATH}/handler.zip"
done
