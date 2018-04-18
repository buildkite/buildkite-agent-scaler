#!/bin/bash
set -eux
make build
buildkite-agent artifact upload handler.zip
