#!/bin/bash
set -eu

make handler.zip

# set a version for later steps
buildkite-agent meta-data set version \
  "$(awk -F\" '/const Version/ {print $2}' version/version.go)"
