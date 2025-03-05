.PHONY: all clean build

all: build

# -----------------------------------------
# Lambda management

LAMBDA_S3_BUCKET := buildkite-aws-stack-lox
LAMBDA_S3_BUCKET_PATH := /
ARCHITECTURES := amd64 arm64
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

clean:
	-rm handler.zip
	-rm $(patsubst %,bootstrap-%,$(ARCHITECTURES))

build: handler.zip

handler.zip: bootstrap
	zip -9 -v -j $@ $(patsubst %,bootstrap-%,$(ARCHITECTURES))

bootstrap: lambda/main.go
	@for ARCH in $(ARCHITECTURES); do \
		echo "Building for $$ARCH"; \
		docker run \
			--env GOCACHE=/go/cache \
			--env CGO_ENABLED \
			--env GOARCH=$$ARCH \
			--user $(USER) \
			--volume $(PWD):/app \
			--workdir /app \
			--rm \
			golang:1.22 \
			go build -ldflags="$(LD_FLAGS)" -buildvcs="$(BUILDVSC_FLAG)" -tags lambda.norpc -o bootstrap-$$ARCH ./lambda; \
	done
	@echo "Build completed."

lambda-sync: handler.zip
	aws s3 sync \
		--acl public-read \
		--exclude '*' --include '*.zip' \
		. s3://$(LAMBDA_S3_BUCKET)$(LAMBDA_S3_BUCKET_PATH)

lambda-versions:
	aws s3api head-object \
		--bucket ${LAMBDA_S3_BUCKET} \
		--key handler.zip --query "VersionId" --output text
