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

NAME="ecs-agent-scaler"
VERSION=$(awk -F\" '/const Version/ {print $2}' version/version.go)
BASE_BUCKET=buildkite-lambdas
BUCKET_PATH="/${NAME}/v${VERSION}"

if [[ "${1:-}" != "release" ]] ; then
  BUCKET_PATH="${BUCKET_PATH}-${BUILDKITE_BUILD_NUMBER}"
  echo "Uploading as $BUCKET_PATH"
fi

echo "~~~ :buildkite: Downloading artifacts"
mkdir -p dist/
buildkite-agent artifact download "dist/*" dist/
ls -al dist/

echo "+++ :s3: Uploading lambda to ${BASE_BUCKET}/${BUCKET_PATH}/ in ${AWS_DEFAULT_REGION}"
echo aws s3 sync --acl public-read ./dist "s3://${BASE_BUCKET}/${BUCKET_PATH}/"
for f in build/* ;
	do echo "https://s3.amazonaws.com/${BASE_BUCKET}/${BUCKET_PATH}/$f"
done

for region in "${EXTRA_REGIONS[@]}" ; do
	bucket="${BASE_BUCKET}-${region}"
	echo "+++ :s3: Copying files to ${bucket}"
	echo aws --region "${region}" s3 sync --exclude "*" --include "*.zip" --delete --acl public-read "s3://${BASE_BUCKET}/${BUCKET_PATH}/" "s3://${bucket}/${BUCKET_PATH}/"
	for f in build/* ; do
		echo "https://${bucket}.s3-${region}.amazonaws.com/${BUCKET_PATH}/$f"
	done
done
