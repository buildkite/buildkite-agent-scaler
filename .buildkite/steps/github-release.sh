#!/usr/bin/env bash

set -eufo pipefail

source .buildkite/lib/release-dry-run.sh

if [[ "${RELEASE_DRY_RUN:-false}" != true && -z "$BUILDKITE_TAG" ]]; then
  echo ^^^ +++
  echo This step should only be run on a tag >&2
  exit 1
fi

echo --- :hammer: Installing packages
apk add --no-progress git github-cli

echo --- Checking tags
version=$(awk -F\" '/const Version/ {print $2}' version/version.go)
tag="v${version#v}"

if [[ "${RELEASE_DRY_RUN:-false}" != true && "$tag" != "$BUILDKITE_TAG" ]]; then
  echo ^^^ +++
  echo "Error: version.go has not been updated to ${BUILDKITE_TAG#v}" >&2
  exit 1
fi

last_tag=$(git describe --tags --abbrev=0 --exclude "$tag")

# escape . so we can use in regex
escaped_tag="${tag//\./\\.}"
escaped_last_tag="${last_tag//\./\\.}"

echo --- The following notes will accompany the release:
# The sed commands below:
#   Find lines between headers of the changelogs (inclusive)
#   Delete the lines included from the headers
# The command substituion will then delete the empty lines from the end
notes=$(sed -n "/^## \[${escaped_tag}\]/,/^## \[${escaped_last_tag}\]/p" CHANGELOG.md | sed '$d')
echo "$notes"

echo --- :lambda: Downloading lambda from artifacts
buildkite-agent artifact download handler.zip .

echo "--- 🚀 Releasing $version"
if [[ "${GITHUB_RELEASE_ACCESS_TOKEN:-}" == "" ]]; then
  echo ^^^ +++
  echo "Error: Missing \$GITHUB_RELEASE_ACCESS_TOKEN"
  exit 1
fi
GITHUB_TOKEN="$GITHUB_RELEASE_ACCESS_TOKEN" release_dry_run gh release create \
  --draft \
  --title "$tag" \
  --notes "$notes" \
  "$tag" \
  handler.zip
