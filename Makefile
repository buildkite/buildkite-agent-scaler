.PHONY: all clean build

all: build

clean:
	-rm handler.zip

# -----------------------------------------
# Lambda management

LAMBDA_S3_BUCKET := buildkite-aws-stack-lox
LAMBDA_S3_BUCKET_PATH := /

ifdef BUILDKITE_BUILD_NUMBER
	LD_FLAGS := -s -w -X version.Build=$(BUILDKITE_BUILD_NUMBER)
endif

ifndef BUILDKITE_BUILD_NUMBER
	LD_FLAGS := -s -w
endif

build: handler.zip

create-application-version: packaged.yml
	aws serverlessrepo create-application-version \
		--region us-east-1 \
		--application-id arn:aws:serverlessrepo:us-east-1:253121499730:applications/buildkite-elastic-ci-scaler \
		--template-body file://packaged.yml \
		--semantic-version "$(VERSION)" \
		--source-code-url "https://github.com/buildkite/buildkite-agent-scheduler/commit/$(git rev-parse HEAD)"

packaged.yml: template.yaml handler.zip
	sam package \
		--s3-bucket buildkite-sar-us-east-1 \
		--s3-prefix buildkite-agent-scaler \
		--output-template-file packaged.yml

handler.zip: lambda/handler
	zip -9 -v -j $@ "$<"

lambda/handler: lambda/main.go
	docker run \
		--volume go-module-cache:/go/pkg/mod \
		--volume $(PWD):/go/src/github.com/buildkite/buildkite-agent-scaler \
		--workdir /go/src/github.com/buildkite/buildkite-agent-scaler \
		--rm golang:1.15 \
		go build -ldflags="$(LD_FLAGS)" -o ./lambda/handler ./lambda
	chmod +x lambda/handler

lambda-sync: handler.zip
	aws s3 sync \
		--acl public-read \
		--exclude '*' --include '*.zip' \
		. s3://$(LAMBDA_S3_BUCKET)$(LAMBDA_S3_BUCKET_PATH)

lambda-versions:
	aws s3api head-object \
		--bucket ${LAMBDA_S3_BUCKET} \
		--key handler.zip --query "VersionId" --output text
