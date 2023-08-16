#!/usr/bin/env bash

set -euo pipefail

echo --- Download handler.zip
buildkite-agent artifact download handler.zip .

echo --- Create template for Serverless Application Repository
sam package --region us-east-1 --s3-bucket buildkite-serverless-apps-us-east-1 --s3-prefix elastic-ci/agent-scaler --output-template-file packaged.yml
buildkite-agent artifact upload packaged.yml

echo --- Print template for Serverless Application Repository
echo "$(< packaged.yml)"
