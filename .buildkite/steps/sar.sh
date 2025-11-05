#!/usr/bin/env bash

set -euo pipefail

echo --- Download handler zips
buildkite-agent artifact download "handler*.zip" .

# Package x86_64 version (default - handler.zip is already x86_64)
echo "--- Create x86_64 template for Serverless Application Repository"
sam package \
  --template-file template.yaml \
  --region us-east-1 \
  --s3-bucket buildkite-serverless-apps-us-east-1 \
  --s3-prefix elastic-ci/agent-scaler \
  --output-template-file packaged.yml
buildkite-agent artifact upload packaged.yml

# Package arm64 version
echo "--- Create arm64 template for Serverless Application Repository"
# Temporarily rename files to avoid overwriting handler.zip
mv handler.zip handler-x86_64-backup.zip
mv handler-arm64.zip handler.zip
sam package \
  --template-file template.yaml \
  --region us-east-1 \
  --s3-bucket buildkite-serverless-apps-us-east-1 \
  --s3-prefix elastic-ci/agent-scaler-arm64 \
  --output-template-file packaged-arm64.yml
# Restore original filenames
mv handler.zip handler-arm64.zip
mv handler-x86_64-backup.zip handler.zip
# Update the hardcoded architecture from x86_64 to arm64
echo "Updating Lambda architecture to arm64..."
sed -i.bak 's/- x86_64/- arm64/' packaged-arm64.yml && rm packaged-arm64.yml.bak
buildkite-agent artifact upload packaged-arm64.yml

echo --- Print x86_64 template for Serverless Application Repository
echo "$(< packaged.yml)"

echo --- Print arm64 template for Serverless Application Repository
echo "$(< packaged-arm64.yml)"
