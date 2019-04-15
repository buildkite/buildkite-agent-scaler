# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [v0.4.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v0.4.0) (2019-03-25)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v0.3.0...v0.4.0)

### Changed
- Add DISABLE_SCALE_IN param to opt-out of scale in [#10](https://github.com/buildkite/buildkite-agent-scaler/pull/10) (@lox)
- Factor running jobs into scaling decisions [#9](https://github.com/buildkite/buildkite-agent-scaler/pull/9) (@lox)
- Add scale-in cooldown support [#6](https://github.com/buildkite/buildkite-agent-scaler/pull/6) (@etaoins)
- Release to github [#5](https://github.com/buildkite/buildkite-agent-scaler/pull/5) (@lox)

## [v0.3.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v0.3.0) (2019-02-27)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/75dc66865e51...v0.3.0)

### Changed
- Add an invocation counter to detect cold starts [#4](https://github.com/buildkite/buildkite-agent-scaler/pull/4) (@lox)
- Add cloudwatch metrics publishing [#3](https://github.com/buildkite/buildkite-agent-scaler/pull/3) (@lox)
