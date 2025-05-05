.PHONY: all clean build

all: build

clean:
	-rm handler.zip bootstrap

# -----------------------------------------
# Lambda management

LAMBDA_S3_BUCKET := buildkite-aws-stack-lox
LAMBDA_S3_BUCKET_PATH := /
export GOARCH := amd64
export CGO_ENABLED := 0

ifdef BUILDKITE_BUILD_NUMBER
	LD_FLAGS := -s -w -X version.Build=$(BUILDKITE_BUILD_NUMBER)
	BUILDVSC_FLAG := false
	USER := 0:0
endif

ifndef BUILDKITE_BUILD_NUMBER
	LD_FLAGS := -s -w
	BUILDVSC_FLAG := true
	USER := "$(shell id -u):$(shell id -g)"
endif

build: handler.zip

handler.zip: bootstrap
	zip -9 -v -j $@ "$<"

bootstrap: lambda/main.go
	docker run \
		--env GOCACHE=/go/cache \
		--env GOARCH \
		--env CGO_ENABLED \
		--user $(USER) \
		--volume $(PWD):/app \
		--workdir /app \
		--rm \
		golang:1.22 \
		go build -ldflags="$(LD_FLAGS)" -buildvcs="$(BUILDVSC_FLAG)" -tags lambda.norpc -o bootstrap ./lambda

lambda-sync: handler.zip
	aws s3 sync \
		--acl public-read \
		--exclude '*' --include '*.zip' \
		. s3://$(LAMBDA_S3_BUCKET)$(LAMBDA_S3_BUCKET_PATH)

lambda-versions:
	aws s3api head-object \
		--bucket ${LAMBDA_S3_BUCKET} \
		--key handler.zip --query "VersionId" --output text
