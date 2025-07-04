#!/usr/bin/env bash
set -euo pipefail

if [[ $(./bin/mise exec -- gofmt -l ./ | head -c 1 | wc -c) != 0 ]]; then
  echo "The following files haven't been formatted with \`go fmt\`:"
  ./bin/mise exec -- gofmt -l ./
  echo
  echo "Fix this by running \`go fmt ./...\` locally, and committing the result."
  exit 1
fi

echo "Everything is formatted! 🎉"
