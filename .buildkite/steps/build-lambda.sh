#!/bin/bash
set -eu

make handler.zip

if [[ -z "${BUILDKITE_TAG:-}" ]]; then
  VERSION="$(awk -F\" '/const Version/ {print $2}' version/version.go)-edge"
else
  VERSION=$(awk -F\" '/const Version/ {print $2}' version/version.go)
fi

# set version for later steps
buildkite-agent meta-data set version "$VERSION"
