#!/bin/bash
set -eu

tag="${ECR_REPOSITORY}/${BUILDKITE_PIPELINE_SLUG}:${BUILDKITE_COMMIT_REVISION}"

docker build -t "$tag" .
docker push "$tag"
