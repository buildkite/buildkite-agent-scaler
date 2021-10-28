# Releasing

1. Generate release notes using [ghch](https://github.com/buildkite/ghch) `~/go/bin/ghch --from=v1.1.1 --next-version=v1.1.2 --format=markdown`
1. Examine the release notes to determine what the version should be, re-run
`ghch` if necessary.
1. Create a release branch `git checkout -b keithduncan/release/1.1.2`
1. Update [version/version.go](version/version.go) with the new version number
1. Update [CHANGELOG.md](CHANGELOG.md) with the release notes
1. Push your branch and open a pull request
1. Once CI has passed, merge your pull request
1. Open the default build for the merge commit on the [main pipeline](.buildkite/pipeline.yml)
	1. Wait for the tests to pass, then unblock the pipeline to release
	1. Wait for the build to finish and create a git tag for us
	1. Update the created GitHub release, and copy the changelog entry into the description
1. Create a new build on the [buildkite-agent-scaler-publish pipeline](https://buildkite.com/buildkite-aws-stack/buildkite-agent-scaler-publish) supply `refs/tags/$TAG` for the *Branch* field
to update the AWS Serverless Application Repository
	1. Ideally this pipeline would be automatically triggered but the pipelines
	are in separate Buildkite organisations in order to use different agent pools
	1. Unblock the pipeline to release to the AWS Serverless Application Repository
1. Update the Elastic CI Stack for AWS to import the newly released scaler
	1. Create a branch in the Elastic CI Stack for AWS repository
	1. Update the [aws-stack.yml `Autoscaling`](https://github.com/buildkite/elastic-ci-stack-for-aws/blob/9596a11992fd8ba5eaef747b9f73be9111365264/templates/aws-stack.yml#L1160-L1176) resource’s `SemanticVersion` property
	1. Push your branch and open a pull request
	1. Wait for CI to pass
	1. Create a stack with the branch’s published template to verify functionality
	1. Merge your Elastic CI Stack branch