#!/bin/bash
set -euo pipefail

echo '+++ Running tests'
./bin/mise exec -- gotestsum --junitfile "junit-${OSTYPE}.xml" -- -count=1 -failfast ./...
