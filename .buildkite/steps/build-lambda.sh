#!/bin/bash
set -eux

make handler.zip
buildkite-agent artifact upload handler.zip
