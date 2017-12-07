#!/bin/bash
set -eu

go_pkg="github.com/buildkite/buildkite-ecs-agent-scaler"
go_src_dir="/go/src/${go_pkg}"
version=$(awk -F\" '/const Version/ {print $2}' version/version.go)
dist_file="dist/buildkite-ecs-agent-scaler-v${version}-${BUILDKITE_BUILD_NUMBER:-dev}-lambda.zip"

docker run --rm -v "${PWD}:${go_src_dir}" -w "${go_src_dir}" eawsy/aws-lambda-go
mkdir -p dist/
mv handler.zip "$dist_file"

buildkite-agent artifact upload "$dist_file"
