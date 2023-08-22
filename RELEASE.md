# Releasing

1. Generate release notes using [ghch](https://github.com/buildkite/ghch) `ghch --from=v1.1.1 --next-version=v1.1.2 --format=markdown`
1. Examine the release notes to determine what the version should be, re-run
`ghch` if necessary.
1. Create a release branch `git checkout -b release/$VERSION`
1. Update [version/version.go](version/version.go) with the new version number
1. Update [CHANGELOG.md](CHANGELOG.md) with the release notes
1. Push your branch and open a pull request
1. Once CI has passed, merge your pull request
1. Create a git tag `git tag -sm $TAG $TAG`, where `$TAG=v$VERSION`. Make sure the tag has a `v` and the version does not.
1. Push your tag to GitHub: `git push --tags`
1. Check the following builds:
    1. The SAR publishing pipeline: [buildkite-agent-scaler-publish pipeline](https://buildkite.com/buildkite-aws-stack/buildkite-agent-scaler-publish)
to update the AWS Serverless Application Repository
    1. The tag build on the [usual pipeline](https://buildkite.com/buildkite/buildkite-agent-scaler)
1. Check the [release page on GitHub](https://github.com/buildkite/buildkite-agent-scaler/releases) for a draft release that's been created
1. If you approve of the draft github release, unblock the steps in both builds.
1. Once both builds are green, publish the draft GitHub release
