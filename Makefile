.PHONY: all clean build

all: build

clean:
	-rm handler.zip

# -----------------------------------------
# Lambda management

LAMBDA_S3_BUCKET := buildkite-aws-stack-lox
LAMBDA_S3_BUCKET_PATH := /

build: handler.zip

handler.zip: lambda/handler
	zip -9 -v -j $@ "$<"

lambda/handler: lambda/main.go
	docker run \
		--volume go-module-cache:/go/pkg/mod \
		--volume $(PWD):/code \
		--workdir /code \
		--rm golang:1.11 \
		go build -ldflags="$(FLAGS)" -o ./lambda/handler ./lambda
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
