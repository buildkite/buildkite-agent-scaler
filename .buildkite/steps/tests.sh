#!/bin/bash
set -euo pipefail

go install gotest.tools/gotestsum@v1.12.0

echo '+++ Running tests'
gotestsum --junitfile "junit-${OSTYPE}.xml" -- -count=1 -failfast ./...
