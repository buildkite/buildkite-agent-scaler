#!/bin/bash
set -e

echo '--- Getting credentials from SSM'
export GITHUB_RELEASE_ACCESS_TOKEN=$(aws ssm get-parameter --name /pipelines/buildkite-agent-scaler/GITHUB_RELEASE_ACCESS_TOKEN --with-decryption --output text --query Parameter.Value --region us-east-1)

if [[ "$GITHUB_RELEASE_ACCESS_TOKEN" == "" ]]; then
  echo "Error: Missing \$GITHUB_RELEASE_ACCESS_TOKEN"
  exit 1
fi

VERSION=$(buildkite-agent meta-data get "version")
buildkite-agent artifact download "handler.zip" .

echo "--- 🚀 Releasing $VERSION"
github-release "v$VERSION" handler.zip \
  --commit "$(git rev-parse HEAD)" \
  --github-repository "buildkite/buildkite-agent-scaler"
