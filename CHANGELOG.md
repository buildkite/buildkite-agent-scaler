# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [v1.0.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.0.0) (2019-05-05)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v0.4.1...v1.0.0)

### Changed
- Support scaling based on waiting jobs behind wait steps [#17](https://github.com/buildkite/buildkite-agent-scaler/pull/17) (@lox)
- Add a configurable scale factor to scale in/out [#13](https://github.com/buildkite/buildkite-agent-scaler/pull/13) (@lox)
- Support reading Agent Token from SSM Parameter Store [#15](https://github.com/buildkite/buildkite-agent-scaler/pull/15) (@lox)
- Respect poll duration header from agent api [#14](https://github.com/buildkite/buildkite-agent-scaler/pull/14) (@lox)
- Add detailed readme [#16](https://github.com/buildkite/buildkite-agent-scaler/pull/16) (@lox)

## [v0.4.1](https://github.com/buildkite/buildkite-agent-scaler/tree/v0.4.1) (2019-04-16)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v0.4.0...v0.4.1)

### Changed
- Public to newer aws regions (ca-central-1, eu-north-1 and eu-west-3) [#11](https://github.com/buildkite/buildkite-agent-scaler/pull/11) (@lox)

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
