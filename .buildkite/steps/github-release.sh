#!/bin/bash
set -e

if [[ "$GITHUB_RELEASE_ACCESS_TOKEN" == "" ]]; then
  echo "Error: Missing \$GITHUB_RELEASE_ACCESS_TOKEN"
  exit 1
fi

VERSION=$(buildkite-agent meta-data get "version")
buildkite-agent artifact download "handler.zip" .

echo "--- 🚀 Releasing $VERSION"
github-release "v$VERSION" handler.zip \
  --commit "$(git rev-parse HEAD)" \
  --github-repository "segmentio/buildkite-agent-scaler"
