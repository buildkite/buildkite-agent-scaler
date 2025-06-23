# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [v1.9.5](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.9.4...v1.9.5) (2025-06-23)
* fix: scope down ElasticCIModeSSMAndEC2 policy [#221](https://github.com/buildkite/buildkite-agent-scaler/pull/221) ([scadu](https://github.com/scadu))

## [v1.9.4](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.9.3...v1.9.4) (2025-06-11)
* fix: correct permissions to handle instances in ElasticCIMode [#219](https://github.com/buildkite/buildkite-agent-scaler/pull/219) ([scadu](https://github.com/scadu))
* fix: bump Go to 1.24 to mitigate CVE-2025-22871 [#218](https://github.com/buildkite/buildkite-agent-scaler/pull/218) ([scadu](https://github.com/scadu))

## [v1.9.3](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.9.2...v1.9.3) (2025-06-06)
* feat: graceful scale-in for Elastic CI Stack [#212](https://github.com/buildkite/buildkite-agent-scaler/pull/212) ([scadu](https://github.com/scadu))

## [v1.9.2](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.9.1...v1.9.2) (2025-06-02)

* chore: Update AWS SDK to v2 [#215](https://github.com/buildkite/buildkite-agent-scaler/pull/215) ([scadu](https://github.com/scadu) [walsm232](https://github.com/walsm232)) 
* Bump github.com/aws/aws-lambda-go from 1.47.0 to 1.48.0 [#213](https://github.com/buildkite/buildkite-agent-scaler/pull/213) ([dependabot[bot]](https://github.com/apps/dependabot))
* Update CODEOWNERS [#208](https://github.com/buildkite/buildkite-agent-scaler/pull/208) ([karensawrey](https://github.com/karensawrey))
* Create CODEOWNERS [#206](https://github.com/buildkite/buildkite-agent-scaler/pull/206) ([karensawrey](https://github.com/karensawrey))

## [1.9.1](https://github.com/buildkite/buildkite-agent-scaler/tree/1.9.1) (2025-01-20)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.9.0...1.9.1)

### Changed
- Add an option to override the agent endpoint URL via flag and env var [#203](https://github.com/buildkite/buildkite-agent-scaler/pull/203) (@wolfeidau)

### Fixed
- Fix errors in README [#196](https://github.com/buildkite/buildkite-agent-scaler/pull/196) (@DrJosh9000)

### Internal
- Assume an IAM role for publishing to SAR S3 bucket [#201](https://github.com/buildkite/buildkite-agent-scaler/pull/201) (@yob)
- Update release process to assume new IAM roles via OIDC [#202](https://github.com/buildkite/buildkite-agent-scaler/pull/202) (@yob)
- Update the role used to publish SAR [#200](https://github.com/buildkite/buildkite-agent-scaler/pull/200) (@yob)
- Bump github.com/aws/aws-sdk-go from 1.54.19 to 1.55.5 [#192](https://github.com/buildkite/buildkite-agent-scaler/pull/192) (@dependabot[bot])

## [v1.9.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.9.0) (2024-07-29)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.8.0...v1.9.0)

### Changed
- Enable subnet and security group configuration in Cloudformation [#191](https://github.com/buildkite/buildkite-agent-scaler/pull/191) (@bnovick)

## [v1.8.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.8.0) (2024-07-18)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.7.0...v1.8.0)

### Changed
- The scaler will run at least once, regardless of `LAMBDA_TIMEOUT` [#188](https://github.com/buildkite/buildkite-agent-scaler/pull/188) (@DrJosh9000)

### Internal
- Dependabot updates [#185](https://github.com/buildkite/buildkite-agent-scaler/pull/185), [#186](https://github.com/buildkite/buildkite-agent-scaler/pull/186), [#114](https://github.com/buildkite/buildkite-agent-scaler/pull/114) (@dependabot[bot])
- Fix dependabot reviewers [#187](https://github.com/buildkite/buildkite-agent-scaler/pull/187) (@DrJosh9000)
- Added Using Cluster [#183](https://github.com/buildkite/buildkite-agent-scaler/pull/183) (@stephanieatte)
- Calm dependabot down a little [#179](https://github.com/buildkite/buildkite-agent-scaler/pull/179) (@yob)
- Update README example due to end of support for the Go 1.x runtime [#139](https://github.com/buildkite/buildkite-agent-scaler/pull/139) (@tomowatt)

## [v1.7.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.7.0) (2023-10-13)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.6.0...v1.7.0)

### Changed
- Updated lambda runtime to `provided.al2` in light of end of support for the Go 1.x runtime [#108](https://github.com/buildkite/buildkite-agent-scaler/pull/108) (@HugeIRL)

### Updated
- Bump github.com/aws/aws-sdk-go 1.45.25 [#109](https://github.com/buildkite/buildkite-agent-scaler/pull/109) [#106](https://github.com/buildkite/buildkite-agent-scaler/pull/106) (@dependabot[bot])
- Bump github.com/aws/aws-lambda-go from 1.7.0 to 1.41.0 [#107](https://github.com/buildkite/buildkite-agent-scaler/pull/107) (@dependabot[bot])

### Internal
- Add dependabot [#105](https://github.com/buildkite/buildkite-agent-scaler/pull/105) (@triarius)

## [v1.6.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.6.0) (2023-09-13)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.5.1...v1.6.0)

### Changed
- Change `EventScheduleRate` parameter to `EventSchedulePeriod` and require units [#102](https://github.com/buildkite/buildkite-agent-scaler/pull/102) (@triarius)


### Internal
- Fix scaler release does not prepend a v to the version on s3 [#99](https://github.com/buildkite/buildkite-agent-scaler/pull/99) (@triarius)

## [v1.5.1](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.5.1) (2023-08-22)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.5.0...v1.5.1)

### Added
- A new release process which will fix publish releases to S3 [#97](https://github.com/buildkite/buildkite-agent-scaler/pull/97) (@triarius)

## [v1.5.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.5.0) (2023-07-25)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.4.0...v1.5.0)

### Added
- Add MinPollInterval param [#94](https://github.com/buildkite/buildkite-agent-scaler/pull/94) (@DrJosh9000)
- Allow the event schedule rate to be configured via parameters [#93](https://github.com/buildkite/buildkite-agent-scaler/pull/93) (@tomellis91)

### Fixed
- DescribeScalingActivities should be called only once per lambda instance [#95](https://github.com/buildkite/buildkite-agent-scaler/pull/95) (@DrJosh9000)
- A fix to the release process (Assume the OIDC role for release-version) [#91](https://github.com/buildkite/buildkite-agent-scaler/pull/91) (@sj26)

### Changed
- Use the metrics route scoped to a queue to get metrics for the queue [#92](https://github.com/buildkite/buildkite-agent-scaler/pull/92) (@triarius)

## [v1.4.0](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.4.0) (2023-05-17)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.3.2...v1.4.0)

### Added
- A parameter to configure logs retention [#75](https://github.com/buildkite/buildkite-agent-scaler/pull/75) (@Amir-Ahmad)
- A parameter to limit pagination of autoscaling:DescribeScalingActivity [#81](https://github.com/buildkite/buildkite-agent-scaler/pull/81) (@triarius)
- A parameter for stack name and use it in lambda function description [#70](https://github.com/buildkite/buildkite-agent-scaler/pull/70) (@ellsclytn)
- A parameter to allow running scaler with a fixed size instance buffer [#72](https://github.com/buildkite/buildkite-agent-scaler/pull/72) (@wbond)

### Changed
- Allow releasing development versions of buildkite-agent-scaler to an "edge" serverless repo [#83](https://github.com/buildkite/buildkite-agent-scaler/pull/83) (@triarius)

### Updated
- Update go 1.15 -> 1.19 [#77](https://github.com/buildkite/buildkite-agent-scaler/pull/77) (@moskyb)
- Bump github.com/aws/aws-sdk-go to 1.34.0 [#78](https://github.com/buildkite/buildkite-agent-scaler/pull/78) [#76](https://github.com/buildkite/buildkite-agent-scaler/pull/76) (@dependabot[bot])
- Improvements to code formatting and clarity [#88](https://github.com/buildkite/buildkite-agent-scaler/pull/88) (@moskyb)
- Improvements to CI [#82](https://github.com/buildkite/buildkite-agent-scaler/pull/82) (@triarius) [#87](https://github.com/buildkite/buildkite-agent-scaler/pull/87) [#86](https://github.com/buildkite/buildkite-agent-scaler/pull/86) (@yob)

## [1.3.2](https://github.com/buildkite/buildkite-agent-scaler/tree/1.3.2) (2022-08-04)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.3.1...1.3.2)

### Fixed
- Update IAM policy to allow describing scaling activities [#61](https://github.com/buildkite/buildkite-agent-scaler/pull/61) (@zl4bv)

## [v1.3.1](https://github.com/buildkite/buildkite-agent-scaler/tree/v1.3.1) (2022-06-09)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.3.0...v1.3.1)

### Changed
- Fix CI snafu that caused 1.3.0 to never be properly released [#64](https://github.com/buildkite/buildkite-agent-scaler/pull/64) (@moskyb)

## [1.3.0](https://github.com/buildkite/buildkite-agent-scaler/tree/1.3.0) (2022-06-07)
[Full Changelog](https://github.com/buildkite/buildkite-agent-scaler/compare/v1.2.0...1.3.0)

### Changed
- Add ability to use permissions boundary [#62](https://github.com/buildkite/buildkite-agent-scaler/pull/62) (@kwong-chong-lfs)

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
