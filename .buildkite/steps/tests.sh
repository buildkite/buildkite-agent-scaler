#!/bin/bash
set -euo pipefail

echo '+++ Running tests'
gotestsum --junitfile "junit-${OSTYPE}.xml" -- -count=1 -failfast ./...
