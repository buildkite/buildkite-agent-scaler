# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [v1.2.0](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.1.3...v1.2.0) (2021-11-22)

### Added

* Restore last scale in and out event times from Auto Scale group activity [#52](https://github.com/buildkite/buildkite-agent-scaler/pull/52) (@gu-kevin)
* `DisableScaleIn` parameter to template [#59](https://github.com/buildkite/buildkite-agent-scaler/pull/59)

## [v1.1.3](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.1.2...v1.1.3) (2021-10-28)

* Fix crash when publishing CloudWatch metrics [#56](https://github.com/buildkite/buildkite-agent-scaler/pull/56) (@eleanorakh)

## [v1.1.2](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.1.1...v1.1.2) (2021-10-25)

* Fix BuildkiteAgentTokenParameter when using AWS Secrets Manager reference syntax [#53](https://github.com/buildkite/buildkite-agent-scaler/pull/53)
* Add new SCALE_ONLY_AFTER_ALL_EVENT environment variable to respect cooldown after scale events [#51](https://github.com/buildkite/buildkite-agent-scaler/pull/51) @gu-kevin

## [v1.1.0](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.0.2...v1.1.0) (2021-04-14)

* When the elastic stack is very small (<=2 running instances), consider adding a new instance when we suspect the current instances are shutting down and there's pending jobs [#40](https://github.com/buildkite/buildkite-agent-scaler/pull/40) ([nitrocode](https://github.com/dbaggerman))

## [v1.0.2](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.0.1...v1.0.2) (2020-10-19)

* Respect Auto Scaling Group MaxSize and MinSize [#37](https://github.com/buildkite/buildkite-agent-scaler/pull/37) ([nitrocode](https://github.com/nitrocode))
* Support 6 more regions: af-south-1, ap-east-1. ca-central-1, eu-south-1, eu-west-3, eu-north-1, me-south-1 [#33](https://github.com/buildkite/buildkite-agent-scaler/pull/33) ([JuanitoFatas](https://github.com/JuanitoFatas))

## [v1.0.1](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.0.0...v1.0.1) (2019-11-27)

* Handle HTTP errors from metrics API [#23](https://github.com/buildkite/buildkite-agent-scaler/pull/23) ([pda](https://github.com/pda))
* Fix suspected typo in lambda env var [#22](https://github.com/buildkite/buildkite-agent-scaler/pull/22) ([amu-g](https://github.com/amu-g))
* Correct required environment variables in README [#19](https://github.com/buildkite/buildkite-agent-scaler/pull/19) ([mikenicholson](https://github.com/mikenicholson))

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
